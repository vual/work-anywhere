package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func newEnrollmentService(t *testing.T, settings settingsStore) *Service {
	t.Helper()
	events := make(chan protocol.Event)
	service, err := New(settings, func(request protocol.ControlRequest) protocol.ControlResponse { return protocol.ControlResponse{} }, func() (<-chan protocol.Event, func()) { return events, func() {} }, discardLog{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func enrollmentMock(t *testing.T, complete func(http.ResponseWriter, *http.Request), captured *map[string]string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/enrollment/policy":
			_ = json.NewEncoder(writer).Encode(map[string]string{"protocol": "daemon-enrollment-v1", "mode": "self_service", "relayOrigin": server.URL})
		case "/v1/enrollment/challenges":
			if err := json.NewDecoder(request.Body).Decode(captured); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"requestId": "05050505-0505-0505-0505-050505050505", "challenge": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), "expiresAtMs": int64(1800000000000)})
		case "/v1/enrollment/complete":
			complete(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRegistrationSignsPersistedIdentityAndRecovers(t *testing.T) {
	settings := &memorySettings{values: make(map[string][]byte)}
	service := newEnrollmentService(t, settings)
	var input map[string]string
	deviceID := uuid.NewString()
	var server *httptest.Server
	server = enrollmentMock(t, func(writer http.ResponseWriter, request *http.Request) {
		var complete map[string]string
		_ = json.NewDecoder(request.Body).Decode(&complete)
		edPublic, _ := base64.StdEncoding.DecodeString(input["ed25519PublicKey"])
		noisePublic, _ := base64.StdEncoding.DecodeString(input["x25519PublicKey"])
		capability, _ := base64.StdEncoding.DecodeString(input["capabilityHash"])
		signature, _ := base64.StdEncoding.DecodeString(complete["signature"])
		requestID := uuid.MustParse(complete["requestId"])
		if !ed25519.Verify(edPublic, enrollmentSignatureInput(server.URL, requestID[:], bytes.Repeat([]byte{2}, 32), 1800000000000, edPublic, noisePublic, capability, input["displayName"]), signature) {
			t.Error("bad signature")
		}
		var persisted persistedState
		_, _ = settings.GetSetting(remoteStateKey, &persisted)
		if !bytes.Equal(persisted.Ed25519Public, edPublic) {
			t.Error("private identity not saved before registration")
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"deviceId": deviceID, "created": true})
	}, &input)
	if err := service.Configure(server.URL, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Register(context.Background(), "Desk", ""); err != nil {
		t.Fatal(err)
	}
	first := service.snapshotState()
	service.mu.Lock()
	service.state.DeviceID = ""
	service.mu.Unlock()
	if err := service.Register(context.Background(), "Desk", ""); err != nil {
		t.Fatal(err)
	}
	if state := service.snapshotState(); state.DeviceID != first.DeviceID || !bytes.Equal(state.Ed25519Private, first.Ed25519Private) {
		t.Fatal("recovery changed identity")
	}
	loaded := newEnrollmentService(t, settings)
	if loaded.Status().DeviceID != deviceID {
		t.Fatal("restart lost identity")
	}
}

func TestConfigureResetAndStaleRegistration(t *testing.T) {
	settings := &memorySettings{values: make(map[string][]byte)}
	service := newEnrollmentService(t, settings)
	var input map[string]string
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := enrollmentMock(t, func(writer http.ResponseWriter, request *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_ = json.NewEncoder(writer).Encode(map[string]any{"deviceId": uuid.NewString()})
	}, &input)
	if err := service.Configure(server.URL, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Register(context.Background(), "Desk", "") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not start")
	}
	if err := service.Configure("https://other.example", true); ErrorCode(err) != "relay_change_requires_reset" {
		t.Fatalf("missing reset gate: %v", err)
	}
	if err := service.ConfigureWithReset("https://other.example", true, true); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale request accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registration not canceled")
	}
	if state := service.snapshotState(); state.RelayURL != "https://other.example" || state.DeviceID != "" || len(state.Ed25519Private) != 0 {
		t.Fatal("old identity leaked into new config")
	}
}

func TestRegistrationErrorsAndPartialIdentity(t *testing.T) {
	service := newEnrollmentService(t, &memorySettings{values: make(map[string][]byte)})
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if err := service.Configure(old.URL, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Register(context.Background(), "", ""); ErrorCode(err) != "relay_upgrade_required" {
		t.Fatalf("upgrade: %v", err)
	}
	service.mu.Lock()
	service.state.Ed25519Private = []byte{1}
	service.mu.Unlock()
	if err := service.Register(context.Background(), "", ""); ErrorCode(err) != "identity_conflict" {
		t.Fatalf("partial identity: %v", err)
	}
	if !service.blocked {
		t.Fatal("permanent error not stopped")
	}
	if !retryable(errors.New("network")) || retryable(&remoteError{code: "device_revoked"}) {
		t.Fatal("retry policy")
	}
	if delay := retryDelay(&remoteError{code: "rate_limited", retryAfter: time.Minute}, time.Second); delay != time.Minute {
		t.Fatal("ignored Retry-After")
	}
	for _, origin := range []string{"https://RELAY.EXAMPLE:443/", "https://relay.example"} {
		if normalized, err := normalizeRelayURL(origin); err != nil || normalized != "https://relay.example" {
			t.Fatalf("normalization: %s %v", normalized, err)
		}
	}
}

func TestIncompleteIdentityRequiresExplicitReset(t *testing.T) {
	for _, input := range []persistedState{
		{X25519Private: bytes.Repeat([]byte{1}, 32)},
		{Ed25519Public: bytes.Repeat([]byte{2}, 32)},
		{X25519Public: bytes.Repeat([]byte{3}, 32)},
		{CapabilityHash: bytes.Repeat([]byte{4}, 32)},
	} {
		input.RelayURL, input.Enabled = "https://first.example", true
		settings := &memorySettings{values: make(map[string][]byte)}
		if err := settings.PutSetting(remoteStateKey, input); err != nil {
			t.Fatal(err)
		}
		service := newEnrollmentService(t, settings)
		if err := service.Register(context.Background(), "", ""); ErrorCode(err) != "identity_conflict" {
			t.Fatalf("incomplete identity regenerated: %v", err)
		}
		if err := service.Configure("https://second.example", true); ErrorCode(err) != "relay_change_requires_reset" {
			t.Fatalf("incomplete identity bypassed reset gate: %v", err)
		}
	}
}
