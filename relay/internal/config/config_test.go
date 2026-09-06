package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/waw")
	t.Setenv("RELAY_PUBLIC_URL", "https://relay.example.test")
	t.Setenv("RELAY_DATA_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("ENROLLMENT_MODE", "invite")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || len(cfg.DataKey) != 32 || cfg.MaxConnections != 10_000 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsInsecurePublicURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/waw")
	t.Setenv("RELAY_PUBLIC_URL", "http://relay.example.test")
	t.Setenv("RELAY_DATA_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https validation error, got %v", err)
	}
}
