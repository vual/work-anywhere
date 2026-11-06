//go:build windows

package autostart

import (
	"errors"

	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName  = "WorkAnywhere"
)

// Enable points the current user's login entry at this executable.
func Enable() error {
	self, err := currentExecutable()
	if err != nil {
		return err
	}
	return enable(valueName, self)
}

// Disable removes the current user's login entry. A missing entry is not an error.
func Disable() error { return disable(valueName) }

// Get reports the current user's login entry.
func Get() (Status, error) {
	self, err := currentExecutable()
	if err != nil {
		return Status{Supported: true}, err
	}
	return get(valueName, self)
}

func enable(name, executable string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(name, Command(executable))
}

func disable(name string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if err = key.DeleteValue(name); errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func get(name, executable string) (Status, error) {
	status := Status{Supported: true}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	defer key.Close()
	command, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Enabled = true
	status.Command = command
	status.Current = paths.CanonicalPath(commandExecutable(command)) == paths.CanonicalPath(executable)
	return status, nil
}
