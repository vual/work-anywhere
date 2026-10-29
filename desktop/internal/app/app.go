package app

import (
	"context"
	"fmt"
	"os"

	"github.com/hurf/work-anywhere/desktop/internal/appinfo"
	"github.com/hurf/work-anywhere/desktop/internal/bridge"
	"github.com/hurf/work-anywhere/desktop/internal/cli"
	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/daemon"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

type BuildInfo = appinfo.BuildInfo

func Run(ctx context.Context, args []string, build BuildInfo) error {
	return run(ctx, args, build, codex.ResolveRuntime, bridge.Run)
}

func run(ctx context.Context, args []string, build BuildInfo, resolve func([]string) (protocol.RuntimeDescriptor, error), runBridge func(context.Context, []string, string) error) error {
	if len(args) >= 2 && args[0] == "daemon" && args[1] == "run" {
		return daemon.Run(ctx, build.Version)
	}
	if len(args) > 0 && args[0] == "bridge" {
		args = args[1:]
		if len(args) == 0 {
			args = []string{"app-server"}
		}
	}
	if codex.IsPlainAppServer(args) {
		return runBridge(ctx, args, build.Version)
	}
	if cli.IsCommand(args) {
		return cli.Run(ctx, args, build)
	}

	runtime, err := resolve(args)
	if err != nil {
		return err
	}
	code, err := codex.RunPassthrough(ctx, runtime, args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("Codex exited with code %d", e.code) }
func (e *exitError) ExitCode() int { return e.code }
