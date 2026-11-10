package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update/manifest"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *memoryStore) GetSetting(key string, target any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, target)
}

func (s *memoryStore) PutSetting(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(value)
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	s.values[key] = raw
	return err
}

type fakePlatform struct {
	mu       sync.Mutex
	kind     string
	launched []string
	path     string
}

func (p *fakePlatform) InstallKind() string          { return p.kind }
func (p *fakePlatform) InstallerTasks() (bool, bool) { return true, false }
func (p *fakePlatform) Launch(path string, args []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.path, p.launched = path, args
	return nil
}

type fixture struct {
	server     *httptest.Server
	installer  []byte
	rangeSeen  chan string
	launched   chan bool
	service    *Service
	platform   *fakePlatform
	dir        string
	servedHash string
}

func newFixture(t *testing.T, current string, corrupt bool) *fixture {
	t.Helper()
	f := &fixture{installer: bytes.Repeat([]byte("work-anywhere-setup"), 20000), rangeSeen: make(chan string, 4), launched: make(chan bool, 1)}
	hash := sha256.Sum256(f.installer)
	f.servedHash = hex.EncodeToString(hash[:])
	key := manifest.TestVectorKey()
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/update-manifest.json":
			payload, _ := json.Marshal(manifest.Payload{
				Channel: "stable", Version: "0.3.0", Tag: "v0.3.0", PublishedAt: "2026-11-10T08:00:00Z",
				NotesURL: "https://github.com/vual/work-anywhere/releases/tag/v0.3.0",
				Assets: map[string]manifest.Asset{manifest.AssetWindowsSetup: {
					Name: "work-anywhere-setup-0.3.0.exe", URL: manifest.ReleasesPrefix + "download/v0.3.0/work-anywhere-setup-0.3.0.exe",
					Size: int64(len(f.installer)), SHA256: f.servedHash,
				}},
			})
			envelope, _ := manifest.Sign(payload, manifest.TestVectorKeyID, key)
			_, _ = w.Write(envelope)
		case "/download/v0.3.0/work-anywhere-setup-0.3.0.exe":
			if value := r.Header.Get("Range"); value != "" {
				f.rangeSeen <- value
			}
			body := f.installer
			if corrupt {
				body = bytes.ToUpper(body)
			}
			http.ServeContent(w, r, "setup.exe", time.Time{}, bytes.NewReader(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	f.dir = t.TempDir()
	f.platform = &fakePlatform{kind: InstallKindInstalled}
	store := &memoryStore{}
	_ = store.PutSetting(preferencesKey, preferences{MirrorBase: f.server.URL})
	f.service = New(Options{
		CurrentVersion: current,
		Dir:            f.dir,
		Store:          store,
		Platform:       f.platform,
		Client:         f.server.Client(),
		TrustedKeys:    map[string]ed25519.PublicKey{manifest.TestVectorKeyID: key.Public().(ed25519.PublicKey)},
		Launched:       func(interrupt bool) { f.launched <- interrupt },
	})
	return f
}

func waitState(t *testing.T, service *Service, want string) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := service.Status()
		if status.State == want {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state = %s, want %s", service.Status().State, want)
	return Status{}
}

func TestCheckApplyLaunchesInstaller(t *testing.T) {
	f := newFixture(t, "0.2.0", false)
	status, err := f.service.Check(t.Context())
	if err != nil || status.State != StateAvailable || status.Latest.Version != "0.3.0" || !status.CanApply {
		t.Fatalf("check = %+v, %v", status, err)
	}
	if _, err := f.service.Apply("0.3.0", true); err != nil {
		t.Fatal(err)
	}
	select {
	case interrupt := <-f.launched:
		if !interrupt {
			t.Fatal("interrupt flag not forwarded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("installer was not launched")
	}
	waitState(t, f.service, StateApplying)
	if filepath.Base(f.platform.path) != installerFileName {
		t.Fatalf("launched %s", f.platform.path)
	}
	joined := strings.Join(f.platform.launched, " ")
	for _, want := range []string{"/VERYSILENT", "/WAWUPDATE=1", "/TASKS=autostart,!codex"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %s", joined, want)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, pendingFile)); err != nil {
		t.Fatal("pending.json not written")
	}
}

func TestDownloadResumesPartialFile(t *testing.T) {
	f := newFixture(t, "0.2.0", false)
	if _, err := f.service.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "0.3.0")
	_ = os.MkdirAll(dir, 0o700)
	half := len(f.installer) / 2
	if err := os.WriteFile(filepath.Join(dir, installerFileName+".part"), f.installer[:half], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Apply("0.3.0", false); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-f.rangeSeen:
		if value != "bytes="+strconv.Itoa(half)+"-" {
			t.Fatalf("range = %s", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no range request")
	}
	<-f.launched
}

func TestHashMismatchDoesNotLaunch(t *testing.T) {
	f := newFixture(t, "0.2.0", true)
	if _, err := f.service.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Apply("0.3.0", false); err != nil {
		t.Fatal(err)
	}
	status := waitState(t, f.service, StateAvailable)
	if status.Error == nil || status.Error.Code != CodeHashMismatch {
		t.Fatalf("status = %+v", status)
	}
	if f.platform.path != "" {
		t.Fatal("installer launched after hash mismatch")
	}
}

func TestUpToDateAndPortable(t *testing.T) {
	f := newFixture(t, "0.3.0", false)
	status, err := f.service.Check(t.Context())
	if err != nil || status.State != StateUpToDate || status.Latest != nil {
		t.Fatalf("status = %+v, %v", status, err)
	}

	portable := newFixture(t, "0.2.0", false)
	portable.platform.kind = InstallKindPortable
	if _, err := portable.service.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = portable.service.Apply("0.3.0", false)
	if code, _ := ErrorCode(err, ""); code != CodeUnsupportedInstall {
		t.Fatalf("error = %v", err)
	}
}

func TestDevelopmentBuildShowsLatestWithoutApply(t *testing.T) {
	f := newFixture(t, "dev", false)
	status, err := f.service.Check(t.Context())
	if err != nil || status.Latest == nil || status.CanApply {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestPreflightErrorIsReturned(t *testing.T) {
	f := newFixture(t, "0.2.0", false)
	f.service.options.Preflight = func(confirm bool) error {
		if !confirm {
			return NewError(CodeActiveTurns, "1 turn is active")
		}
		return nil
	}
	if _, err := f.service.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Apply("0.3.0", false)
	if code, _ := ErrorCode(err, ""); code != CodeActiveTurns {
		t.Fatalf("error = %v", err)
	}
}

func TestSkipAndNotify(t *testing.T) {
	f := newFixture(t, "0.2.0", false)
	if _, err := f.service.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if version, ok := f.service.ShouldNotify(); !ok || version != "0.3.0" {
		t.Fatal("first notification expected")
	}
	if _, ok := f.service.ShouldNotify(); ok {
		t.Fatal("version notified twice")
	}
	if status, err := f.service.Skip("0.3.0"); err != nil || status.SkippedVersion != "0.3.0" {
		t.Fatalf("skip = %+v %v", status, err)
	}
}

func TestSettingsRejectHTTPMirror(t *testing.T) {
	f := newFixture(t, "0.2.0", false)
	mirror := "http://mirror.example.com"
	if _, err := f.service.UpdateSettings(nil, &mirror); err == nil {
		t.Fatal("http mirror accepted")
	}
	off := false
	status, err := f.service.UpdateSettings(&off, nil)
	if err != nil || status.AutoCheck {
		t.Fatalf("status = %+v %v", status, err)
	}
}

func TestResolvePending(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		current string
		started time.Time
		want    PendingOutcome
	}{
		{"succeeded", "0.3.0", now.Add(-time.Minute), PendingSucceeded},
		{"failed", "0.2.0", now.Add(-time.Minute), PendingFailed},
		{"stale", "0.2.0", now.Add(-8 * 24 * time.Hour), PendingStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.MkdirAll(filepath.Join(dir, "0.3.0"), 0o700)
			if err := writePending(dir, Pending{FromVersion: "0.2.0", ToVersion: "0.3.0", StartedAt: c.started}); err != nil {
				t.Fatal(err)
			}
			if outcome, _ := resolvePending(dir, c.current, now); outcome != c.want {
				t.Fatalf("outcome = %v", outcome)
			}
			if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
				t.Fatal("pending.json not removed")
			}
			_, err := os.Stat(filepath.Join(dir, "0.3.0"))
			if (c.want == PendingSucceeded) != os.IsNotExist(err) {
				t.Fatalf("download dir state wrong: %v", err)
			}
		})
	}
	if outcome, _ := resolvePending(t.TempDir(), "0.2.0", now); outcome != PendingNone {
		t.Fatal("expected no pending")
	}
}

func TestPendingFailureSurfacesInstallFailed(t *testing.T) {
	dir := t.TempDir()
	_ = writePending(dir, Pending{FromVersion: "0.2.0", ToVersion: "0.3.0", StartedAt: time.Now(), LogPath: "x.log"})
	service := New(Options{CurrentVersion: "0.2.0", Dir: dir, Platform: &fakePlatform{kind: InstallKindInstalled}})
	status := service.Status()
	if status.State != StateError || status.Error.Code != CodeInstallFailed || status.Error.LogPath != "x.log" {
		t.Fatalf("status = %+v", status)
	}
}

func TestInstallerArgsTasks(t *testing.T) {
	for _, c := range []struct {
		autostart, codex bool
		want             string
	}{
		{true, true, "/TASKS=autostart,codex"},
		{true, false, "/TASKS=autostart,!codex"},
		{false, true, "/TASKS=!autostart,codex"},
		{false, false, "/TASKS=!autostart,!codex"},
	} {
		args := InstallerArgs(`C:\x\install.log`, c.autostart, c.codex, 42)
		want := []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/NOCANCEL", "/WAWUPDATE=1", "/WAWPID=42", `/LOG=C:\x\install.log`, c.want}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args = %v", args)
		}
	}
}

func TestInstallKindFor(t *testing.T) {
	local := t.TempDir()
	installDir := filepath.Join(local, "Programs", "WorkAnywhere")
	_ = os.MkdirAll(installDir, 0o700)
	exe := filepath.Join(installDir, "work-anywhere.exe")
	if installKindFor(exe, local) != InstallKindPortable {
		t.Fatal("missing uninstaller must be portable")
	}
	_ = os.WriteFile(filepath.Join(installDir, "unins000.exe"), nil, 0o600)
	if installKindFor(exe, local) != InstallKindInstalled {
		t.Fatal("expected installed")
	}
	if installKindFor(filepath.Join(local, "work-anywhere.exe"), local) != InstallKindPortable {
		t.Fatal("other directory must be portable")
	}
}
