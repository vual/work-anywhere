//go:build windows

package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestMaterializeLegacyAttachmentClearsDatabaseContent(t *testing.T) {
	directory := t.TempDir()
	state, err := Open(filepath.Join(directory, "state.db"), filepath.Join(directory, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	want := []byte("legacy-image")
	encrypted, err := state.Encrypt(want)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.db.Exec(`INSERT INTO attachments(id,instance_id,conversation_id,mime_type,file_name,size,content_encrypted,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		"legacy-1", "agent-1", "conversation-1", "image/jpeg", "photo.jpg", len(want), encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "attachments", "conversation-1", "legacy-legacy-1", "photo-legacy-1.jpg")
	attachment, err := state.MaterializeLegacyAttachment("legacy-1", path, "project-1", "legacy-legacy-1")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, want) {
		t.Fatalf("materialized content mismatch: %q, %v", content, err)
	}
	if attachment.Path != path || attachment.ProjectID != "project-1" || attachment.MessageID != "legacy-legacy-1" || attachment.Size != int64(len(want)) {
		t.Fatalf("unexpected migrated metadata: %#v", attachment)
	}
	var remaining []byte
	if err = state.db.QueryRow(`SELECT content_encrypted FROM attachments WHERE id=?`, "legacy-1").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != nil {
		t.Fatal("legacy content was not cleared from the database")
	}
}
