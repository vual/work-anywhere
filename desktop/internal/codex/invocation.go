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
	if len(args) == 0 || !strings.EqualFold(args[0], "app-server") {
		return false
	}
	if len(args) > 1 {
		switch strings.ToLower(args[1]) {
		case "generate-json-schema", "generate-ts", "daemon":
			return false
		}
	}
	return true
}

func AppServerArgs(original []string) []string {
	args := append([]string(nil), original...)
	if len(args) == 0 || !strings.EqualFold(args[0], "app-server") {
		args = append([]string{"app-server"}, args...)
	}
	filtered := args[:0]
	for i := 0; i < len(args); i++ {
		if args[i] == "--listen" && i+1 < len(args) {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--listen=") {
			continue
		}
		filtered = append(filtered, args[i])
	}
	return append(filtered, "--listen", "stdio://")
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
