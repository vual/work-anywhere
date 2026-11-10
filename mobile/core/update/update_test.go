package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Shared with Desktop, the protocol owner.
const (
	vectorsPath     = "../../../desktop/docs/self-update/vectors.json"
	desktopKeysPath = "../../../desktop/internal/update/manifest/keys.go"
	testPackage     = "io.workanywhere.work_anywhere"
)

type vectors struct {
	TrustedKeys map[string]string `json:"trustedKeys"`
	Manifests   []struct {
		Name                 string `json:"name"`
		Raw                  string `json:"raw"`
		ExpectError          string `json:"expectError"`
		ExpectVersion        string `json:"expectVersion"`
		ExpectAPKVersionCode int64  `json:"expectApkVersionCode"`
	} `json:"manifests"`
	Versions []struct {
		Candidate string `json:"candidate"`
		Current   string `json:"current"`
		Newer     bool   `json:"newer"`
		Invalid   bool   `json:"invalid"`
	} `json:"versions"`
	Mirrors []struct {
		MirrorBase  string `json:"mirrorBase"`
		ManifestURL string `json:"manifestUrl"`
		AssetIn     string `json:"assetIn"`
		AssetOut    string `json:"assetOut"`
		Invalid     bool   `json:"invalid"`
	} `json:"mirrors"`
}

func loadVectors(t *testing.T) (vectors, map[string]ed25519.PublicKey) {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(vectorsPath))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	trusted := map[string]ed25519.PublicKey{}
	for id, encoded := range v.TrustedKeys {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		trusted[id] = key
	}
	return v, trusted
}

