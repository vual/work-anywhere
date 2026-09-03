package bridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
)

func standaloneArguments(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == "--standalone-ws" {
			found = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, found
}

func runStandaloneWebSocket(ctx context.Context, args []string) error {
	runtime, err := codex.ResolveRuntime(args)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	endpoint := "ws://" + listener.Addr().String()
	_ = listener.Close()
	serverArgs := codex.AppServerArgs(args)
	for index := 0; index < len(serverArgs); index++ {
		if serverArgs[index] == "--listen" && index+1 < len(serverArgs) {
			serverArgs[index+1] = endpoint
		}
	}
	cmd := exec.CommandContext(ctx, runtime.RealExecutable, serverArgs...)
	cmd.Env = standaloneEnvironment(runtime.RealExecutable)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	platformprocess.ConfigureStandalone(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	var socket *websocket.Conn
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		socket, _, err = websocket.Dial(short, endpoint, nil)
		cancel()
		if err == nil {
			break
		}
		if cmd.ProcessState != nil {
			return errors.New("experimental App Server exited during startup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if socket == nil {
		return fmt.Errorf("experimental WebSocket did not start at %s: %w", endpoint, err)
	}
	defer socket.CloseNow()
	socket.SetReadLimit(ipc.DefaultMaxFrame)
	errCh := make(chan error, 2)
	var stdoutMu sync.Mutex
	go func() {
		reader := bufio.NewReaderSize(os.Stdin, 64*1024)
		for {
			line, readErr := ipc.ReadBoundedLine(reader, ipc.DefaultMaxFrame)
			if len(line) > 0 {
				payload := bytes.TrimRight(line, "\r\n")
				if writeErr := socket.Write(ctx, websocket.MessageText, payload); writeErr != nil {
					errCh <- writeErr
					return
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					_ = socket.Close(websocket.StatusNormalClosure, "desktop eof")
				}
				errCh <- readErr
				return
			}
		}
	}()
	go func() {
		writer := bufio.NewWriterSize(os.Stdout, 64*1024)
		for {
			kind, payload, readErr := socket.Read(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if kind != websocket.MessageText {
				errCh <- errors.New("App Server sent a non-text WebSocket frame")
				return
			}
			stdoutMu.Lock()
			_, writeErr := writer.Write(payload)
			if writeErr == nil {
				writeErr = writer.WriteByte('\n')
			}
			if writeErr == nil {
				writeErr = writer.Flush()
			}
			stdoutMu.Unlock()
			if writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
	err = <-errCh
	if errors.Is(err, io.EOF) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
		return nil
	}
	return err
}

func standaloneEnvironment(realExecutable string) []string {
	result := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, "CODEX_CLI_PATH") {
			continue
		}
		result = append(result, item)
	}
	return append(result, "CODEX_CLI_PATH="+realExecutable)
}
