package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppServerArgumentClassification(t *testing.T) {
	tests := []struct {
		args    []string
		managed bool
	}{
		{[]string{"app-server"}, true},
		{[]string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled", "-c", "features.prefer_mxc=true"}, true},
		{[]string{"--config=x=true", "-cy=true", "app-server", "--listen=stdio://"}, true},
		{[]string{"app-server", "--stdio", "--strict-config"}, true},
		{[]string{"app-server", "--code-mode-host", "wss://example.test", "--cloud-skills-snapshot=x"}, true},
		{[]string{"app-server", "--standalone-ws"}, true},
		{[]string{"-c", "app-server"}, false},
		{[]string{"exec", "app-server"}, false},
		{[]string{"--unknown", "app-server"}, false},
		{[]string{"--help", "app-server"}, false},
		{[]string{"app-server", "--future-option"}, false},
		{[]string{"app-server", "--listen", "ws://127.0.0.1:1"}, false},
		{[]string{"app-server", "--listen=off"}, false},
		{[]string{"app-server", "--listen=unix://"}, false},
		{[]string{"app-server", "--listen"}, false},
		{[]string{"app-server", "--config"}, false},
		{[]string{"app-server", "--strict-config=true"}, false},
	}
	for _, mode := range []string{"--print-startup-config", "--startup-sign-out", "--help", "-h", "--version", "proxy", "daemon", "help", "generate-ts", "generate-json-schema"} {
		tests = append(tests, struct {
			args    []string
			managed bool
		}{[]string{"-c", "x=true", "app-server", "--analytics-default-enabled", mode}, false})
	}
	tests = append(tests, struct {
		args    []string
		managed bool
	}{[]string{"app-server", "--print-startup-config", "--startup-client-info", `{"name":"codex_desktop","version":"26.1023.6621"}`, "--startup-sign-out"}, false})
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			if got := IsPlainAppServer(test.args); got != test.managed {
				t.Fatalf("managed=%v want %v", got, test.managed)
			}
			if !reflect.DeepEqual(AppServerArgs(test.args), test.args) {
				t.Fatal("arguments were rewritten")
			}
		})
	}
}

func TestChildEnvironment(t *testing.T) {
	base := []string{"Path=original", "CODEX_CLI_PATH=bridge", "codex_cli_path=duplicate", "CODEX_APP_RUNTIME_CLI_PATH=bridge", "CODEX_HOME=profile", "CODEX_WINDOWS_REGISTERED_CORE=identity"}
	got := ChildEnvironment(base, "real")
	want := []string{"Path=original", "CODEX_HOME=profile", "CODEX_WINDOWS_REGISTERED_CORE=identity", "CODEX_APP_RUNTIME_CLI_PATH=real", "CODEX_CLI_PATH=real"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment: %#v", got)
	}
	if !reflect.DeepEqual(ChildEnvironment(nil, "real"), []string{"CODEX_CLI_PATH=real"}) {
		t.Fatal("invented runtime override")
	}
	if base[1] != "CODEX_CLI_PATH=bridge" {
		t.Fatal("mutated parent environment")
	}
}

func TestDiagnosticTransportPreservesOptionValues(t *testing.T) {
	args := []string{"-c", "app-server", "app-server", "-c", "--listen", "--cloud-skills-snapshot", "--stdio", "--listen=stdio://", "--analytics-default-enabled"}
	want := []string{"-c", "app-server", "app-server", "-c", "--listen", "--cloud-skills-snapshot", "--stdio", "--analytics-default-enabled", "--listen", "ws://127.0.0.1:1234"}
	if got := DiagnosticWebSocketArgs(args, "ws://127.0.0.1:1234"); !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic args: %q", got)
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
