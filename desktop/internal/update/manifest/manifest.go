// Package manifest implements the signed update manifest defined in
// docs/self-update/manifest.md: envelope parsing, Ed25519 verification,
// payload validation, version comparison and mirror rewriting.
package manifest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	Format           = "waw-update-manifest"
	FormatVersion    = 1
	SignatureDomain  = "waw-update-manifest-v1\n"
	DefaultURL       = "https://github.com/vual/work-anywhere/releases/latest/download/update-manifest.json"
	ReleasesPrefix   = "https://github.com/vual/work-anywhere/releases/"
	MirrorPath       = "latest/download/update-manifest.json"
	MaxManifestBytes = 64 << 10
	MaxAssetBytes    = 512 << 20
	ChannelStable    = "stable"

	AssetWindowsSetup    = "windows-amd64-setup"
	AssetWindowsPortable = "windows-amd64-portable"
	AssetAndroidAPK      = "android-apk"
)

// Stable error codes shared with Mobile.
const (
	CodeUnavailable      = "update_manifest_unavailable"
	CodeInvalid          = "update_manifest_invalid"
	CodeSignatureInvalid = "update_signature_invalid"
	CodeHashMismatch     = "update_hash_mismatch"
)

// Error carries a stable update error code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Envelope is the outer, signed manifest document.
type Envelope struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`
	KeyID         string `json:"keyId"`
	Payload       string `json:"payload"`
	Signature     string `json:"signature"`
}

// Asset describes one platform artifact.
type Asset struct {
	Name              string `json:"name"`
	URL               string `json:"url"`
	Size              int64  `json:"size"`
	SHA256            string `json:"sha256"`
	PackageName       string `json:"packageName,omitempty"`
	VersionCode       int64  `json:"versionCode,omitempty"`
	SigningCertSHA256 string `json:"signingCertSha256,omitempty"`
}

// Payload is the signed inner document.
type Payload struct {
	Channel     string           `json:"channel"`
	Version     string           `json:"version"`
	Tag         string           `json:"tag"`
	PublishedAt string           `json:"publishedAt"`
	NotesURL    string           `json:"notesUrl"`
	Assets      map[string]Asset `json:"assets"`
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Sign encodes payload into a signed envelope.
func Sign(payload []byte, keyID string, key ed25519.PrivateKey) ([]byte, error) {
	if keyID == "" {
		return nil, fmt.Errorf("key id is required")
	}
	signature := ed25519.Sign(key, signedMessage(payload))
	return json.MarshalIndent(Envelope{
		Format:        Format,
		FormatVersion: FormatVersion,
		KeyID:         keyID,
		Payload:       base64.RawURLEncoding.EncodeToString(payload),
		Signature:     base64.RawURLEncoding.EncodeToString(signature),
	}, "", "  ")
}

func signedMessage(payload []byte) []byte {
	message := make([]byte, 0, len(SignatureDomain)+len(payload))
	message = append(message, SignatureDomain...)
	return append(message, payload...)
}

// Verify checks the envelope signature against trusted keys and returns the
// validated payload. Every failure is an *Error with a stable code.
func Verify(raw []byte, trusted map[string]ed25519.PublicKey) (*Payload, error) {
	if len(raw) > MaxManifestBytes {
		return nil, errorf(CodeUnavailable, "manifest exceeds %d bytes", MaxManifestBytes)
	}
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, errorf(CodeInvalid, "manifest envelope is not valid JSON")
	}
	if envelope.Format != Format || envelope.FormatVersion != FormatVersion {
		return nil, errorf(CodeInvalid, "unsupported manifest format %q version %d", envelope.Format, envelope.FormatVersion)
	}
	key, ok := trusted[envelope.KeyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errorf(CodeSignatureInvalid, "manifest key %q is not trusted", envelope.KeyID)
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, errorf(CodeSignatureInvalid, "manifest payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errorf(CodeSignatureInvalid, "manifest signature encoding is invalid")
	}
	if !ed25519.Verify(key, signedMessage(payload), signature) {
		return nil, errorf(CodeSignatureInvalid, "manifest signature does not verify")
	}
	var parsed Payload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&parsed); err != nil {
		return nil, errorf(CodeInvalid, "manifest payload is not valid JSON")
	}
	if err := parsed.Validate(); err != nil {
		return nil, err
	}
	return &parsed, nil
}

// Validate applies the field rules of the manifest protocol.
func (p *Payload) Validate() error {
	if p.Channel != ChannelStable {
		return errorf(CodeInvalid, "unsupported channel %q", p.Channel)
	}
	version, err := ParseVersion(p.Version)
	if err != nil || version.Prerelease != "" || version.Build != "" {
		return errorf(CodeInvalid, "invalid release version %q", p.Version)
	}
	if p.Tag != "v"+p.Version {
		return errorf(CodeInvalid, "tag %q does not match version %q", p.Tag, p.Version)
	}
	if _, err := time.Parse(time.RFC3339, p.PublishedAt); err != nil {
		return errorf(CodeInvalid, "invalid publishedAt")
	}
	if !isHTTPS(p.NotesURL) {
		return errorf(CodeInvalid, "notesUrl must be https")
	}
	for key, asset := range p.Assets {
		switch key {
		case AssetWindowsSetup, AssetWindowsPortable, AssetAndroidAPK:
		default:
			continue
		}
		if err := asset.validate(key); err != nil {
			return err
		}
	}
	return nil
}

func (a Asset) validate(key string) error {
	if a.Name == "" || !isHTTPS(a.URL) {
		return errorf(CodeInvalid, "asset %s requires a name and an https url", key)
	}
	if a.Size <= 0 || a.Size > MaxAssetBytes {
		return errorf(CodeInvalid, "asset %s size is out of range", key)
	}
	if !hex64.MatchString(a.SHA256) {
		return errorf(CodeInvalid, "asset %s sha256 is invalid", key)
	}
	if key == AssetAndroidAPK {
		if a.PackageName == "" || a.VersionCode <= 0 || !hex64.MatchString(a.SigningCertSHA256) {
			return errorf(CodeInvalid, "asset %s requires packageName, versionCode and signingCertSha256", key)
		}
	}
	return nil
}

func isHTTPS(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// ManifestURL returns the manifest location for an optional mirror.
func ManifestURL(mirrorBase string) (string, error) {
	if mirrorBase == "" {
		return DefaultURL, nil
	}
	base, err := NormalizeMirror(mirrorBase)
	if err != nil {
		return "", err
	}
	return base + "/" + MirrorPath, nil
}

// AssetURL rewrites a GitHub release URL onto the mirror, if configured.
func AssetURL(raw, mirrorBase string) (string, error) {
	if mirrorBase == "" {
		return raw, nil
	}
	base, err := NormalizeMirror(mirrorBase)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(raw, ReleasesPrefix) {
		return raw, nil
	}
	return base + "/" + strings.TrimPrefix(raw, ReleasesPrefix), nil
}

// NormalizeMirror validates a mirror prefix and strips trailing slashes.
func NormalizeMirror(mirrorBase string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(mirrorBase), "/")
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errorf(CodeInvalid, "mirror must be an https URL without query or fragment")
	}
	return trimmed, nil
}
