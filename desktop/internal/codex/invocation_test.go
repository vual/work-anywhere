package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppServerArgumentClassification(t *testing.T) {
	if !IsPlainAppServer([]string{"app-server", "--listen", "ws://127.0.0.1:1"}) {
		t.Fatal("app-server was not classified")
	}
	if IsPlainAppServer([]string{"app-server", "generate-json-schema", "--out", "x"}) {
		t.Fatal("schema generator must pass through")
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
