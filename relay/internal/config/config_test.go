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

func TestLoadAllowsHTTPIPAndDomain(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/waw")
	t.Setenv("RELAY_PUBLIC_URL", "http://relay.example.test")
	t.Setenv("RELAY_DATA_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := Load(); err != nil {
		t.Fatalf("expected HTTP domain to be accepted, got %v", err)
	}

	t.Setenv("RELAY_PUBLIC_URL", "http://172.20.31.206:18080")
	if _, err := Load(); err != nil {
		t.Fatalf("expected HTTP IP to be accepted, got %v", err)
	}
}

func TestLoadRejectsUnsupportedPublicURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/waw")
	t.Setenv("RELAY_DATA_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	for _, value := range []string{"ftp://relay.example.test", "http://relay.example.test/path", "http://relay.example.test?x=1"} {
		t.Setenv("RELAY_PUBLIC_URL", value)
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "HTTP or HTTPS origin") {
			t.Errorf("expected origin validation error for %q, got %v", value, err)
		}
	}
}
