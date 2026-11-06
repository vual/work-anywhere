//go:build windows

package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	"golang.org/x/sys/windows/registry"
)

const backupFileName = "codex-cli-path-backup.json"

type environmentBackup struct {
	HadValue bool   `json:"hadValue"`
	Value    string `json:"value,omitempty"`
}

func InstallBridge() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.Abs(self)
	if err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	current, _, getErr := key.GetStringValue("CODEX_CLI_PATH")
	if getErr != nil && !errors.Is(getErr, registry.ErrNotExist) {
		return getErr
	}
	if !samePath(current, self) {
		dataDir, dirErr := paths.DataDir()
		if dirErr != nil {
			return dirErr
		}
		if dirErr = os.MkdirAll(filepath.Join(dataDir, "install"), 0o700); dirErr != nil {
			return dirErr
		}
		backup := environmentBackup{HadValue: getErr == nil, Value: current}
		encoded, _ := json.MarshalIndent(backup, "", "  ")
		if err = os.WriteFile(filepath.Join(dataDir, "install", backupFileName), encoded, 0o600); err != nil {
			return err
		}
	}
	if err = key.SetStringValue("CODEX_CLI_PATH", self); err != nil {
		return err
	}
	broadcastEnvironmentChange()
	fmt.Printf("CODEX_CLI_PATH is now %s\nRestart ChatGPT Desktop to activate the bridge.\n", self)
	return nil
}

func UninstallBridge() error {
	dataDir, err := paths.DataDir()
	if err != nil {
		return err
	}
	backupPath := filepath.Join(dataDir, "install", backupFileName)
	data, readErr := os.ReadFile(backupPath)
	backup := environmentBackup{}
	if readErr == nil {
		if err = json.Unmarshal(data, &backup); err != nil {
			return err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	// Codex integration is optional: leave CODEX_CLI_PATH alone unless it
	// still points at this executable, so uninstalling never clobbers a value
	// the user or another tool owns.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	current, _, getErr := key.GetStringValue("CODEX_CLI_PATH")
	if getErr != nil && !errors.Is(getErr, registry.ErrNotExist) {
		return getErr
	}
	if getErr != nil || !samePath(current, self) {
		_ = os.Remove(backupPath)
		fmt.Println("CODEX_CLI_PATH does not point at this executable; left unchanged.")
		return nil
	}
	if backup.HadValue {
		err = key.SetStringValue("CODEX_CLI_PATH", backup.Value)
	} else {
		err = key.DeleteValue("CODEX_CLI_PATH")
		if errors.Is(err, registry.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	_ = os.Remove(backupPath)
	broadcastEnvironmentChange()
	fmt.Println("CODEX_CLI_PATH was restored. Restart ChatGPT Desktop to apply it.")
	return nil
}

func samePath(a, b string) bool { return paths.CanonicalPath(a) == paths.CanonicalPath(b) }

func broadcastEnvironmentChange() {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	name, _ := syscall.UTF16PtrFromString("Environment")
	var result uintptr
	proc.Call(0xffff, 0x001A, 0, uintptr(unsafe.Pointer(name)), 0x0002, 2000, uintptr(unsafe.Pointer(&result)))
}

// Status reports whether the user-level CODEX_CLI_PATH points at this executable.
func Status() (BridgeStatus, error) {
	status := BridgeStatus{Supported: true}
	self, err := os.Executable()
	if err != nil {
		return status, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return status, err
	}
	defer key.Close()
	current, _, err := key.GetStringValue("CODEX_CLI_PATH")
	if errors.Is(err, registry.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.CodexCLIPath = current
	status.Installed = samePath(current, self)
	return status, nil
}
