package remote

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestEnrollmentSharedVector(t *testing.T) {
	raw, err := os.ReadFile("../../../relay/docs/daemon-enrollment/vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Origin        string `json:"origin"`
		ExpiresAtMS   int64  `json:"expiresAtMs"`
		DisplayName   string `json:"displayName"`
		TranscriptHex string `json:"transcriptHex"`
	}
	if err = json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	result := enrollmentSignatureInput(vector.Origin, bytes.Repeat([]byte{5}, 16), bytes.Repeat([]byte{2}, 32), vector.ExpiresAtMS, privateKey[32:], bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), vector.DisplayName)
	if hex.EncodeToString(result) != vector.TranscriptHex {
		t.Fatalf("transcript differs: %x", result)
	}
	signature := ed25519.Sign(privateKey, result)
	if !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), result, signature) {
		t.Fatal("signature verification failed")
	}
}
