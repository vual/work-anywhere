// Package autostart registers the current executable to start the daemon when
// the user signs in. It owns only the per-user login entry; daemon start-up
// itself stays in the CLI.
package autostart

import (
	"os"
	"path/filepath"
	"strings"
)

// StartArguments are appended to the executable in the login entry.
const StartArguments = "daemon start --background"

// Status describes the current user's login entry.
type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Command   string `json:"command,omitempty"`
	// Current reports whether the entry starts this executable. An entry left
	// behind by another installation is enabled but not current.
	Current bool `json:"current"`
}

// Command returns the login command for executable.
func Command(executable string) string {
	return `"` + executable + `" ` + StartArguments
}

// commandExecutable extracts the executable path from a login command.
func commandExecutable(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
		return strings.Trim(command, `"`)
	}
	if index := strings.IndexByte(command, ' '); index >= 0 {
		return command[:index]
	}
	return command
}

func currentExecutable() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(self)
}
