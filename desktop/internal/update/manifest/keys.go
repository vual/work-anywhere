package manifest

import (
	"crypto/ed25519"
	"encoding/base64"
)

// trustedKeyStrings lists the release signing keys (keyId -> base64url
// Ed25519 public key). Mobile core/update/keys.go must contain the same set;
// see docs/self-update/manifest.md#signing-keys for rotation rules.
var trustedKeyStrings = map[string]string{
	"waw-release-2026": "4p54fNrFhwKX-fJsAH4K6-Drg-IH5svO9myPJUlGjQs",
}

// TrustedKeys returns the compiled-in release public keys.
func TrustedKeys() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey, len(trustedKeyStrings))
	for id, encoded := range trustedKeyStrings {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err == nil && len(raw) == ed25519.PublicKeySize {
			keys[id] = ed25519.PublicKey(raw)
		}
	}
	return keys
}
