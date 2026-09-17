package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRestoreRetainsCatalogWhenRateLimited(t *testing.T) {
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
	if !result.OK || result.Error != nil {
		t.Fatalf("restore result = %#v, want retained offline catalog", result)
	}
}

func TestMultiPairingCatalogSelectionAndDisable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	first := testCatalogCredential(server.URL)
	second := testCatalogCredential(server.URL)
	first.DaemonDeviceID = uuid.NewString()
	second.DaemonDeviceID = uuid.NewString()
	firstRaw, _ := json.Marshal(first)
	secondRaw, _ := json.Marshal(second)
	core := NewCore()
	defer core.Close()
	raw, _ := json.Marshal(catalog{Version: 2, ActiveDaemonID: first.DaemonDeviceID, Pairings: []catalogPairing{
		{DaemonID: first.DaemonDeviceID, DisplayName: "First", RelayURL: server.URL, Enabled: true, Order: 0, Credential: firstRaw},
		{DaemonID: second.DaemonDeviceID, DisplayName: "Second", RelayURL: server.URL, Enabled: true, Order: 1, Credential: secondRaw},
	}})
	result := core.Restore(string(raw))
	if !json.Valid([]byte(result)) {
		t.Fatal("restore returned invalid JSON")
	}
	if !strings.Contains(result, first.DaemonDeviceID) || !strings.Contains(result, second.DaemonDeviceID) {
		t.Fatalf("catalog missing pairing IDs: %s", result)
	}
	var envelope resultEnvelope
	_ = json.Unmarshal([]byte(core.SelectPairing(second.DaemonDeviceID)), &envelope)
	if !envelope.OK {
		t.Fatalf("selection failed: %#v", envelope.Error)
	}
	assertExportedCatalog(t, core, 2)

	_ = json.Unmarshal([]byte(core.SetPairingEnabled(second.DaemonDeviceID, false)), &envelope)
	if !envelope.OK {
		t.Fatalf("disable failed: %#v", envelope.Error)
	}
	if !strings.Contains(string(mustResult(envelope)), first.DaemonDeviceID) {
		t.Fatalf("active pairing did not fall back to first: %#v", envelope.Result)
	}
	assertExportedCatalog(t, core, 2)
}

func assertExportedCatalog(t *testing.T, core *Core, want int) {
	t.Helper()
	var stored catalog
	if err := json.Unmarshal([]byte(core.ExportCatalog()), &stored); err != nil {
		t.Fatalf("exported catalog is invalid: %v", err)
	}
	if len(stored.Pairings) != want {
		t.Fatalf("exported pairings = %d, want %d", len(stored.Pairings), want)
	}
	for _, item := range stored.Pairings {
		if len(item.Credential) == 0 {
			t.Fatalf("pairing %s lost its credential", item.DaemonID)
		}
	}
}

func testCatalogCredential(relayURL string) endpoint.CredentialSet {
	return endpoint.CredentialSet{
		RelayURL: relayURL, PairingID: uuid.NewString(), DaemonDeviceID: uuid.NewString(), MobileDeviceID: uuid.NewString(),
		Ed25519Private: make([]byte, 64), X25519Private: make([]byte, 32), X25519Public: make([]byte, 32), DaemonX25519: make([]byte, 32),
		DaemonCapability: []byte("daemon"), MobileCapability: []byte("mobile"),
	}
}

func mustResult(envelope resultEnvelope) []byte {
	raw, _ := json.Marshal(envelope.Result)
	return raw
}
