package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
)

// An independent process exercises OS pipe backpressure, not just a Reader mock.
func TestOutputChild(t *testing.T) {
	switch os.Getenv("WAW_OUTPUT_TEST") {
	case "stdout":
		block := bytes.Repeat([]byte("x"), 1<<20)
		for n := 0; n < 65; n++ {
			_, _ = os.Stdout.Write(block)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), 128<<10))
		_, _ = os.Stderr.WriteString("\nafter-long-line\n")
		os.Exit(0)
	}
}

func TestOversizedStdoutKillsGenerationWithoutStdinLock(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutputChild$")
	cmd.Env = append(os.Environ(), "WAW_OUTPUT_TEST=stdout")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	i.cmd, i.state = cmd, "ready"
	response := make(chan []byte, 1)
	i.requests[`"pending"`] = requestOrigin{internal: true, method: "thread/read", response: response}
	i.serverWriteMu.Lock()
	defer i.serverWriteMu.Unlock()
	done := make(chan struct{})
	go func() { i.readServer(i.generation, stdout); i.waitServer(i.generation, cmd); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("reader/process remained blocked")
	}
	select {
	case raw := <-response:
		if !strings.Contains(string(raw), "stdout read failed") {
			t.Fatalf("lost diagnostic: %s", raw)
		}
	default:
		t.Fatal("pending request was not completed")
	}
	if i.cmd != nil || i.state != "error" || !strings.Contains(i.lastError, "maximum frame") {
		t.Fatalf("bad exit state: %+v", i.Snapshot())
	}
}

func TestStderrLongLineDrainsToNextDiagnostic(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutputChild$")
	cmd.Env = append(os.Environ(), "WAW_OUTPUT_TEST=stderr")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan struct{})
	go func() { i.readServerStderr(i.generation, stderr); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stderr stopped draining")
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if i.lastError != "after-long-line" {
		t.Fatalf("next diagnostic lost: %q", i.lastError)
	}
	i.readServerStderr(i.generation, strings.NewReader(strings.Repeat("x", 128<<10)))
	if len(i.lastError) > (64<<10)+len(" [truncated]") || !strings.HasSuffix(i.lastError, " [truncated]") {
		t.Fatal("diagnostic is not bounded")
	}
}

func TestOldGenerationOutputErrorIgnored(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.generation = 2
	i.state = "ready"
	i.failServerOutput(1, "stdout", errors.New("old"))
	if i.state != "ready" || i.outputError != "" {
		t.Fatal("old generation changed new instance")
	}
	old := uint64(1)
	if err := i.startGeneration(&old); err != nil || i.state != "ready" {
		t.Fatal("late recovery changed new generation")
	}
}

func TestInitializeDoesNotRenewAutomaticRecoveryBudget(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.autoRestartAttempted = true
	i.requests[`"init"`] = requestOrigin{internal: true, method: "initialize"}
	i.handleServerFrame(i.generation, []byte(`{"jsonrpc":"2.0","id":"init","result":{}}`))
	if !i.autoRestartAttempted {
		t.Fatal("initialize granted another automatic recovery")
	}
}

func TestSerializedImageLimitRejectsBeforeWrite(t *testing.T) {
	i, writer, cleanup := testInstance(t)
	defer cleanup()
	// A 48 MiB image encodes to 64 MiB before the JSON envelope is added.
	raw, _ := json.Marshal(map[string]any{"threadId": "thread", "input": []map[string]any{{"type": "image", "imageUrl": "data:image/png;base64," + strings.Repeat("A", ipc.DefaultMaxFrame)}}})
	_, err := i.Call(context.Background(), "turn/start", raw)
	if err == nil || mobileControlError(err).Code != "too_large" {
		t.Fatalf("error = %v", err)
	}
	if writer.Len() != 0 || len(i.requests) != 0 || len(i.pendingTurnSettings) != 0 {
		t.Fatal("oversized request mutated or wrote state")
	}
}

func TestTimedOutMutationPreventsAutomaticRecovery(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = i.Call(ctx, "thread/name/set", json.RawMessage(`{"threadId":"t","name":"x"}`))
	if !i.uncertainWrite {
		t.Fatal("unknown write result was forgotten")
	}
}

func TestRecoveryEligibilityAfterProcessExit(t *testing.T) {
	for _, kind := range []string{"idle", "turn", "action", "pendingTurn", "write", "unknownWrite", "alreadyRecovered"} {
		t.Run(kind, func(t *testing.T) {
			i, _, cleanup := testInstance(t)
			defer cleanup()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			i.manager.ctx = ctx
			i.cachedInitializeRequest = []byte(`{"method":"initialize"}`)
			switch kind {
			case "turn":
				i.activeTurns["t"] = "turn"
			case "action":
				i.actions["a"] = &pendingAction{}
			case "pendingTurn":
				i.pendingTurnStarts["t"] = true
			case "write":
				i.requests[`"w"`] = requestOrigin{method: "thread/name/set"}
			case "unknownWrite":
				i.uncertainWrite = true
			case "alreadyRecovered":
				i.autoRestartAttempted = true
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutputChild$")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			i.waitServer(i.generation, cmd)
			want := kind == "idle" || kind == "alreadyRecovered"
			if i.autoRestartAttempted != want {
				t.Fatalf("recovery attempt=%v", i.autoRestartAttempted)
			}
		})
	}
}
