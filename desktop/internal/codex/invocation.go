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
	for pos := index + 1; pos < len(args); pos++ {
		key, value, inline := strings.Cut(args[pos], "=")
		switch key {
		case "--analytics-default-enabled", "--strict-config", "--stdio", "--standalone-ws":
			if inline {
				return false
			}
		case "--listen", "--cloud-skills-snapshot", "--code-mode-host", "--startup-client-info":
			if !inline {
				pos++
				if pos == len(args) {
					return false
				}
				value = args[pos]
			}
			if value == "" || (key == "--listen" && value != "stdio://") {
				return false
			}
		default:
			if !consumeGlobalOption(args, &pos) {
				return false
			}
		}
	}
	return true
}

func AppServerArgs(original []string) []string {
	args := append([]string(nil), original...)
	// stdio is the official default. In particular, never rewrite explicit
	// transports or append server flags to one-shot commands.
	return args
}

// StandaloneArguments removes only the explicit diagnostic flag, never an
// identically spelled value belonging to a config or App Server option.
func StandaloneArguments(original []string) ([]string, bool) {
	index := appServerIndex(original)
	if index < 0 || !IsPlainAppServer(original) {
		return append([]string(nil), original...), false
	}
	args := append([]string(nil), original[:index+1]...)
	found := false
	for pos := index + 1; pos < len(original); pos++ {
		start := pos
		if original[pos] == "--standalone-ws" {
			found = true
			continue
		}
		if !consumeGlobalOption(original, &pos) {
			key, _, inline := strings.Cut(original[pos], "=")
			if !inline && (key == "--listen" || key == "--cloud-skills-snapshot" || key == "--code-mode-host" || key == "--startup-client-info") {
				pos++
			}
		}
		args = append(args, original[start:pos+1]...)
	}
	return args, found
}

// DiagnosticWebSocketArgs is only used by the explicit standalone diagnostic.
// It consumes option values so a config value cannot be mistaken for a flag.
func DiagnosticWebSocketArgs(original []string, endpoint string) []string {
	index := appServerIndex(original)
	if index < 0 {
		return append([]string(nil), original...)
	}
	args := append([]string(nil), original[:index+1]...)
	for pos := index + 1; pos < len(original); pos++ {
		start := pos
		key, _, inline := strings.Cut(original[pos], "=")
		if key == "--stdio" {
			continue
		}
		if key == "--listen" {
			if !inline {
				pos++
			}
			continue
		}
		if !consumeGlobalOption(original, &pos) {
			pos = start
			if !inline && (key == "--code-mode-host" || key == "--cloud-skills-snapshot" || key == "--startup-client-info") {
				pos++
			}
		}
		if pos >= len(original) {
			return append([]string(nil), original...)
		}
		args = append(args, original[start:pos+1]...)
	}
	return append(args, "--listen", endpoint)
}

// appServerIndex returns the position of the app-server subcommand. Codex
// accepts global options before the subcommand (for example, `-c key=value
// app-server`), so checking args[0] is not sufficient for desktop launches.
func appServerIndex(args []string) int {
	for index := 0; index < len(args); index++ {
		if args[index] == "app-server" {
			return index
		}
		if !consumeGlobalOption(args, &index) {
			return -1
		}
	}
	return -1
}

// Only options understood by this adapter are eligible for supervision. Unknown
// syntax is passed to the official CLI, which remains the command-line authority.
func consumeGlobalOption(args []string, pos *int) bool {
	arg := args[*pos]
	key, value, inline := strings.Cut(arg, "=")
	if strings.HasPrefix(arg, "-c") && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
		return true
	}
	switch key {
	case "-c", "--config", "--enable", "--disable":
		if inline {
			return value != ""
		}
		*pos++
		return *pos < len(args) && args[*pos] != ""
	default:
		return false
	}
}

func RunPassthrough(ctx context.Context, runtime protocol.RuntimeDescriptor, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, runtime.RealExecutable, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = ChildEnvironment(os.Environ(), runtime.RealExecutable)
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return -1, fmt.Errorf("run real Codex: %w", err)
	}
	return 0, nil
}

func ChildEnvironment(base []string, realExecutable string) []string {
	out := make([]string, 0, len(base)+1)
	hadRuntime := false
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, "CODEX_CLI_PATH") {
			continue
		}
		if ok && strings.EqualFold(key, "CODEX_APP_RUNTIME_CLI_PATH") {
			hadRuntime = true
			continue
		}
		out = append(out, item)
	}
	if hadRuntime {
		out = append(out, "CODEX_APP_RUNTIME_CLI_PATH="+realExecutable)
	}
	return append(out, "CODEX_CLI_PATH="+realExecutable)
}

func EnvironmentSnapshot() []string { return append([]string(nil), os.Environ()...) }