func TestManifestVectors(t *testing.T) {
	v, trusted := loadVectors(t)
	for _, vector := range v.Manifests {
		t.Run(vector.Name, func(t *testing.T) {
			payload, err := Verify([]byte(vector.Raw), trusted)
			if vector.ExpectError != "" {
				if err == nil {
					t.Fatalf("expected %s", vector.ExpectError)
				}
				if code, _ := ErrorCode(err, ""); code != vector.ExpectError {
					t.Fatalf("error = %v, want %s", err, vector.ExpectError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if payload.Version != vector.ExpectVersion || payload.Assets[AndroidAssetKey].VersionCode != vector.ExpectAPKVersionCode {
				t.Fatalf("payload = %+v", payload)
			}
		})
	}
}

func TestVersionVectors(t *testing.T) {
	v, _ := loadVectors(t)
	for _, vector := range v.Versions {
		newer, err := IsNewer(vector.Candidate, vector.Current)
		if vector.Invalid {
			if err == nil {
				t.Errorf("%s vs %s: expected parse error", vector.Candidate, vector.Current)
			}
			continue
		}
		if err != nil || newer != vector.Newer {
			t.Errorf("%s vs %s: newer=%v err=%v", vector.Candidate, vector.Current, newer, err)
		}
	}
}

func TestMirrorVectors(t *testing.T) {
	v, _ := loadVectors(t)
	for _, vector := range v.Mirrors {
		manifestURL, err := ManifestURL(vector.MirrorBase)
		if vector.Invalid {
			if err == nil {
				t.Errorf("%q: expected invalid mirror", vector.MirrorBase)
			}
			continue
		}
		assetURL, assetErr := AssetURL(vector.AssetIn, vector.MirrorBase)
		if err != nil || manifestURL != vector.ManifestURL || assetErr != nil || assetURL != vector.AssetOut {
			t.Errorf("%q: manifest %q asset %q err %v %v", vector.MirrorBase, manifestURL, assetURL, err, assetErr)
		}
	}
}

var keyBlock = regexp.MustCompile(`(?s)var trustedKeyStrings = map\[string\]string\{.*?\}`)

func TestTrustedKeysMatchDesktop(t *testing.T) {
	desktop, err := os.ReadFile(filepath.FromSlash(desktopKeysPath))
	if err != nil {
		t.Fatal(err)
	}
	mobile, err := os.ReadFile("keys.go")
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(raw []byte) string {
		return strings.Join(strings.Fields(keyBlock.FindString(string(raw))), " ")
	}
	if normalize(desktop) == "" || normalize(desktop) != normalize(mobile) {
		t.Fatal("trusted keys differ from desktop/internal/update/manifest/keys.go")
	}
	if _, ok := TrustedKeys()["waw-test-vectors"]; ok {
		t.Fatal("test vector key must not be trusted")
	}
}

func validPayload(t *testing.T) *Payload {
	t.Helper()
	v, trusted := loadVectors(t)
	payload, err := Verify([]byte(v.Manifests[0].Raw), trusted)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSelectAndroid(t *testing.T) {
	payload := validPayload(t)
	release, err := SelectAndroid(payload, 86, testPackage, "")
	if err != nil || release == nil || release.VersionCode != 87 || release.VersionName != "0.3.0" {
		t.Fatalf("release = %+v err = %v", release, err)
	}
	if release, err := SelectAndroid(payload, 87, testPackage, ""); err != nil || release != nil {
		t.Fatalf("same versionCode should be current: %+v %v", release, err)
	}
	if _, err := SelectAndroid(payload, 1, "other.package", ""); err == nil {
		t.Fatal("expected package mismatch")
	}
	mirrored, err := SelectAndroid(payload, 1, testPackage, "https://mirror.example.com")
	if err != nil || !strings.HasPrefix(mirrored.URL, "https://mirror.example.com/download/") {
		t.Fatalf("mirrored = %+v err = %v", mirrored, err)
	}
	delete(payload.Assets, AndroidAssetKey)
	if code, _ := ErrorCode(func() error { _, err := SelectAndroid(payload, 1, testPackage, ""); return err }(), ""); code != CodeNoAsset {
		t.Fatalf("missing asset code = %s", code)
	}
}

func apkServer(t *testing.T, body []byte, ranges *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ranges != nil {
			*ranges = append(*ranges, r.Header.Get("Range"))
		}
		http.ServeContent(w, r, "app.apk", time.Time{}, strings.NewReader(string(body)))
	}))
	t.Cleanup(server.Close)
	return server
}

func releaseFor(server *httptest.Server, body []byte, versionCode int64) *AndroidRelease {
	sum := sha256.Sum256(body)
	return &AndroidRelease{VersionCode: versionCode, Size: int64(len(body)), URL: server.URL + "/app.apk", SHA256: hex.EncodeToString(sum[:])}
}

func TestDownloadAndroidResumesAndPrunes(t *testing.T) {
	body := []byte(strings.Repeat("apk-bytes-", 4096))
	var ranges []string
	server := apkServer(t, body, &ranges)
	cache := t.TempDir()
	stale, _, _ := APKPaths(cache, 8)
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, part, final := APKPaths(cache, 9)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, body[:1000], 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := DownloadAndroid(context.Background(), server.Client(), releaseFor(server, body, 9), cache, nil)
	if err != nil || path != final {
		t.Fatalf("path = %s err = %v", path, err)
	}
	if got, _ := os.ReadFile(final); string(got) != string(body) {
		t.Fatal("downloaded body differs")
	}
	if len(ranges) != 1 || ranges[0] != "bytes=1000-" {
		t.Fatalf("ranges = %v", ranges)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale version directory was not pruned")
	}
}

func TestDownloadAndroidHashMismatch(t *testing.T) {
	server := apkServer(t, []byte("real apk"), nil)
	_, err := DownloadAndroid(context.Background(), server.Client(), releaseFor(server, []byte("other!!!"), 3), t.TempDir(), nil)
	if code, _ := ErrorCode(err, ""); code != CodeHashMismatch {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadAndroidCancelled(t *testing.T) {
	body := []byte(strings.Repeat("x", 1<<16))
	server := apkServer(t, body, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DownloadAndroid(ctx, server.Client(), releaseFor(server, body, 4), t.TempDir(), nil)
	if code, _ := ErrorCode(err, ""); code != CodeCancelled {
		t.Fatalf("error = %v", err)
	}
}

func TestCheckAndroidFetchesManifest(t *testing.T) {
	v, trusted := loadVectors(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+MirrorPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(v.Manifests[0].Raw))
	}))
	defer server.Close()
	release, err := CheckAndroid(context.Background(), server.Client(), trusted, 10, testPackage, server.URL)
	if err != nil || release == nil || release.VersionCode != 87 {
		t.Fatalf("release = %+v err = %v", release, err)
	}
	if _, err := CheckAndroid(context.Background(), server.Client(), map[string]ed25519.PublicKey{}, 10, testPackage, server.URL); err == nil {
		t.Fatal("untrusted key accepted")
	}
}
