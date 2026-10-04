package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/internal/config"
	"github.com/vual/work-anywhere/relay/internal/store"
	"github.com/vual/work-anywhere/relay/pkg/endpoint/protocol"
)

func enrollmentTestServer(t *testing.T, mode string) (*Server, *fakeStore, ed25519.PrivateKey, map[string]string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := &fakeStore{devices: make(map[uuid.UUID]store.Device)}
	service := New(config.Config{PublicURL: "https://relay.example", EnrollmentMode: mode, MaxDaemons: 10_000}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	input := map[string]string{"displayName": "Desk", "ed25519PublicKey": base64.StdEncoding.EncodeToString(publicKey), "x25519PublicKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), "capabilityHash": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))}
	return service, state, privateKey, input
}

func enrollmentRequestBody(t *testing.T, service *Server, privateKey ed25519.PrivateKey, input map[string]string) map[string]string {
	t.Helper()
	raw, _ := json.Marshal(input)
	response := httptest.NewRecorder()
	service.enrollmentChallenge(response, httptest.NewRequest(http.MethodPost, "/v1/enrollment/challenges", bytes.NewReader(raw)))
	if response.Code != http.StatusCreated {
		t.Fatalf("challenge: %d %s", response.Code, response.Body.String())
	}
	var challenge struct {
		RequestID   string `json:"requestId"`
		Challenge   string `json:"challenge"`
		ExpiresAtMS int64  `json:"expiresAtMs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.MustParse(challenge.RequestID)
	challengeBytes, _ := base64.StdEncoding.DecodeString(challenge.Challenge)
	noisePublic, _ := base64.StdEncoding.DecodeString(input["x25519PublicKey"])
	capability, _ := base64.StdEncoding.DecodeString(input["capabilityHash"])
	transcript := protocol.EnrollmentSignatureInput(service.cfg.PublicURL, requestID[:], challengeBytes, challenge.ExpiresAtMS, privateKey.Public().(ed25519.PublicKey), noisePublic, capability, input["displayName"])
	return map[string]string{"requestId": requestID.String(), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, transcript))}
}

func enrollmentCompleteBody(service *Server, input map[string]string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(input)
	response := httptest.NewRecorder()
	service.completeEnrollment(response, httptest.NewRequest(http.MethodPost, "/v1/enrollment/complete", bytes.NewReader(raw)))
	return response
}

func TestSignedEnrollmentRecoveryAndReplay(t *testing.T) {
	service, state, privateKey, input := enrollmentTestServer(t, "self_service")
	body := enrollmentRequestBody(t, service, privateKey, input)
	first := enrollmentCompleteBody(service, body)
	if first.Code != 201 {
		t.Fatalf("register: %d %s", first.Code, first.Body.String())
	}
	if replay := enrollmentCompleteBody(service, body); replay.Code != 409 {
		t.Fatalf("replay accepted: %d", replay.Code)
	}
	input["displayName"] = "Changed"
	service.cfg.EnrollmentMode = "disabled"
	recovery := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input))
	if recovery.Code != 200 {
		t.Fatalf("recover: %d %s", recovery.Code, recovery.Body.String())
	}
	if len(state.devices) != 1 {
		t.Fatal("duplicate identity")
	}
	for _, device := range state.devices {
		if device.DisplayName != "Desk" {
			t.Fatal("recovery changed name")
		}
		device.Status = "revoked"
		state.devices[device.ID] = device
	}
	revoked := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input))
	if revoked.Code != 403 || !bytes.Contains(revoked.Body.Bytes(), []byte("device_revoked")) {
		t.Fatal("revoked identity recovered")
	}
}

func TestEnrollmentSignatureExpiryAndConcurrency(t *testing.T) {
	t.Run("wrong_signature", func(t *testing.T) {
		service, _, privateKey, input := enrollmentTestServer(t, "self_service")
		body := enrollmentRequestBody(t, service, privateKey, input)
		body["signature"] = base64.StdEncoding.EncodeToString(make([]byte, 64))
		if response := enrollmentCompleteBody(service, body); response.Code != 401 {
			t.Fatalf("wrong signature: %d", response.Code)
		}
	})
	t.Run("expired", func(t *testing.T) {
		service, _, privateKey, input := enrollmentTestServer(t, "self_service")
		body := enrollmentRequestBody(t, service, privateKey, input)
		requestID := uuid.MustParse(body["requestId"])
		pending := service.enrollments[requestID]
		pending.expiresAt = time.Now().Add(-time.Second).UnixMilli()
		service.enrollments[requestID] = pending
		if response := enrollmentCompleteBody(service, body); response.Code != 409 {
			t.Fatalf("expiry: %d", response.Code)
		}
	})
	t.Run("single_consumer", func(t *testing.T) {
		service, _, privateKey, input := enrollmentTestServer(t, "self_service")
		body := enrollmentRequestBody(t, service, privateKey, input)
		var wait sync.WaitGroup
		results := make(chan int, 2)
		for index := 0; index < 2; index++ {
			wait.Add(1)
			go func() { defer wait.Done(); results <- enrollmentCompleteBody(service, body).Code }()
		}
		wait.Wait()
		close(results)
		created, rejected := 0, 0
		for status := range results {
			if status == 201 {
				created++
			}
			if status == 409 {
				rejected++
			}
		}
		if created != 1 || rejected != 1 {
			t.Fatalf("created=%d rejected=%d", created, rejected)
		}
	})
}

func TestEnrollmentPolicyLimitsAndProxy(t *testing.T) {
	service, _, privateKey, input := enrollmentTestServer(t, "invite")
	if response := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input)); response.Code != 403 || !bytes.Contains(response.Body.Bytes(), []byte("invite_required")) {
		t.Fatal("invite gate missing")
	}
	for index := 0; index < 5; index++ {
		raw, _ := json.Marshal(input)
		response := httptest.NewRecorder()
		service.enrollmentChallenge(response, httptest.NewRequest("POST", "/", bytes.NewReader(raw)))
		if index == 4 && (response.Code != 429 || response.Header().Get("Retry-After") == "") {
			t.Fatal("limit missing")
		}
	}
	service.cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	for _, scenario := range []struct{ remote, header, expected string }{
		{"192.0.2.1:1234", "203.0.113.9", "192.0.2.1"},
		{"10.0.0.1:1234", "203.0.113.9, 10.0.0.2", "203.0.113.9"},
		{"10.0.0.1:1234", "fake, 203.0.113.9", "10.0.0.1"},
	} {
		request := httptest.NewRequest("GET", "/", nil)
		request.RemoteAddr = scenario.remote
		request.Header.Set("X-Forwarded-For", scenario.header)
		if got := service.peerIP(request); got != scenario.expected {
			t.Fatalf("peer %s expected %s", got, scenario.expected)
		}
	}
	service.enrollmentMu.Lock()
	service.expireEnrollmentsLocked(time.Now().Add(time.Minute).UnixMilli())
	service.enrollmentMu.Unlock()
	if len(service.enrollments) != 0 {
		t.Fatal("expired challenges retained")
	}
}

func TestDisabledEnrollmentRejectsNewDevice(t *testing.T) {
	service, _, privateKey, input := enrollmentTestServer(t, "disabled")
	response := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input))
	if response.Code != 403 {
		t.Fatalf("disabled: %d", response.Code)
	}
}

func TestRegisteredDeviceAuthenticatesExistingWebSocket(t *testing.T) {
	service, _, privateKey, input := enrollmentTestServer(t, "self_service")
	response := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input))
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	var result struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	capability, _ := base64.StdEncoding.DecodeString(input["capabilityHash"])
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	connection := connectTestDevice(t, server.URL, testDevice{id: uuid.MustParse(result.DeviceID), role: relayv1.DeviceRole_DEVICE_ROLE_DAEMON, privateKey: privateKey, capability: capability})
	defer connection.CloseNow()
	if service.registry.Count() != 1 {
		t.Fatal("registered device did not connect")
	}
}

func TestLegacyEnrollmentCannotRecoverOrReplaceSignedIdentity(t *testing.T) {
	service, state, privateKey, input := enrollmentTestServer(t, "open")
	if response := enrollmentCompleteBody(service, enrollmentRequestBody(t, service, privateKey, input)); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	for _, scenario := range []struct {
		status string
		code   string
	}{
		{"active", "identity_conflict"},
		{"revoked", "device_revoked"},
	} {
		for deviceID, device := range state.devices {
			device.Status = scenario.status
			state.devices[deviceID] = device
		}
		raw, _ := json.Marshal(input)
		response := httptest.NewRecorder()
		service.enrollDaemon(response, httptest.NewRequest(http.MethodPost, "/v1/enroll/daemon", bytes.NewReader(raw)))
		if !bytes.Contains(response.Body.Bytes(), []byte(scenario.code)) || response.Code == 201 {
			t.Fatalf("legacy bypass: %d %s", response.Code, response.Body.String())
		}
	}
	if len(state.devices) != 1 {
		t.Fatal("legacy registration duplicated identity")
	}
}
