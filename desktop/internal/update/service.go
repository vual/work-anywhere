package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update/manifest"
)

const (
	preferencesKey     = "update.preferences"
	firstCheckDelay    = 30 * time.Second
	checkInterval      = 24 * time.Hour
	checkJitter        = time.Hour
	assetKey           = manifest.AssetWindowsSetup
	maxMirrorBaseBytes = 512
)

// Store persists update preferences (config.Store satisfies it).
type Store interface {
	GetSetting(key string, target any) (bool, error)
	PutSetting(key string, value any) error
}

// Logger records structured events.
type Logger interface {
	Event(name string, fields map[string]any)
}

// Platform isolates OS-specific behaviour.
type Platform interface {
	InstallKind() string
	// InstallerTasks reports the current autostart and Codex integration
	// choices to preserve.
	InstallerTasks() (autostart, codex bool)
	Launch(path string, args []string) error
}

// Options configures a Service.
type Options struct {
	CurrentVersion string
	Dir            string
	Store          Store
	Log            Logger
	Platform       Platform
	Client         *http.Client
	TrustedKeys    map[string]ed25519.PublicKey
	// Preflight runs right before the installer is launched; it returns a
	// coded error such as update_active_turns.
	Preflight func(confirmInterrupt bool) error
	// Launched is called after the installer started; the daemon must exit.
	Launched func(interrupt bool)
	// OnChange is called with every status change.
	OnChange func(Status)
	Now      func() time.Time
}

type preferences struct {
	AutoCheck       *bool  `json:"autoCheck,omitempty"`
	MirrorBase      string `json:"mirrorBase,omitempty"`
	SkippedVersion  string `json:"skippedVersion,omitempty"`
	NotifiedVersion string `json:"notifiedVersion,omitempty"`
}

// Service owns the update state machine.
type Service struct {
	options Options

	mu           sync.Mutex
	prefs        preferences
	state        string
	latest       *manifest.Payload
	progress     *Progress
	lastChecked  *time.Time
	statusErr    *StatusError
	updatedFrom  string
	downloaded   string
	cancel       context.CancelFunc
	applyPending *bool
	checking     bool
	ctx          context.Context
	versionOK    bool
	lastProgress time.Time
}

// New creates a Service and resolves any pending installer hand-off.
func New(options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Client == nil {
		options.Client = NewHTTPClient()
	}
	if options.TrustedKeys == nil {
		options.TrustedKeys = manifest.TrustedKeys()
	}
	service := &Service{options: options, state: StateIdle, ctx: context.Background()}
	_, err := manifest.ParseVersion(strings.TrimPrefix(options.CurrentVersion, "v"))
	service.versionOK = err == nil
	if options.Store != nil {
		if _, err := options.Store.GetSetting(preferencesKey, &service.prefs); err != nil {
			service.logEvent("update.preferences_failed", map[string]any{"error": err.Error()})
		}
	}
	service.resolvePending()
	return service
}

func (s *Service) logEvent(name string, fields map[string]any) {
	if s.options.Log != nil {
		s.options.Log.Event(name, fields)
	}
}

func (s *Service) resolvePending() {
	if s.options.Dir == "" {
		return
	}
	outcome, pending := resolvePending(s.options.Dir, s.options.CurrentVersion, s.options.Now())
	switch outcome {
	case PendingSucceeded:
		s.updatedFrom = pending.FromVersion
		s.logEvent("update.install_succeeded", map[string]any{"from": pending.FromVersion, "to": pending.ToVersion})
	case PendingFailed:
		s.state = StateError
		s.statusErr = &StatusError{Code: CodeInstallFailed, Message: "the installer did not finish upgrading to " + pending.ToVersion, LogPath: pending.LogPath}
		s.logEvent("update.install_failed", map[string]any{"from": pending.FromVersion, "to": pending.ToVersion, "log": pending.LogPath})
	}
}

func (s *Service) autoCheck() bool {
	return s.prefs.AutoCheck == nil || *s.prefs.AutoCheck
}

// Run performs automatic checks until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	timer := time.NewTimer(firstCheckDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Cancel()
			return
		case <-timer.C:
		}
		s.mu.Lock()
		enabled := s.autoCheck() && s.versionOK
		s.mu.Unlock()
		if enabled {
			if _, err := s.Check(ctx); err != nil {
				code, _ := ErrorCode(err, CodeManifestUnavailable)
				s.logEvent("update.auto_check_failed", map[string]any{"code": code})
			}
		}
		jitter := time.Duration(rand.Int64N(int64(2*checkJitter))) - checkJitter
		timer.Reset(checkInterval + jitter)
	}
}

// Status returns the current UpdateStatus.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Service) statusLocked() Status {
	status := Status{
		State:          s.state,
		CurrentVersion: s.options.CurrentVersion,
		Progress:       s.progress,
		LastCheckedAt:  s.lastChecked,
		Error:          s.statusErr,
		InstallKind:    s.installKind(),
		AutoCheck:      s.autoCheck(),
		SkippedVersion: s.prefs.SkippedVersion,
		MirrorBase:     s.prefs.MirrorBase,
		UpdatedFrom:    s.updatedFrom,
	}
	status.CanApply = s.versionOK && status.InstallKind == InstallKindInstalled
	if s.latest != nil {
		latest := &Latest{Version: s.latest.Version, NotesURL: s.latest.NotesURL}
		if asset, ok := s.latest.Assets[assetKey]; ok {
			latest.Size = asset.Size
		}
		status.Latest = latest
	}
	return status
}

