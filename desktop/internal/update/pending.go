package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	pendingFile        = "pending.json"
	installerFileName  = "work-anywhere-setup.exe"
	installLogFileName = "install.log"
	pendingMaxAge      = 7 * 24 * time.Hour
)

// Pending records an installer hand-off so the next daemon can report the
// result.
type Pending struct {
	FromVersion string    `json:"fromVersion"`
	ToVersion   string    `json:"toVersion"`
	StartedAt   time.Time `json:"startedAt"`
	LogPath     string    `json:"logPath"`
}

// PendingOutcome is the result of inspecting pending.json at startup.
type PendingOutcome int

const (
	PendingNone PendingOutcome = iota
	PendingSucceeded
	PendingFailed
	PendingStale
)

func writePending(dir string, pending Pending) error {
	raw, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, pendingFile)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// resolvePending inspects and removes pending.json, deleting the downloaded
// installer after a success.
func resolvePending(dir, currentVersion string, now time.Time) (PendingOutcome, Pending) {
	path := filepath.Join(dir, pendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return PendingNone, Pending{}
	}
	var pending Pending
	if json.Unmarshal(raw, &pending) != nil {
		_ = os.Remove(path)
		return PendingStale, Pending{}
	}
	_ = os.Remove(path)
	switch {
	case now.Sub(pending.StartedAt) > pendingMaxAge:
		return PendingStale, pending
	case currentVersion == pending.ToVersion:
		_ = os.RemoveAll(filepath.Join(dir, pending.ToVersion))
		return PendingSucceeded, pending
	default:
		return PendingFailed, pending
	}
}

// InstallerArgs builds the silent installer command line.
func InstallerArgs(logPath string, autostart, codex bool, waitPID int) []string {
	task := func(name string, enabled bool) string {
		if enabled {
			return name
		}
		return "!" + name
	}
	return []string{
		"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/NOCANCEL",
		"/WAWUPDATE=1",
		"/WAWPID=" + strconv.Itoa(waitPID),
		"/LOG=" + logPath,
		"/TASKS=" + task("autostart", autostart) + "," + task("codex", codex),
	}
}

// cleanOtherVersions removes download directories except keep.
func cleanOtherVersions(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}
