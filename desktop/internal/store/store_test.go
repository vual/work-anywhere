//go:build windows

package store

import (
	"bytes"
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

func TestSettingsAreEncryptedAndRoundTrip(t *testing.T) {
	directory := t.TempDir()
	state, err := Open(filepath.Join(directory, "state.db"), filepath.Join(directory, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	value := map[string]string{"privateKey": "sensitive-value"}
	if err = state.PutSetting("remote.state", value); err != nil {
		t.Fatal(err)
	}
	var restored map[string]string
	found, err := state.GetSetting("remote.state", &restored)
	if err != nil || !found || restored["privateKey"] != value["privateKey"] {
		t.Fatalf("setting roundtrip failed: found=%v value=%v err=%v", found, restored, err)
	}
	var raw []byte
	if err = state.db.QueryRow(`SELECT value_encrypted FROM settings WHERE key='remote.state'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sensitive-value")) {
		t.Fatal("setting was stored in plaintext")
	}
}
