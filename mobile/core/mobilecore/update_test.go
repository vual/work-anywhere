package mobilecore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hurf/work-anywhere/mobile/core/update"
)

type recordingSink struct {
	mu     sync.Mutex
	events []string
}

func (s *recordingSink) OnEvent(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

// updateFixture serves a manifest signed with a test key whose android-apk
// points back at the same server.
func updateFixture(t *testing.T, apk []byte, versionCode int64) (*Core, string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var manifest []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + update.MirrorPath:
			_, _ = w.Write(manifest)
		case "/download/v0.4.0/app.apk":
			_, _ = w.Write(apk)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	sum := sha256.Sum256(apk)
	payload, _ := json.Marshal(update.Payload{
		Channel: update.ChannelStable, Version: "0.4.0", Tag: "v0.4.0", PublishedAt: "2026-11-10T08:00:00Z",
		NotesURL: "https://github.com/vual/work-anywhere/releases/tag/v0.4.0",
		Assets: map[string]update.Asset{update.AndroidAssetKey: {
			Name: "app.apk", URL: update.ReleasesPrefix + "download/v0.4.0/app.apk", Size: int64(len(apk)),
			SHA256: hex.EncodeToString(sum[:]), PackageName: testPackage, VersionCode: versionCode,
			SigningCertSHA256: strings.Repeat("cd", 32),
		}},
	})
	manifest, err = update.Sign(payload, "waw-test", key)
	if err != nil {
		t.Fatal(err)
	}
	core := NewCore()
	t.Cleanup(core.Close)
	core.update.client = server.Client()
	core.update.trusted = map[string]ed25519.PublicKey{"waw-test": key.Public().(ed25519.PublicKey)}
	return core, server.URL
}

const testPackage = "io.workanywhere.work_anywhere"

func decode(t *testing.T, raw string, into any) resultEnvelope {
	t.Helper()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *fault          `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	if into != nil && envelope.OK {
		if err := json.Unmarshal(envelope.Result, into); err != nil {
			t.Fatal(err)
		}
	}
	return resultEnvelope{OK: envelope.OK, Error: envelope.Error}
}

func TestCheckAndDownloadUpdate(t *testing.T) {
	apk := []byte(strings.Repeat("apk", 1000))
	core, mirror := updateFixture(t, apk, 90)
	sink := &recordingSink{}
	core.SetEventSink(sink)

	var current struct{ State string }
	if result := decode(t, core.CheckUpdate(90, testPackage, mirror), &current); !result.OK || current.State != "upToDate" {
		t.Fatalf("same versionCode: %+v %+v", result, current)
	}
	if result := decode(t, core.DownloadUpdate(90, t.TempDir()), nil); result.OK || result.Error.Code != update.CodeNoAsset {
		t.Fatalf("download without newer release: %+v", result)
	}

	var info struct {
		State  string
		Latest struct {
			VersionName string
			VersionCode int64
			Size        int64
		}
	}
	if result := decode(t, core.CheckUpdate(89, testPackage, mirror), &info); !result.OK || info.State != "available" || info.Latest.VersionCode != 90 || info.Latest.VersionName != "0.4.0" {
		t.Fatalf("check: %+v %+v", result, info)
	}
	cache := t.TempDir()
	var artifact updateArtifact
	if result := decode(t, core.DownloadUpdate(90, cache), &artifact); !result.OK {
		t.Fatalf("download: %+v", result.Error)
	}
	if got, err := os.ReadFile(artifact.Path); err != nil || string(got) != string(apk) || artifact.PackageName != testPackage {
		t.Fatalf("artifact %+v err %v", artifact, err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	last := sink.events[len(sink.events)-1]
	if !strings.Contains(last, `"type":"update"`) || !strings.Contains(last, `"state":"downloaded"`) || strings.Contains(last, "daemonId") {
		t.Fatalf("events = %v", sink.events)
	}
}

func TestCheckUpdateRejectsUntrustedManifest(t *testing.T) {
	core, mirror := updateFixture(t, []byte("apk"), 90)
	other, _, _ := ed25519.GenerateKey(nil)
	core.update.trusted = map[string]ed25519.PublicKey{"waw-test": other}
	if result := decode(t, core.CheckUpdate(1, testPackage, mirror), nil); result.OK || result.Error.Code != update.CodeSignatureInvalid {
		t.Fatalf("result = %+v", result)
	}
	if result := decode(t, core.CheckUpdate(1, testPackage, "http://insecure.example.com"), nil); result.OK || result.Error.Code != update.CodeManifestInvalid {
		t.Fatalf("http mirror result = %+v", result)
	}
}
