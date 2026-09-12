package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	endpoint "github.com/vual/work-anywhere/relay/pkg/endpoint/client"
)

func TestCallRejectsInvalidJSONWithoutNetwork(t *testing.T) {
	core := NewCore()
	defer core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Call("agent.list", "{", 10)), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "invalid_params" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	core := NewCore()
	core.Close()
	core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Status()), &result); err != nil || !result.OK {
		t.Fatalf("unexpected status: %#v err=%v", result, err)
	}
}

func TestRestoreRejectsIncompleteCredentials(t *testing.T) {
	core := NewCore()
	defer core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Restore(`{"pairingId":"invalid"}`)), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "restore_failed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRestorePreservesRateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	credential := endpoint.CredentialSet{
		RelayURL:         server.URL,
		PairingID:        uuid.NewString(),
		DaemonDeviceID:   uuid.NewString(),
		MobileDeviceID:   uuid.NewString(),
		Ed25519Private:   make([]byte, 64),
		X25519Private:    make([]byte, 32),
		X25519Public:     make([]byte, 32),
		DaemonX25519:     make([]byte, 32),
		DaemonCapability: []byte("daemon-capability"),
		MobileCapability: []byte("mobile-capability"),
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	core := NewCore()
	defer core.Close()
	var result resultEnvelope
	if err = json.Unmarshal([]byte(core.Restore(string(raw))), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "rate_limited" {
		t.Fatalf("restore result = %#v, want rate_limited", result)
	}
}
