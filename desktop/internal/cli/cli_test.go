package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintJSONKeepsPairingURISeparators(t *testing.T) {
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	err = printJSON(map[string]string{"uri": "waw://pair?v=1&payload=test"})
	_ = write.Close()
	os.Stdout = original
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(read)
	_ = read.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !bytes.Contains(data, []byte("waw://pair?v=1&payload=test")) {
		t.Fatalf("pairing URI was not emitted as usable JSON: %s", data)
	}
	if strings.Contains(string(data), `\u0026`) {
		t.Fatalf("pairing URI still contains JSON escaped ampersand: %s", data)
	}
}

func TestRemoteRegistrationAndExplicitResetCommands(t *testing.T) {
	method, params, _, err := parse([]string{"remote", "register", "My", "PC"})
	if err != nil || method != "remote.register" {
		t.Fatalf("register: %s %v", method, err)
	}
	var registration struct {
		DisplayName string `json:"displayName"`
	}
	if err = json.Unmarshal(params, &registration); err != nil || registration.DisplayName != "My PC" {
		t.Fatalf("name: %s %v", params, err)
	}
	method, params, _, err = parse([]string{"remote", "configure", "https://relay.example", "--reset-identity", "off"})
	if err != nil || method != "remote.configure" {
		t.Fatal("configure command", err)
	}
	var configuration struct {
		Enabled       bool `json:"enabled"`
		ResetIdentity bool `json:"resetIdentity"`
	}
	if err = json.Unmarshal(params, &configuration); err != nil || configuration.Enabled || !configuration.ResetIdentity {
		t.Fatal("reset option lost", err)
	}
	if _, _, _, err = parse([]string{"remote", "configure", "https://relay.example", "--typo"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
