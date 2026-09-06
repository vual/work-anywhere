package codex

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func IsPlainAppServer(args []string) bool {
	index := appServerIndex(args)
	if index < 0 {
		return false
	}
	if index+1 < len(args) {
		switch strings.ToLower(args[index+1]) {
		case "generate-json-schema", "generate-ts", "daemon":
			return false
		}
	}
	return true
}

func AppServerArgs(original []string) []string {
	args := append([]string(nil), original...)
	index := appServerIndex(args)
	if index < 0 {
		args = append([]string{"app-server"}, args...)
		index = 0
	}
	filtered := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		if i > index && args[i] == "--listen" && i+1 < len(args) {
			i++
			continue
		}
		if i > index && strings.HasPrefix(args[i], "--listen=") {
			continue
		}
		filtered = append(filtered, args[i])
	}
	return append(filtered, "--listen", "stdio://")
}

// appServerIndex returns the position of the app-server subcommand. Codex
// accepts global options before the subcommand (for example, `-c key=value
// app-server`), so checking args[0] is not sufficient for desktop launches.
func appServerIndex(args []string) int {
	for index, arg := range args {
		if strings.EqualFold(arg, "app-server") {
			return index
		}
	}
	return -1
}

func RunPassthrough(ctx context.Context, runtime protocol.RuntimeDescriptor, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, runtime.RealExecutable, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = childEnvironment(os.Environ(), runtime.RealExecutable)
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return -1, fmt.Errorf("run real Codex: %w", err)
	}
	return 0, nil
}

func childEnvironment(base []string, realExecutable string) []string {
	out := make([]string, 0, len(base)+1)
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, "CODEX_CLI_PATH") {
			continue
		}
		out = append(out, item)
	}
	return append(out, "CODEX_CLI_PATH="+realExecutable)
}

func EnvironmentSnapshot() []string { return append([]string(nil), os.Environ()...) }
