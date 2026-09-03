package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/identity"
	"github.com/hurf/work-anywhere/desktop/internal/platform/localipc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func Run(ctx context.Context, args []string, version string) error {
	if filtered, ok := standaloneArguments(args); ok {
		return runStandaloneWebSocket(ctx, filtered)
	}
	realRuntime, err := codex.ResolveRuntime(args)
	if err != nil {
		return err
	}
	instance, err := codex.ResolveInstance()
	if err != nil {
		return err
	}

	conn, err := connectDaemon(ctx)
	if err != nil {
		return fallback(ctx, instance, realRuntime, args)
	}
	defer conn.Close()

	cwd, _ := os.Getwd()
	hello := protocol.Hello{
		Role: "bridge", Version: version, Instance: instance, Runtime: realRuntime,
		Args: append([]string(nil), args...), CWD: cwd, Environment: codex.EnvironmentSnapshot(),
	}
	if err = ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindHello, Metadata: ipc.Metadata(hello)}); err != nil {
		return fallback(ctx, instance, realRuntime, args)
	}
	ack, err := ipc.ReadFrame(conn)
	if err != nil || ack.Kind != ipc.KindHelloAck {
		return fallback(ctx, instance, realRuntime, args)
	}

	errCh := make(chan error, 2)
	var desktopSent atomic.Bool
	go desktopToDaemon(conn, errCh, &desktopSent)
	go daemonToDesktop(conn, errCh)
	err = <-errCh
	var unavailable *daemonUnavailableError
	if errors.As(err, &unavailable) && !desktopSent.Load() {
		return fallback(ctx, instance, realRuntime, args)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func connectDaemon(parent context.Context) (net.Conn, error) {
	sid, err := identity.CurrentUser()
	if err != nil {
		return nil, err
	}
	name := paths.LocalEndpoint(sid)
	ctx, cancel := context.WithTimeout(parent, 350*time.Millisecond)
	conn, err := localipc.DialPipe(ctx, name)
	cancel()
	if err == nil {
		return conn, nil
	}
	if startErr := startDaemon(); startErr != nil {
		return nil, startErr
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel = context.WithTimeout(parent, 300*time.Millisecond)
		conn, err = localipc.DialPipe(ctx, name)
		cancel()
		if err == nil {
			return conn, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon pipe unavailable: %w", err)
}

func startDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "daemon", "run")
	platformprocess.ConfigureDetached(cmd)
	return cmd.Start()
}

func desktopToDaemon(conn net.Conn, result chan<- error, sent *atomic.Bool) {
	reader := bufio.NewReaderSize(os.Stdin, 64*1024)
	for {
		line, err := ipc.ReadBoundedLine(reader, ipc.DefaultMaxFrame)
		if len(line) > 0 {
			if writeErr := ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindDesktopFrame, Payload: line}); writeErr != nil {
				result <- writeErr
				return
			}
			sent.Store(true)
		}
		if err != nil {
			_ = ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindDesktopEOF})
			result <- err
			return
		}
	}
}

func daemonToDesktop(conn net.Conn, result chan<- error) {
	writer := bufio.NewWriterSize(os.Stdout, 64*1024)
	defer writer.Flush()
	for {
		frame, err := ipc.ReadFrame(conn)
		if err != nil {
			result <- err
			return
		}
		switch frame.Kind {
		case ipc.KindServerFrame:
			if _, err = writer.Write(frame.Payload); err == nil {
				err = writer.Flush()
			}
			if err != nil {
				result <- err
				return
			}
		case ipc.KindServerExit:
			var meta struct {
				Code  int    `json:"code"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(frame.Metadata, &meta)
			if meta.Error != "" {
				result <- errors.New(meta.Error)
			} else {
				result <- io.EOF
			}
			return
		case ipc.KindError:
			result <- &daemonUnavailableError{message: string(frame.Payload)}
			return
		case ipc.KindPing:
			_ = ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindPong})
		}
	}
}

type daemonUnavailableError struct{ message string }

func (e *daemonUnavailableError) Error() string { return "daemon: " + e.message }

func fallback(ctx context.Context, instance protocol.InstanceDescriptor, rt protocol.RuntimeDescriptor, args []string) error {
	lease, already, err := identity.AcquireLease(identity.InstanceMutexName(instance.ID))
	if err != nil {
		return err
	}
	defer lease.Close()
	if already {
		return errors.New("Codex instance is already owned by daemon or another fallback process")
	}
	code, err := codex.RunPassthrough(ctx, rt, args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("real Codex exited with code %d", code)
	}
	return nil
}
