//go:build windows

package store

import (
	"path/filepath"
	"testing"
)

func TestEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"), filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := map[string]any{"message": "secret", "number": float64(42)}
	encrypted, err := s.Encrypt(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == `{"message":"secret","number":42}` {
		t.Fatal("value was not encrypted")
	}
	var got map[string]any
	if err = s.Decrypt(encrypted, &got); err != nil {
		t.Fatal(err)
	}
	if got["message"] != want["message"] || got["number"] != want["number"] {
		t.Fatalf("roundtrip mismatch: %#v", got)
	}
}
