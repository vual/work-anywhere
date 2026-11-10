package mobilecore

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/mobile/core/update"
)

const updateCheckTimeout = 30 * time.Second

// updateRuntime holds the app-update state. It has its own lock so a long APK
// download never blocks pairing operations guarded by Core.operations.
type updateRuntime struct {
	mu      sync.Mutex
	release *update.AndroidRelease
	cancel  context.CancelFunc
	client  *http.Client
	trusted map[string]ed25519.PublicKey
}

type updateInfo struct {
	State  string                 `json:"state"`
	Latest *update.AndroidRelease `json:"latest,omitempty"`
}

type updateArtifact struct {
	Path              string `json:"path"`
	VersionCode       int64  `json:"versionCode"`
	PackageName       string `json:"packageName"`
	SigningCertSHA256 string `json:"signingCertSha256"`
}

type updateEvent struct {
	Type     string `json:"type"`
	State    string `json:"state"`
	Received int64  `json:"received,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Error    *fault `json:"error,omitempty"`
}

func (u *updateRuntime) dependencies() (*http.Client, map[string]ed25519.PublicKey) {
	u.mu.Lock()
	defer u.mu.Unlock()
	client, trusted := u.client, u.trusted
	if client == nil {
		client = update.NewHTTPClient()
	}
	if trusted == nil {
		trusted = update.TrustedKeys()
	}
	return client, trusted
}

// CheckUpdate fetches and verifies the signed release manifest and reports
// whether the android-apk asset is newer than currentVersionCode. It does not
// download anything.
func (c *Core) CheckUpdate(currentVersionCode int64, packageName, mirrorBase string) string {
	client, trusted := c.update.dependencies()
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	release, err := update.CheckAndroid(ctx, client, trusted, currentVersionCode, strings.TrimSpace(packageName), mirrorBase)
	if err != nil {
		code, message := update.ErrorCode(err, update.CodeManifestUnavailable)
		return failure(code, message)
	}
	c.update.mu.Lock()
	c.update.release = release
	c.update.mu.Unlock()
	if release == nil {
		return success(updateInfo{State: "upToDate"})
	}
	return success(updateInfo{State: "available", Latest: release})
}

// DownloadUpdate blocks while downloading the APK found by the last
// CheckUpdate into cacheDir/updates/<versionCode>/app.apk. Progress is sent
// to the event sink as type=update events; the result names the verified APK.
func (c *Core) DownloadUpdate(versionCode int64, cacheDir string) string {
	c.update.mu.Lock()
	release := c.update.release
	if release == nil || release.VersionCode != versionCode {
		c.update.mu.Unlock()
		return failure(update.CodeNoAsset, "check for updates before downloading")
	}
	if c.update.cancel != nil {
		c.update.mu.Unlock()
		return failure(update.CodeInProgress, "an update download is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.update.cancel = cancel
	c.update.mu.Unlock()
	defer func() {
		c.update.mu.Lock()
		c.update.cancel = nil
		c.update.mu.Unlock()
		cancel()
	}()

	client, _ := c.update.dependencies()
	c.emitUpdate(updateEvent{State: "downloading", Total: release.Size})
	path, err := update.DownloadAndroid(ctx, client, release, cacheDir, func(received, total int64) {
		c.emitUpdate(updateEvent{State: "downloading", Received: received, Total: total})
	})
	if err != nil {
		code, message := update.ErrorCode(err, update.CodeDownloadFailed)
		if code == update.CodeCancelled {
			_, part, _ := update.APKPaths(cacheDir, versionCode)
			_ = os.Remove(part)
		}
		c.emitUpdate(updateEvent{State: "error", Error: &fault{Code: code, Message: message}})
		return failure(code, message)
	}
	c.emitUpdate(updateEvent{State: "downloaded", Received: release.Size, Total: release.Size})
	return success(updateArtifact{Path: path, VersionCode: release.VersionCode, PackageName: release.PackageName, SigningCertSHA256: release.SigningCertSHA256})
}

// CancelUpdateDownload stops a running DownloadUpdate; it is a no-op otherwise.
func (c *Core) CancelUpdateDownload() {
	c.update.mu.Lock()
	cancel := c.update.cancel
	c.update.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// emitUpdate sends an app-update event straight to the sink; unlike daemon
// events it carries no daemonId or selection generation.
func (c *Core) emitUpdate(event updateEvent) {
	event.Type = "update"
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	c.mu.Lock()
	sink := c.sink
	c.mu.Unlock()
	if sink != nil {
		sink.OnEvent(string(raw))
	}
}
