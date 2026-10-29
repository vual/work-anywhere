//go:build windows

package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func jobCommand(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestJobHelper$")
	cmd.Env = append(os.Environ(), "WAW_JOB_TEST_MODE="+mode)
	ConfigureManagedChild(cmd)
	return cmd
}

// Helpers never run user tools or access user session/configuration files.
func TestJobHelper(t *testing.T) {
	mode := os.Getenv("WAW_JOB_TEST_MODE")
	if mode == "" {
		return
	}
	input := bufio.NewReader(os.Stdin)
	switch mode {
	case "leaf":
		fmt.Println(os.Getpid())
		_, _ = io.Copy(io.Discard, input)
	case "tree":
		if _, err := input.ReadString('\n'); err != nil {
			os.Exit(2)
		}
		cmd := jobCommand("leaf")
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		if cmd.Start() != nil {
			os.Exit(3)
		}
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		fmt.Print(line)
		_, _ = io.Copy(io.Discard, input)
		runtime.KeepAlive(stdin)
	case "owner":
		group, _ := NewGroup()
		cmd := jobCommand("tree")
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		if cmd.Start() != nil || group.Add(cmd) != nil {
			os.Exit(4)
		}
		_, _ = io.WriteString(stdin, "spawn\n")
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		fmt.Printf("%d %s", cmd.Process.Pid, line)
		_, _ = input.ReadString('\n')
		runtime.KeepAlive(group)
		runtime.KeepAlive(stdin)
	}
	os.Exit(0)
}

func startJobTestChild(t *testing.T, mode string, parent windows.Handle) (*exec.Cmd, io.WriteCloser, *bufio.Reader, func()) {
	t.Helper()
	cmd := jobCommand(mode)
	cmd.SysProcAttr.ParentProcess = syscall.Handle(parent)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	cleanup := func() { once.Do(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
	t.Cleanup(cleanup)
	return cmd, stdin, bufio.NewReader(stdout), cleanup
}

func jobLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() { line, _ := r.ReadString('\n'); ch <- strings.TrimSpace(line) }()
	select {
	case line := <-ch:
		if line == "" {
			t.Fatal("helper closed stdout")
		}
		return line
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not respond")
		return ""
	}
}

func processWaitHandle(t *testing.T, pid int) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	return h
}

func expectProcessExit(t *testing.T, h windows.Handle) {
	t.Helper()
	state, err := windows.WaitForSingleObject(h, 10000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("process did not exit: %d %v", state, err)
	}
}

func TestGroupIndependentRootsAndDescendants(t *testing.T) {
	group, _ := NewGroup()
	defer group.Close()
	parent, _, parentOut, _ := startJobTestChild(t, "leaf", 0)
	_ = jobLine(t, parentOut)
	ph, err := windows.OpenProcess(windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE, false, uint32(parent.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(ph)
	root, in, out, stop := startJobTestChild(t, "tree", ph)
	if err = group.Add(root); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(in, "spawn\n")
	pid, err := strconv.Atoi(jobLine(t, out))
	if err != nil {
		t.Fatal(err)
	}
	descendant := processWaitHandle(t, pid)
	other, _, otherOut, _ := startJobTestChild(t, "leaf", ph)
	if err = group.Add(other); err != nil {
		t.Fatal(err)
	}
	_ = jobLine(t, otherOut)
	otherHandle := processWaitHandle(t, other.Process.Pid)
	group.mu.Lock()
	count := len(group.members)
	group.mu.Unlock()
	if count != 2 {
		t.Fatalf("expected independent jobs, got %d", count)
	}
	stop()
	expectProcessExit(t, descendant)
	state, _ := windows.WaitForSingleObject(otherHandle, 0)
	if state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("one root exiting killed another root")
	}
	if err = group.Close(); err != nil {
		t.Fatal(err)
	}
	if err = group.Close(); err != nil {
		t.Fatal("Close is not idempotent")
	}
	expectProcessExit(t, otherHandle)
}

func TestGroupCrashCleanup(t *testing.T) {
	_, in, out, _ := startJobTestChild(t, "owner", 0)
	var root, child int
	if _, err := fmt.Sscanf(jobLine(t, out), "%d %d", &root, &child); err != nil {
		t.Fatal(err)
	}
	rh, ch := processWaitHandle(t, root), processWaitHandle(t, child)
	_, _ = io.WriteString(in, "exit\n")
	expectProcessExit(t, rh)
	expectProcessExit(t, ch)
}

func TestGroupConcurrentAddClose(t *testing.T) {
	group, _ := NewGroup()
	var wg sync.WaitGroup
	for range 8 {
		cmd, _, _, _ := startJobTestChild(t, "leaf", 0)
		wg.Go(func() {
			if err := group.Add(cmd); err != nil {
				_ = cmd.Process.Kill()
			}
		})
	}
	wg.Go(func() { _ = group.Close() })
	wg.Wait()
	_ = group.Close()
	group.mu.Lock()
	closed := group.closed
	group.mu.Unlock()
	if !closed {
		t.Fatal("group not closed")
	}
	if group.Add(nil) == nil {
		t.Fatal("closed group accepted an Add")
	}
}
