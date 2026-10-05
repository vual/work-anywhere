//go:build windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreProtectsAndAtomicallyReloadsSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "private-message-must-not-appear"
	if err = store.PutSetting("remote.state.v1", map[string]string{"private": secret}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("protected setting was written as plaintext")
	}

	const writes = 16
	var wait sync.WaitGroup
	for index := 0; index < writes; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if putErr := store.PutSetting("setting-"+string(rune('a'+index)), index); putErr != nil {
				t.Errorf("put setting: %v", putErr)
			}
		}(index)
	}
	wait.Wait()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	found, err := reopened.GetSetting("remote.state.v1", &value)
	if err != nil || !found || value["private"] != secret {
		t.Fatalf("reloaded setting: found=%v value=%v err=%v", found, value, err)
	}
}

func TestStoreRejectsCorruptConfigWithoutOverwritingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const corrupt = "{broken"
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt config was accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != corrupt {
		t.Fatalf("corrupt config was modified: %q, %v", data, err)
	}
}

func TestThreadSettingsAreBounded(t *testing.T) {
	state := fileState{Version: fileVersion, Settings: make(map[string]storedValue)}
	for index := 0; index < maxThreadSettingCount+5; index++ {
		state.Settings[threadSettingPrefix+string(rune(index))] = storedValue{UpdatedAt: time.Unix(int64(index), 0)}
	}
	state.Settings["remote.state.v1"] = storedValue{}
	trimThreadSettings(&state)
	if len(state.Settings) != maxThreadSettingCount+1 {
		t.Fatalf("settings count = %d", len(state.Settings))
	}
}
