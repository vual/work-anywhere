//go:build windows

package processinfo

import (
	"strconv"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// Alive reports whether pid is a running process. When startTime is a
// non-empty Windows FILETIME (100ns ticks since 1601) it must match the
// process creation time, which guards against PID reuse.
func Alive(pid int, startTime string) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err = windows.GetExitCodeProcess(handle, &code); err != nil || code != stillActive {
		return false
	}
	if startTime == "" {
		return true
	}
	expected, err := strconv.ParseUint(startTime, 10, 64)
	if err != nil {
		return true
	}
	var creation, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return true
	}
	actual := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	return actual == expected
}