func (s *Service) installKind() string {
	if s.options.Platform == nil {
		return InstallKindPortable
	}
	return s.options.Platform.InstallKind()
}

func (s *Service) changedLocked() {
	if s.options.OnChange == nil {
		return
	}
	status := s.statusLocked()
	go s.options.OnChange(status)
}

func (s *Service) setErrorLocked(state string, err error, fallback string) {
	code, message := ErrorCode(err, fallback)
	s.state = state
	s.statusErr = &StatusError{Code: code, Message: message}
}

// Check fetches the manifest now.
func (s *Service) Check(ctx context.Context) (Status, error) {
	s.mu.Lock()
	if s.busyLocked() || s.checking {
		status := s.statusLocked()
		s.mu.Unlock()
		return status, NewError(CodeInProgress, "an update is already in progress")
	}
	s.checking = true
	previous := s.state
	s.state = StateChecking
	s.statusErr = nil
	mirror := s.prefs.MirrorBase
	s.changedLocked()
	s.mu.Unlock()

	payload, err := s.fetch(ctx, mirror)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.checking = false
	now := s.options.Now().UTC()
	s.lastChecked = &now
	if err != nil {
		state := StateError
		if s.latest != nil {
			state = previous
		}
		s.setErrorLocked(state, err, CodeManifestUnavailable)
		s.changedLocked()
		return s.statusLocked(), err
	}
	newer, compareErr := manifest.IsNewer(payload.Version, s.options.CurrentVersion)
	if compareErr == nil && !newer {
		s.latest = nil
		s.downloaded = ""
		s.state = StateUpToDate
		s.changedLocked()
		return s.statusLocked(), nil
	}
	// Development builds (unparsable current version) only show the latest
	// version; CanApply stays false.
	if s.latest == nil || s.latest.Version != payload.Version {
		s.downloaded = ""
	}
	s.latest = payload
	s.state = StateAvailable
	if s.downloaded != "" {
		s.state = StateDownloaded
	}
	if s.options.Dir != "" {
		cleanOtherVersions(s.options.Dir, payload.Version)
	}
	s.changedLocked()
	return s.statusLocked(), nil
}

func (s *Service) fetch(ctx context.Context, mirror string) (*manifest.Payload, error) {
	url, err := manifest.ManifestURL(mirror)
	if err != nil {
		return nil, err
	}
	return FetchManifest(ctx, s.options.Client, url, s.options.TrustedKeys)
}

func (s *Service) busyLocked() bool {
	return s.state == StateDownloading || s.state == StateApplying
}

// Apply downloads (if needed) and installs version. When the download is
// still running, Apply returns the downloading status and installs once the
// verified file is ready.
func (s *Service) Apply(version string, confirmInterrupt bool) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busyLocked() {
		return s.statusLocked(), NewError(CodeInProgress, "an update is already in progress")
	}
	if s.latest == nil || s.latest.Version != version || s.state == StateUpToDate {
		return s.statusLocked(), NewError(CodeManifestUnavailable, "version "+version+" is not the available update; check for updates first")
	}
	if !s.versionOK || s.installKind() != InstallKindInstalled {
		return s.statusLocked(), NewError(CodeUnsupportedInstall, "only installed copies with a release version can update automatically")
	}
	asset, ok := s.latest.Assets[assetKey]
	if !ok {
		return s.statusLocked(), NewError(CodeNoAsset, "the release has no Windows installer")
	}
	if s.options.Preflight != nil {
		if err := s.options.Preflight(confirmInterrupt); err != nil {
			return s.statusLocked(), err
		}
	}
	if s.downloaded != "" {
		if err := s.launchLocked(version, asset, confirmInterrupt); err != nil {
			return s.statusLocked(), err
		}
		return s.statusLocked(), nil
	}
	s.startDownloadLocked(version, asset, &confirmInterrupt)
	return s.statusLocked(), nil
}

func (s *Service) startDownloadLocked(version string, asset manifest.Asset, apply *bool) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.applyPending = apply
	s.state = StateDownloading
	s.statusErr = nil
	s.progress = &Progress{Total: asset.Size}
	s.changedLocked()
	mirror := s.prefs.MirrorBase
	go s.download(ctx, version, asset, mirror)
}

