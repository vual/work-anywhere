package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSensitiveFieldsAreRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.ndjson")
	logger, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Event("test", map[string]any{"instanceId": "ok", "authToken": "do-not-write", "nested": map[string]any{"password": "also-secret"}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-write") || strings.Contains(string(data), "also-secret") {
		t.Fatalf("secret leaked: %s", data)
	}
	if !strings.Contains(string(data), "instanceId") {
		t.Fatalf("safe field missing: %s", data)
	}
}
