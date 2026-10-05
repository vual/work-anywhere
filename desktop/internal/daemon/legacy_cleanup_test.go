package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacyDatabaseDeletesOnlyLegacyStateFiles(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"work-anywhere.db", "work-anywhere.db-wal", "work-anywhere.db-shm", "master.dpapi"} {
		if err := os.WriteFile(filepath.Join(state, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if failures := removeLegacyDatabase(root); len(failures) != 0 {
		t.Fatalf("cleanup failures: %v", failures)
	}
	for _, name := range []string{"work-anywhere.db", "work-anywhere.db-wal", "work-anywhere.db-shm", "master.dpapi"} {
		if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy file %s remains: %v", name, err)
		}
	}
}
