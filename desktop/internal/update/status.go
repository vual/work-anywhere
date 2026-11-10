// Package update implements Desktop self-update (docs/self-update/design.md):
// manifest checks, verified downloads and the hand-off to the silent
// installer.
package update

import (
	"errors"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update/manifest"
)

const (
	StateIdle        = "idle"
	StateChecking    = "checking"
	StateUpToDate    = "upToDate"
	StateAvailable   = "available"
	StateDownloading = "downloading"
	StateDownloaded  = "downloaded"
	StateApplying    = "applying"
	StateError       = "error"

	InstallKindInstalled = "installed"
	InstallKindPortable  = "portable"
)

// Stable Desktop error codes in addition to the manifest codes.
const (
	CodeManifestUnavailable = manifest.CodeUnavailable
	CodeManifestInvalid     = manifest.CodeInvalid
	CodeSignatureInvalid    = manifest.CodeSignatureInvalid
	CodeHashMismatch        = manifest.CodeHashMismatch
	CodeDownloadFailed      = "update_download_failed"
	CodeNoAsset             = "update_no_asset"
	CodeUnsupportedInstall  = "update_unsupported_install"
	CodeActiveTurns         = "update_active_turns"
	CodeBlockedByBridge     = "update_blocked_by_bridge"
	CodeInProgress          = "update_in_progress"
	CodeInstallFailed       = "update_install_failed"
)

// Error is a coded update error.
type Error = manifest.Error

// NewError builds a coded update error.
func NewError(code, message string) *Error { return &Error{Code: code, Message: message} }

// ErrorCode extracts a stable code, defaulting to fallback.
func ErrorCode(err error, fallback string) (string, string) {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code, coded.Message
	}
	return fallback, err.Error()
}

type Latest struct {
	Version  string `json:"version"`
	NotesURL string `json:"notesUrl"`
	Size     int64  `json:"size,omitempty"`
}

type Progress struct {
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

type StatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	LogPath string `json:"logPath,omitempty"`
}

// Status is the UpdateStatus document returned by system.update.*.
type Status struct {
	State          string       `json:"state"`
	CurrentVersion string       `json:"currentVersion"`
	Latest         *Latest      `json:"latest,omitempty"`
	Progress       *Progress    `json:"progress,omitempty"`
	LastCheckedAt  *time.Time   `json:"lastCheckedAt,omitempty"`
	Error          *StatusError `json:"error,omitempty"`
	InstallKind    string       `json:"installKind"`
	AutoCheck      bool         `json:"autoCheck"`
	SkippedVersion string       `json:"skippedVersion"`
	MirrorBase     string       `json:"mirrorBase"`
	// UpdatedFrom is set once after a successful upgrade was detected.
	UpdatedFrom string `json:"updatedFrom,omitempty"`
	// CanApply is false for portable copies and development builds.
	CanApply bool `json:"canApply"`
}
