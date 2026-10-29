package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func TestMain(m *testing.M) {
	switch os.Getenv("WAW_DISPATCH_TEST") {
	case "entry":
		_ = os.Setenv("WAW_DISPATCH_TEST", "official")
		err := run(context.Background(), os.Args[1:], BuildInfo{}, func([]string) (protocol.RuntimeDescriptor, error) {
			return protocol.RuntimeDescriptor{RealExecutable: os.Args[0]}, nil
		}, func(context.Context, []string, string) error { return errors.New("unexpected daemon/bridge dispatch") })
		if err != nil {
			var exit interface{ ExitCode() int }
			if errors.As(err, &exit) {
				os.Exit(exit.ExitCode())
			}
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(90)
		}
		os.Exit(0)
	case "official":
		_, _ = io.ReadAll(os.Stdin)
		time.Sleep(40 * time.Millisecond) // output arrives after stdin EOF
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "cwd": cwd, "cli": os.Getenv("CODEX_CLI_PATH"), "runtime": os.Getenv("CODEX_APP_RUNTIME_CLI_PATH")})
		_, _ = os.Stderr.WriteString("official diagnostic\n")
		os.Exit(7)
	case "blocking":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestPassthroughCancellation(t *testing.T) {
	t.Setenv("WAW_DISPATCH_TEST", "blocking")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	code, err := codex.RunPassthrough(ctx, protocol.RuntimeDescriptor{RealExecutable: os.Args[0]}, nil, nil, io.Discard, io.Discard)
	if ctx.Err() == nil || code == 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("passthrough did not cancel: code=%d err=%v", code, err)
	}
}

func TestOneShotEntryPreservesProcessContract(t *testing.T) {
	for _, args := range [][]string{
		{"-c", "features.code_mode_host=true", "app-server", "--print-startup-config", "--startup-client-info", `{"name":"codex_desktop","version":"test"}`},
		{"app-server", "--print-startup-config", "--startup-sign-out"},
		{"app-server", "--help"}, {"app-server", "proxy"}, {"app-server", "generate-json-schema", "--out", "ignored"},
		{"app-server", "--listen=ws://127.0.0.1:9"}, {"app-server", "--future-flag"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), "WAW_DISPATCH_TEST=entry", "CODEX_APP_RUNTIME_CLI_PATH=bridge")
			cmd.Dir = t.TempDir()
			var stderr strings.Builder
			cmd.Stderr = &stderr
			stdout, err := cmd.Output() // nil stdin is the Desktop policy check's closed stdin
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("exit: %v; %s", err, stderr.String())
			}
			if stderr.String() != "official diagnostic\n" {
				t.Fatalf("stderr altered: %q", stderr.String())
			}
			var result struct {
				Args              []string
				CWD, CLI, Runtime string
			}
			if err := json.Unmarshal(stdout, &result); err != nil {
				t.Fatalf("stdout: %s (%v)", stdout, err)
			}
			if !reflect.DeepEqual(result.Args, args) || !strings.EqualFold(result.CWD, cmd.Dir) || result.CLI != os.Args[0] || result.Runtime != os.Args[0] {
				t.Fatalf("process contract: %+v", result)
			}
		})
	}
}

func TestStdioUsesBridgeWithoutPassthrough(t *testing.T) {
	args := []string{"-c", "x=true", "app-server", "--analytics-default-enabled"}
	called := false
	err := run(context.Background(), args, BuildInfo{}, func([]string) (protocol.RuntimeDescriptor, error) {
		t.Fatal("passthrough selected")
		return protocol.RuntimeDescriptor{}, nil
	}, func(_ context.Context, got []string, _ string) error {
		called = reflect.DeepEqual(got, args)
		return nil
	})
	if err != nil || !called {
		t.Fatalf("dispatch: %v %v", called, err)
	}
}
