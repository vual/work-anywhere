package traffic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrafficDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	recorder := New(dir)
	recorder.Record("instance", "desktop_to_server", []byte(`{"secret":true}`))
	if _, err := os.Stat(filepath.Join(dir, "instance", "traffic.ndjson")); !os.IsNotExist(err) {
		t.Fatal("traffic was written while disabled")
	}
	recorder.Set("instance", true)
	recorder.Record("instance", "desktop_to_server", []byte(`{"secret":true}`))
	data, err := os.ReadFile(filepath.Join(dir, "instance", "traffic.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "desktop_to_server") {
		t.Fatalf("unexpected traffic record: %s", data)
	}
}