func (s *Service) download(ctx context.Context, version string, asset manifest.Asset, mirror string) {
	started := s.options.Now()
	dir := filepath.Join(s.options.Dir, version)
	final := filepath.Join(dir, installerFileName)
	url, err := manifest.AssetURL(asset.URL, mirror)
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err == nil {
		err = Download(ctx, s.options.Client, url, final+".part", final, asset.Size, asset.SHA256, func(received, total int64) {
			s.mu.Lock()
			if s.state == StateDownloading {
				s.progress = &Progress{Received: received, Total: total}
				s.changedLocked()
			}
			s.mu.Unlock()
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	apply := s.applyPending
	s.applyPending = nil
	s.progress = nil
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.state = StateAvailable
			s.statusErr = nil
		} else {
			s.setErrorLocked(StateAvailable, err, CodeDownloadFailed)
			code, _ := ErrorCode(err, CodeDownloadFailed)
			s.logEvent("update.download_failed", map[string]any{"version": version, "code": code})
		}
		s.changedLocked()
		return
	}
	s.downloaded = final
	s.state = StateDownloaded
	s.logEvent("update.downloaded", map[string]any{"version": version, "bytes": asset.Size, "ms": s.options.Now().Sub(started).Milliseconds()})
	if apply != nil && s.latest != nil && s.latest.Version == version {
		if s.options.Preflight != nil {
			if err := s.options.Preflight(*apply); err != nil {
				s.setErrorLocked(StateDownloaded, err, CodeInstallFailed)
				s.changedLocked()
				return
			}
		}
		if err := s.launchLocked(version, asset, *apply); err != nil {
			return
		}
		return
	}
	s.changedLocked()
}

func (s *Service) launchLocked(version string, asset manifest.Asset, interrupt bool) error {
	ok, err := fileMatches(s.downloaded, asset.Size, asset.SHA256)
	if err != nil || !ok {
		_ = os.Remove(s.downloaded)
		s.downloaded = ""
		failure := NewError(CodeHashMismatch, "the downloaded installer changed on disk")
		s.setErrorLocked(StateAvailable, failure, CodeHashMismatch)
		s.changedLocked()
		return failure
	}
	dir := filepath.Dir(s.downloaded)
	logPath := filepath.Join(dir, installLogFileName)
	autostart, codex := s.options.Platform.InstallerTasks()
	if err := writePending(s.options.Dir, Pending{FromVersion: s.options.CurrentVersion, ToVersion: version, StartedAt: s.options.Now().UTC(), LogPath: logPath}); err != nil {
		failure := NewError(CodeInstallFailed, "record pending update: "+err.Error())
		s.setErrorLocked(StateDownloaded, failure, CodeInstallFailed)
		s.changedLocked()
		return failure
	}
	if err := s.options.Platform.Launch(s.downloaded, InstallerArgs(logPath, autostart, codex, os.Getpid())); err != nil {
		_ = os.Remove(filepath.Join(s.options.Dir, pendingFile))
		failure := NewError(CodeInstallFailed, "start installer: "+err.Error())
		s.setErrorLocked(StateDownloaded, failure, CodeInstallFailed)
		s.changedLocked()
		return failure
	}
	s.state = StateApplying
	s.statusErr = nil
	s.logEvent("update.installer_started", map[string]any{"from": s.options.CurrentVersion, "to": version, "autostart": autostart, "codex": codex})
	s.changedLocked()
	if s.options.Launched != nil {
		go s.options.Launched(interrupt)
	}
	return nil
}

// Cancel stops a running download.
func (s *Service) Cancel() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return s.statusLocked()
}

// Skip records version as skipped for notifications.
func (s *Service) Skip(version string) (Status, error) {
	if _, err := manifest.ParseVersion(version); err != nil {
		return s.Status(), NewError(CodeManifestInvalid, "invalid version")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs.SkippedVersion = version
	if err := s.saveLocked(); err != nil {
		return s.statusLocked(), err
	}
	s.changedLocked()
	return s.statusLocked(), nil
}

// UpdateSettings changes autoCheck and/or mirrorBase.
func (s *Service) UpdateSettings(autoCheck *bool, mirrorBase *string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.prefs
	if autoCheck != nil {
		value := *autoCheck
		next.AutoCheck = &value
	}
	if mirrorBase != nil {
		if len(*mirrorBase) > maxMirrorBaseBytes {
			return s.statusLocked(), NewError(CodeManifestInvalid, "mirror address is too long")
		}
		normalized, err := manifest.NormalizeMirror(*mirrorBase)
		if err != nil {
			return s.statusLocked(), err
		}
		next.MirrorBase = normalized
	}
	previous := s.prefs
	s.prefs = next
	if err := s.saveLocked(); err != nil {
		s.prefs = previous
		return s.statusLocked(), err
	}
	s.changedLocked()
	return s.statusLocked(), nil
}

// ShouldNotify reports whether the available version has not been announced
// or skipped yet, and records it as announced.
func (s *Service) ShouldNotify() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil || (s.state != StateAvailable && s.state != StateDownloaded) {
		return "", false
	}
	version := s.latest.Version
	if version == s.prefs.SkippedVersion || version == s.prefs.NotifiedVersion {
		return version, false
	}
	s.prefs.NotifiedVersion = version
	_ = s.saveLocked()
	return version, true
}

func (s *Service) saveLocked() error {
	if s.options.Store == nil {
		return nil
	}
	return s.options.Store.PutSetting(preferencesKey, s.prefs)
}
