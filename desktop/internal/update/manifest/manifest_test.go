package manifest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const vectorsPath = "../../../docs/self-update/vectors.json"

func loadVectors(t *testing.T) Vectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(vectorsPath))
	if err != nil {
		t.Fatal(err)
	}
	var vectors Vectors
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}

func TestVectorsFileIsCurrent(t *testing.T) {
	want, err := BuildVectors()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.FromSlash(vectorsPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("vectors.json is stale; run `go run ./internal/tools/updatemanifest vectors -out docs/self-update/vectors.json`")
	}
}

func TestManifestVectors(t *testing.T) {
	vectors := loadVectors(t)
	trusted := map[string]ed25519.PublicKey{}
	for id, encoded := range vectors.TrustedKeys {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		trusted[id] = raw
	}
	for _, vector := range vectors.Manifests {
		t.Run(vector.Name, func(t *testing.T) {
			payload, err := Verify([]byte(vector.Raw), trusted)
			if vector.ExpectError != "" {
				var updateErr *Error
				if !errors.As(err, &updateErr) || updateErr.Code != vector.ExpectError {
					t.Fatalf("error = %v, want %s", err, vector.ExpectError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if payload.Version != vector.ExpectVersion {
				t.Fatalf("version = %s", payload.Version)
			}
			if code := payload.Assets[AssetAndroidAPK].VersionCode; code != vector.ExpectAPKVersionCode {
				t.Fatalf("versionCode = %d", code)
			}
		})
	}
}

func TestVersionVectors(t *testing.T) {
	for _, vector := range loadVectors(t).Versions {
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
	for _, vector := range loadVectors(t).Mirrors {
		manifestURL, err := ManifestURL(vector.MirrorBase)
		if vector.Invalid {
			if err == nil {
				t.Errorf("%q: expected invalid mirror", vector.MirrorBase)
			}
			continue
		}
		if err != nil || manifestURL != vector.ManifestURL {
			t.Errorf("%q: manifest url %q err %v", vector.MirrorBase, manifestURL, err)
		}
		assetURL, err := AssetURL(vector.AssetIn, vector.MirrorBase)
		if err != nil || assetURL != vector.AssetOut {
			t.Errorf("%q: asset url %q err %v", vector.MirrorBase, assetURL, err)
		}
	}
}

func TestTrustedKeysExcludeTestKey(t *testing.T) {
	if _, ok := TrustedKeys()[TestVectorKeyID]; ok {
		t.Fatal("test vector key must not be trusted")
	}
	for id, encoded := range trustedKeyStrings {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			t.Fatalf("trusted key %s is malformed", id)
		}
	}
}

func TestVerifyRejectsOversizedManifest(t *testing.T) {
	_, err := Verify(make([]byte, MaxManifestBytes+1), nil)
	var updateErr *Error
	if !errors.As(err, &updateErr) || updateErr.Code != CodeUnavailable {
		t.Fatalf("error = %v", err)
	}
}
