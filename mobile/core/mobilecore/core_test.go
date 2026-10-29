package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestRemovePairingForgetsOnlySelectedComputer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	first := testCatalogCredential(server.URL)
	second := testCatalogCredential(server.URL)
	firstRaw, _ := json.Marshal(first)
	secondRaw, _ := json.Marshal(second)
	core := NewCore()
	defer core.Close()
	raw, _ := json.Marshal(catalog{Version: 2, ActiveDaemonID: first.DaemonDeviceID, Pairings: []catalogPairing{
		{DaemonID: first.DaemonDeviceID, RelayURL: server.URL, Enabled: true, Order: 0, Credential: firstRaw},
		{DaemonID: second.DaemonDeviceID, RelayURL: server.URL, Enabled: true, Order: 1, Credential: secondRaw},
	}})
	var envelope resultEnvelope
	if err := json.Unmarshal([]byte(core.Restore(string(raw))), &envelope); err != nil || !envelope.OK {
		t.Fatalf("restore failed: %#v %v", envelope, err)
	}
	if err := json.Unmarshal([]byte(core.RemovePairing(first.DaemonDeviceID)), &envelope); err != nil || !envelope.OK {
		t.Fatalf("remove failed: %#v %v", envelope, err)
	}
	status := core.statusValue()
	if status.ActiveDaemonID != second.DaemonDeviceID || len(status.Pairings) != 1 || status.Pairings[0].DaemonID != second.DaemonDeviceID {
		t.Fatalf("unexpected fallback status: %#v", status)
	}
	assertExportedCatalog(t, core, 1)
	if err := json.Unmarshal([]byte(core.RemovePairing(first.DaemonDeviceID)), &envelope); err != nil || envelope.OK || envelope.Error.Code != "pairing_not_found" {
		t.Fatalf("missing pairing removal = %#v, err=%v", envelope, err)
	}
	if err := json.Unmarshal([]byte(core.RemovePairing(second.DaemonDeviceID)), &envelope); err != nil || !envelope.OK {
		t.Fatalf("last removal failed: %#v %v", envelope, err)
	}
	status = core.statusValue()
	if status.ActiveDaemonID != "" || len(status.Pairings) != 0 {
		t.Fatalf("last pairing was retained: %#v", status)
	}
	assertExportedCatalog(t, core, 0)
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

func TestCatalogEnableReorderAndTargetGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	a, b := testCatalogCredential(server.URL), testCatalogCredential(server.URL)
	ar, _ := json.Marshal(a)
	br, _ := json.Marshal(b)
	stored := catalog{Version: 2, ActiveDaemonID: "missing", Pairings: []catalogPairing{
		{DaemonID: a.DaemonDeviceID, RelayURL: server.URL, Enabled: false, Order: 0, Credential: ar},
		{DaemonID: b.DaemonDeviceID, RelayURL: server.URL, Enabled: false, Order: 1, Credential: br},
	}}
	raw, _ := json.Marshal(stored)
	c := NewCore()
	defer c.Close()
	c.Restore(string(raw))
	before := c.statusValue()
	if before.ActiveDaemonID != "" {
		t.Fatal("disabled catalog selected")
	}
	c.SetPairingEnabled(b.DaemonDeviceID, true)
	after := c.statusValue()
	if after.ActiveDaemonID != b.DaemonDeviceID || after.Generation <= before.Generation {
		t.Fatal("enable did not select/increment")
	}
	c.SetPairingEnabled(a.DaemonDeviceID, true)
	if c.statusValue().ActiveDaemonID != b.DaemonDeviceID {
		t.Fatal("enable stole selection")
	}
	target, _ := json.Marshal(map[string]any{"_wawTarget": map[string]any{"daemonId": b.DaemonDeviceID, "generation": before.Generation}})
	var result resultEnvelope
	json.Unmarshal([]byte(c.Call("turn.start", string(target), 10)), &result)
	if result.Error == nil || result.Error.Code != "stale_selection" {
		t.Fatalf("old target dispatched: %+v", result)
	}
	snapshot := c.ExportCatalog()
	for _, order := range []string{`[]`, `["unknown"]`, `null`, `{}`, `["` + a.DaemonDeviceID + `","` + a.DaemonDeviceID + `"]`} {
		json.Unmarshal([]byte(c.ReorderPairings(order)), &result)
		if result.OK || c.ExportCatalog() != snapshot {
			t.Fatalf("invalid order mutated catalog: %s", order)
		}
	}
	order, _ := json.Marshal([]string{b.DaemonDeviceID, a.DaemonDeviceID})
	c.ReorderPairings(string(order))
	restored := NewCore()
	defer restored.Close()
	restored.Restore(c.ExportCatalog())
	if restored.statusValue().Pairings[0].DaemonID != b.DaemonDeviceID || restored.statusValue().ActiveDaemonID != b.DaemonDeviceID {
		t.Fatal("sort/selection did not survive restore")
	}
	stored.ActiveDaemonID = "missing"
	stored.Pairings[0].Enabled = true
	stored.Pairings[1].Enabled = true
	stored.Pairings[0].Order = 1
	stored.Pairings[1].Order = 0
	raw, _ = json.Marshal(stored)
	restored.Restore(string(raw))
	if restored.statusValue().ActiveDaemonID != b.DaemonDeviceID {
		t.Fatal("legacy fallback ignored sort order")
	}
}

func TestActiveProbeReusesClientReconnectGate(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { attempts.Add(1); w.WriteHeader(429) }))
	defer server.Close()
	credential := testCatalogCredential(server.URL)
	raw, _ := json.Marshal(credential)
	c := NewCore()
	defer c.Close()
	c.Restore(string(raw))
	before := attempts.Load()
	if before == 0 {
		t.Fatal("initial connection not attempted")
	}
	c.ProbePairing(credential.DaemonDeviceID)
	if attempts.Load() != before {
		t.Fatal("active probe created another client/login instead of sharing reconnect cooldown")
	}
}
