package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppServerArgumentClassification(t *testing.T) {
	if !IsPlainAppServer([]string{"app-server", "--listen", "ws://127.0.0.1:1"}) {
		t.Fatal("app-server was not classified")
	}
	if !IsPlainAppServer([]string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled"}) {
		t.Fatal("app-server after global options was not classified")
	}
	if IsPlainAppServer([]string{"app-server", "generate-json-schema", "--out", "x"}) {
		t.Fatal("schema generator must pass through")
	}
	if IsPlainAppServer([]string{"-c", "features.code_mode_host=true", "app-server", "daemon"}) {
		t.Fatal("app-server daemon must pass through")
	}
	args := AppServerArgs([]string{"app-server", "--listen", "ws://127.0.0.1:1", "--enable", "x"})
	want := []string{"app-server", "--enable", "x", "--listen", "stdio://"}
	if len(args) != len(want) {
		t.Fatalf("unexpected args: %#v", args)
	}
	for index := range want {
		if args[index] != want[index] {
			t.Fatalf("unexpected args: %#v", args)
		}
	}
	args = AppServerArgs([]string{"-c", "features.code_mode_host=true", "app-server", "--listen=ws://127.0.0.1:1", "--analytics-default-enabled"})
	want = []string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled", "--listen", "stdio://"}
	if len(args) != len(want) {
		t.Fatalf("unexpected global-option args: %#v", args)
	}
	for index := range want {
		if args[index] != want[index] {
			t.Fatalf("unexpected global-option args: %#v", args)
		}
	}
}

func TestInstanceIdentityIgnoresRuntimePath(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".codex-profile")
	userData := filepath.Join(t.TempDir(), "electron")
	t.Setenv("CODEX_HOME", home)
	t.Setenv("WORK_ANYWHERE_USER_DATA_DIR", userData)
	t.Setenv("WORK_ANYWHERE_INSTANCE_NAME", "test")
	t.Setenv("WORK_ANYWHERE_CODEX_EXE", `C:\version-a\codex.exe`)
	first, err := ResolveInstance()
	if err != nil {
		t.Fatal(err)
	}
	os.Setenv("WORK_ANYWHERE_CODEX_EXE", `C:\version-b\codex.exe`)
	second, err := ResolveInstance()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("runtime path changed stable instance id: %s != %s", first.ID, second.ID)
	}
	if first.CodexHome == "" || first.UserDataDir == "" {
		t.Fatal("canonical paths were not populated")
	}
}

func TestUserCodexCacheDiscoveryPrefersNewestRevision(t *testing.T) {
	local := t.TempDir()
	oldPath := filepath.Join(local, "OpenAI", "Codex", "bin", "old", "codex.exe")
	newPath := filepath.Join(local, "OpenAI", "Codex", "bin", "new", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", local)
	candidates := scanUserCodex()
	if len(candidates) != 2 {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	if candidates[0].Path != newPath || candidates[0].Source != "user_codex_cache" {
		t.Fatalf("newest cached Codex was not preferred: %#v", candidates)
	}
}
