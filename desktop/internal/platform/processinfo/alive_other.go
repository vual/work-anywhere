//go:build !windows

package processinfo

import "syscall"

// Alive reports whether pid is a running process. startTime is only checked
// on Windows, where the Claude Code session registry records it.
func Alive(pid int, _ string) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
