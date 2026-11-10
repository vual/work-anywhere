package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/hurf/work-anywhere/desktop/internal/platform/autostart"
	"github.com/hurf/work-anywhere/desktop/internal/platform/installation"
	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
)

// SystemPlatform is the production Platform.
type SystemPlatform struct{}

// InstallKind reports "installed" only for the per-user Inno Setup location
// with its uninstaller next to the EXE.
func (SystemPlatform) InstallKind() string {
	if runtime.GOOS != "windows" {
		return InstallKindPortable
	}
	self, err := os.Executable()
	if err != nil {
		return InstallKindPortable
	}
	return installKindFor(self, os.Getenv("LOCALAPPDATA"))
}

func installKindFor(executable, localAppData string) string {
	if localAppData == "" {
		return InstallKindPortable
	}
	installDir := filepath.Join(localAppData, "Programs", "WorkAnywhere")
	if paths.CanonicalPath(filepath.Dir(executable)) != paths.CanonicalPath(installDir) {
		return InstallKindPortable
	}
	if _, err := os.Stat(filepath.Join(installDir, "unins000.exe")); err != nil {
		return InstallKindPortable
	}
	return InstallKindInstalled
}

// InstallerTasks preserves the autostart entry only when it points at this
// EXE, and Codex integration only when CODEX_CLI_PATH already does.
func (SystemPlatform) InstallerTasks() (bool, bool) {
	start, _ := autostart.Get()
	bridge, _ := installation.Status()
	return start.Enabled && start.Current, bridge.Installed
}

// Launch starts the installer outside the daemon's Job objects so it
// survives the daemon exit.
func (SystemPlatform) Launch(path string, args []string) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	platformprocess.ConfigureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
