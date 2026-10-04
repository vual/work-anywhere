package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	relayv1 "github.com/hurf/work-anywhere/desktop/internal/remote/gen/waw/relay/v1"
)

type failingEnrollmentSettings struct {
	*memorySettings
	failureMu      sync.Mutex
	failDeviceSave bool
}

func (settings *failingEnrollmentSettings) PutSetting(key string, value any) error {
	settings.failureMu.Lock()
	defer settings.failureMu.Unlock()
	if state, ok := value.(persistedState); ok && state.DeviceID != "" && settings.failDeviceSave {
		settings.failDeviceSave = false
		return errors.New("simulated settings failure")
	}
	return settings.memorySettings.PutSetting(key, value)
}

func TestEnrollmentIDSaveFailureRecoversWithoutNewKeys(t *testing.T) {
	settings := &failingEnrollmentSettings{memorySettings: &memorySettings{values: make(map[string][]byte)}, failDeviceSave: true}
	service := newEnrollmentService(t, settings)
	deviceID := uuid.NewString()
	var input map[string]string
	server := enrollmentMock(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"deviceId": deviceID, "created": false})
	}, &input)
	if err := service.Configure(server.URL, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Register(context.Background(), "Desk", ""); err == nil {
		t.Fatal("save failure ignored")
	}
	first := service.snapshotState()
	if first.DeviceID != "" || len(first.Ed25519Private) != 64 {
		t.Fatal("failed save changed in-memory identity")
	}
	service.mu.Lock()
	service.nextRetryAt = time.Time{}
	service.mu.Unlock()
	if err := service.Register(context.Background(), "Desk", ""); err != nil {
		t.Fatal(err)
	}
	if state := service.snapshotState(); state.DeviceID != deviceID || !bytes.Equal(first.Ed25519Private, state.Ed25519Private) {
		t.Fatal("save recovery regenerated keys")
	}
}

func TestDisableCancelsPendingEnrollment(t *testing.T) {
	service := newEnrollmentService(t, &memorySettings{values: make(map[string][]byte)})
	var input map[string]string
	started := make(chan struct{})
	release := make(chan struct{})
	server := enrollmentMock(t, func(writer http.ResponseWriter, request *http.Request) {
		close(started)
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
	if err := service.Configure(server.URL, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("disabled registration completed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not cancel")
	}
	if status := service.Status(); status.Enabled || status.DeviceID != "" || status.Phase != "disabled" {
		t.Fatal("old enrollment enabled remote")
	}
}

func TestAutomaticRegistrationRunsOnceAndRetainsConfiguredName(t *testing.T) {
	service := newEnrollmentService(t, &memorySettings{values: make(map[string][]byte)})
	var input map[string]string
	registered := make(chan struct{})
	var once sync.Once
	server := enrollmentMock(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"deviceId": uuid.NewString(), "created": true})
		once.Do(func() { close(registered) })
	}, &input)
	if err := service.ConfigureWithReset(server.URL, true, false, "Named PC"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic enrollment never started")
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for service.Status().DeviceID == "" {
		select {
		case <-deadline.C:
			t.Fatal("device ID not saved")
		case <-ticker.C:
		}
	}
	if service.snapshotState().DisplayName != "Named PC" {
		t.Fatal("configured name lost")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
}

func TestOldConnectionFrameCannotChangeNewConfiguration(t *testing.T) {
	service := newEnrollmentService(t, &memorySettings{values: make(map[string][]byte)})
	if err := service.Configure("https://first.example", true); err != nil {
		t.Fatal(err)
	}
	previousGeneration := service.snapshotState().Generation
	if err := service.ConfigureWithReset("https://second.example", true, true); err != nil {
		t.Fatal(err)
	}
	pairingID := uuid.New()
	service.mu.Lock()
	service.connected = true
	service.state.Pairings[pairingID.String()] = Pairing{ID: pairingID.String()}
	service.mu.Unlock()
	frame := &relayv1.ServerFrame{WireMajor: wireMajor, MessageId: newMessageID(), Body: &relayv1.ServerFrame_PairRevoked{PairRevoked: &relayv1.PairRevoked{PairingId: pairingID[:]}}}
	if err := service.handleCurrentFrame(nil, previousGeneration, frame); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale frame accepted: %v", err)
	}
	if len(service.snapshotState().Pairings) != 1 {
		t.Fatal("old frame changed new pairings")
	}
}

type failingConfigurationSettings struct {
	*memorySettings
	fail bool
}

func (settings *failingConfigurationSettings) PutSetting(key string, value any) error {
	if settings.fail {
		return errors.New("configuration save failed")
	}
	return settings.memorySettings.PutSetting(key, value)
}

func TestConfigurationSaveFailureCancelsOldOperationsAndRetainsIdentity(t *testing.T) {
	settings := &failingConfigurationSettings{memorySettings: &memorySettings{values: make(map[string][]byte)}}
	service := newEnrollmentService(t, settings)
	if err := service.Configure("https://first.example", true); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	if err := service.ensureIdentityLocked(); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	previous := service.state
	connectionCtx, cancelConnection := context.WithCancel(context.Background())
	registrationCtx, cancelRegistration := context.WithCancel(context.Background())
	service.operationCancel, service.registrationCancel = cancelConnection, cancelRegistration
	service.connected = true
	service.mu.Unlock()
	settings.fail = true
	if err := service.ConfigureWithReset("https://second.example", true, true); err == nil {
		t.Fatal("configuration save failure ignored")
	}
	if connectionCtx.Err() == nil || registrationCtx.Err() == nil || service.Status().Connected {
		t.Fatal("old operations still running")
	}
	current := service.snapshotState()
	if current.RelayURL != previous.RelayURL || !bytes.Equal(current.Ed25519Private, previous.Ed25519Private) || current.Generation == previous.Generation {
		t.Fatal("failed commit lost identity or accepted old tasks")
	}
}

func TestQueuedRegistrationCannotUseInvitationOnNewRelay(t *testing.T) {
	service := newEnrollmentService(t, &memorySettings{values: make(map[string][]byte)})
	if err := service.Configure("https://first.example", true); err != nil {
		t.Fatal(err)
	}
	previousGeneration := service.snapshotState().Generation
	if err := service.ConfigureWithReset("https://second.example", true, true); err != nil {
		t.Fatal(err)
	}
	if err := service.register(context.Background(), "Old name", "old-relay-invitation", true, previousGeneration); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued request not canceled: %v", err)
	}
	if state := service.snapshotState(); state.DisplayName != "" || len(state.Ed25519Private) != 0 {
		t.Fatal("queued registration changed new identity")
	}
}
