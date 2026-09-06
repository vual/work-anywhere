package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/internal/config"
	"github.com/vual/work-anywhere/relay/internal/jobs"
	waProtocol "github.com/vual/work-anywhere/relay/internal/protocol"
	"github.com/vual/work-anywhere/relay/internal/routing"
	"github.com/vual/work-anywhere/relay/internal/secretbox"
	"github.com/vual/work-anywhere/relay/internal/store"
)

type fakeStore struct {
	mu              sync.Mutex
	devices         map[uuid.UUID]store.Device
	pairingID       uuid.UUID
	session         store.PairingSession
	pushEndpoint    store.PushEndpoint
	pushEndpointIDs []uuid.UUID
}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) Enroll(_ context.Context, input store.EnrollmentInput, _ bool) (store.Device, error) {
	value := store.Device{ID: uuid.New(), Role: input.Role, DisplayName: input.DisplayName, Ed25519PublicKey: input.Ed25519PublicKey, X25519PublicKey: input.X25519PublicKey, CapabilityHash: input.CapabilityHash, Status: "active"}
	f.mu.Lock()
	f.devices[value.ID] = value
	f.mu.Unlock()
	return value, nil
}
func (f *fakeStore) Device(_ context.Context, id uuid.UUID) (store.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices[id], nil
}
func (f *fakeStore) TouchDevice(context.Context, uuid.UUID) {}
func (f *fakeStore) ActivePairing(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, []string, error) {
	return f.pairingID, []string{"conversation.read"}, nil
}

func (f *fakeStore) CreatePairingSession(_ context.Context, id, daemonID uuid.UUID, expires time.Time) error {
	f.mu.Lock()
	f.session = store.PairingSession{ID: id, DaemonDeviceID: daemonID, ExpiresAt: expires, Status: "pending"}
	f.mu.Unlock()
	return nil
}
func (f *fakeStore) CancelPairingSession(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeStore) ClaimPairingAttempt(_ context.Context, id uuid.UUID) (store.PairingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session.ID != id || f.session.Status != "pending" {
		return store.PairingSession{}, errors.New("not found")
	}
	f.session.AttemptCount++
	return f.session, nil
}
func (f *fakeStore) CompletePairing(_ context.Context, session store.PairingSession, mobile store.MobileEnrollment, _ []string, _ []byte) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[mobile.ID] = store.Device{ID: mobile.ID, Role: int16(relayv1.DeviceRole_DEVICE_ROLE_MOBILE), DisplayName: mobile.DisplayName, Ed25519PublicKey: mobile.Ed25519PublicKey, X25519PublicKey: mobile.X25519PublicKey, CapabilityHash: mobile.CapabilityHash, Status: "active"}
	f.session.Status = "approved"
	f.pairingID = session.ID
	return session.ID, nil
}
func (f *fakeStore) RevokePairing(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	return uuid.Nil, uuid.Nil, nil
}
func (f *fakeStore) UpsertPushEndpoint(_ context.Context, endpoint store.PushEndpoint) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushEndpoint = endpoint
	return uuid.New(), nil
}

type fakePushQueue struct {
	endpointIDs  []uuid.UUID
	notification jobs.Notification
}

func (f *fakePushQueue) Enqueue(_ context.Context, endpointIDs []uuid.UUID, notification jobs.Notification) error {
	f.endpointIDs = append([]uuid.UUID(nil), endpointIDs...)
	f.notification = notification
	return nil
}
func (f *fakeStore) DeletePushEndpoint(context.Context, uuid.UUID, string, string, string) error {
	return nil
}
func (f *fakeStore) ActivePushEndpointIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return append([]uuid.UUID(nil), f.pushEndpointIDs...), nil
}

type testDevice struct {
	id         uuid.UUID
	role       relayv1.DeviceRole
	privateKey ed25519.PrivateKey
	capability []byte
}

func TestAuthenticatedConnectionsRouteCiphertext(t *testing.T) {
	state := &fakeStore{devices: make(map[uuid.UUID]store.Device), pairingID: uuid.New()}
	first := newTestDevice(t, relayv1.DeviceRole_DEVICE_ROLE_DAEMON)
	second := newTestDevice(t, relayv1.DeviceRole_DEVICE_ROLE_MOBILE)
	state.devices[first.id] = testStoreDevice(first)
	state.devices[second.id] = testStoreDevice(second)
	service := New(config.Config{EnrollmentMode: "invite", MaxConnections: 10, ShutdownTimeout: time.Second}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	firstConn := connectTestDevice(t, httpServer.URL, first)
	defer firstConn.CloseNow()
	secondConn := connectTestDevice(t, httpServer.URL, second)
	defer secondConn.CloseNow()

	messageID, _ := waProtocol.NewID()
	frame := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: messageID, Body: &relayv1.ClientFrame_SessionData{SessionData: &relayv1.SessionData{PairingId: state.pairingID[:], SessionId: mustTestID(t), RecipientDeviceId: second.id[:], SenderSequence: 1, TrafficClass: relayv1.TrafficClass_TRAFFIC_CLASS_CONTROL, CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli(), Ciphertext: []byte("opaque")}}}
	writeClientFrame(t, firstConn, frame)

	forwarded := readServerFrame(t, secondConn)
	if string(forwarded.GetSessionData().Ciphertext) != "opaque" || !uuidBytesEqual(forwarded.GetSessionData().SenderDeviceId, first.id) {
		t.Fatalf("unexpected forwarded frame: %+v", forwarded.GetSessionData())
	}
	ack := readServerFrame(t, firstConn)
	if !ack.GetRouteAck().Accepted || string(ack.GetRouteAck().RoutedMessageId) != string(messageID) {
		t.Fatalf("unexpected route ack: %+v", ack.GetRouteAck())
	}
}

func TestPairingCreatesSignedMobileIdentity(t *testing.T) {
	state := &fakeStore{devices: make(map[uuid.UUID]store.Device)}
	daemon := newTestDevice(t, relayv1.DeviceRole_DEVICE_ROLE_DAEMON)
	state.devices[daemon.id] = testStoreDevice(daemon)
	service := New(config.Config{EnrollmentMode: "invite", MaxConnections: 10, ShutdownTimeout: time.Second}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()
	daemonConn := connectTestDevice(t, httpServer.URL, daemon)
	defer daemonConn.CloseNow()

	pairingID := uuid.New()
	expiresAt := time.Now().Add(time.Minute)
	writeClientFrame(t, daemonConn, &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustTestID(t), Body: &relayv1.ClientFrame_PairStart{PairStart: &relayv1.PairStart{PairingId: pairingID[:], ExpiresAtMs: expiresAt.UnixMilli()}}})
	if started := readServerFrame(t, daemonConn).GetPairStarted(); started == nil || !uuidBytesEqual(started.PairingId, pairingID) {
		t.Fatal("pairing session was not started")
	}

	mobilePublic, _, _ := ed25519.GenerateKey(rand.Reader)
	mobileX := make([]byte, 32)
	_, _ = rand.Read(mobileX)
	mobileCapability := []byte("mobile-capabilities")
	type pairOutcome struct {
		result *relayv1.PairResult
		err    error
	}
	outcome := make(chan pairOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pairConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/pair", nil)
		if err != nil {
			outcome <- pairOutcome{err: err}
			return
		}
		defer pairConn.CloseNow()
		challenge, err := readServerFrameE(ctx, pairConn)
		if err == nil {
			join := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustTestID(t), Body: &relayv1.ClientFrame_PairJoin{PairJoin: &relayv1.PairJoin{PairingId: pairingID[:], MobileEd25519PublicKey: mobilePublic, MobileX25519PublicKey: mobileX, DisplayName: "test mobile", CapabilityHash: mobileCapability, Proof: make([]byte, 32)}}}
			_ = challenge
			raw, _ := proto.Marshal(join)
			err = pairConn.Write(ctx, websocket.MessageBinary, raw)
		}
		if err != nil {
			outcome <- pairOutcome{err: err}
			return
		}
		result, err := readServerFrameE(ctx, pairConn)
		outcome <- pairOutcome{result: result.GetPairResult(), err: err}
	}()

	hello := readServerFrame(t, daemonConn).GetPairHello()
	if hello == nil {
		t.Fatal("daemon did not receive mobile pairing request")
	}
	scopes := []string{"conversation.read", "status.read"}
	grantExpires := expiresAt.Add(-time.Second).UnixMilli()
	signatureInput := waProtocol.PairGrantSignatureInput(pairingID[:], daemon.id[:], hello.CandidateMobileDeviceId, mobilePublic, mobileX, scopes, grantExpires)
	writeClientFrame(t, daemonConn, &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustTestID(t), Body: &relayv1.ClientFrame_PairDecision{PairDecision: &relayv1.PairDecision{PairingId: pairingID[:], Approved: true, MobileDeviceId: hello.CandidateMobileDeviceId, Scopes: scopes, GrantExpiresAtMs: grantExpires, GrantSignature: ed25519.Sign(daemon.privateKey, signatureInput)}}})

	paired := <-outcome
	if paired.err != nil || paired.result == nil || !paired.result.Approved || len(paired.result.GrantSignature) != ed25519.SignatureSize {
		t.Fatalf("pairing did not complete: result=%+v err=%v", paired.result, paired.err)
	}
	if !ed25519.Verify(daemon.privateKey.Public().(ed25519.PublicKey), signatureInput, paired.result.GrantSignature) {
		t.Fatal("mobile could not verify pairing grant")
	}
}

func TestPushRegistrationEncryptsTokenAndNotifyQueuesOnlyMetadata(t *testing.T) {
	mobileID, daemonID, pairingID, endpointID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	state := &fakeStore{devices: make(map[uuid.UUID]store.Device), pairingID: pairingID, pushEndpointIDs: []uuid.UUID{endpointID}}
	queue := &fakePushQueue{}
	service := New(config.Config{MaxConnections: 10, ACLCacheTTL: time.Second, DataKey: bytes.Repeat([]byte{9}, 32)}, state, slog.New(slog.NewTextHandler(io.Discard, nil)), WithPushQueue(queue))
	mobile := routing.NewConnection(context.Background(), mobileID, int16(relayv1.DeviceRole_DEVICE_ROLE_MOBILE), 10, 1<<20)
	token := []byte("provider-registration-token")
	if err := service.registerPush(mobile, &relayv1.PushRegister{Platform: "fcm", AppId: "com.example", Environment: "test", Locale: "zh-CN", Generation: 1, ProviderToken: token}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state.pushEndpoint.TokenCiphertext, token) {
		t.Fatal("provider token was retained in plaintext")
	}
	aad := secretbox.AssociatedData(mobileID.String(), "fcm", "com.example", "test")
	plain, err := service.box.Open(state.pushEndpoint.TokenCiphertext, aad)
	if err != nil || !bytes.Equal(plain, token) {
		t.Fatalf("provider token envelope failed: %q %v", plain, err)
	}
	daemon := routing.NewConnection(context.Background(), daemonID, int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON), 10, 1<<20)
	notificationID := mustTestID(t)
	if err = service.notify(daemon, &relayv1.Notify{PairingId: pairingID[:], RecipientDeviceId: mobileID[:], NotificationId: notificationID, Hint: relayv1.NotificationHint_NOTIFICATION_HINT_ACTION_REQUIRED, CollapseKey: "action", ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if len(queue.endpointIDs) != 1 || queue.endpointIDs[0] != endpointID || queue.notification.Hint != "action_required" || queue.notification.ID == "" {
		t.Fatalf("unexpected queued notification: %+v %+v", queue.endpointIDs, queue.notification)
	}
}

func newTestDevice(t *testing.T, role relayv1.DeviceRole) testDevice {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = publicKey
	return testDevice{id: uuid.New(), role: role, privateKey: privateKey, capability: []byte("test-capabilities")}
}

func testStoreDevice(value testDevice) store.Device {
	return store.Device{ID: value.id, Role: int16(value.role), Ed25519PublicKey: value.privateKey.Public().(ed25519.PublicKey), X25519PublicKey: make([]byte, 32), CapabilityHash: value.capability, Status: "active"}
}

func connectTestDevice(t *testing.T, baseURL string, device testDevice) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/v1/connect"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	challengeFrame := readServerFrame(t, conn)
	challenge := challengeFrame.GetServerChallenge()
	input := waProtocol.AuthSignatureInput(challenge.ConnectionId, challenge.Challenge, challenge.ExpiresAtMs, device.capability)
	auth := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustTestID(t), Body: &relayv1.ClientFrame_ClientAuth{ClientAuth: &relayv1.ClientAuth{DeviceId: device.id[:], Role: device.role, CapabilityHash: device.capability, Signature: ed25519.Sign(device.privateKey, input)}}}
	writeClientFrame(t, conn, auth)
	if ok := readServerFrame(t, conn).GetAuthOk(); ok == nil || !uuidBytesEqual(ok.DeviceId, device.id) {
		t.Fatal("authentication did not complete")
	}
	return conn
}

func writeClientFrame(t *testing.T, conn *websocket.Conn, frame *relayv1.ClientFrame) {
	t.Helper()
	raw, err := proto.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = conn.Write(ctx, websocket.MessageBinary, raw); err != nil {
		t.Fatal(err)
	}
}

func readServerFrame(t *testing.T, conn *websocket.Conn) *relayv1.ServerFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, raw, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary {
		t.Fatalf("read server frame: kind=%v err=%v", kind, err)
	}
	var frame relayv1.ServerFrame
	if err = proto.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	return &frame
}

func readServerFrameE(ctx context.Context, conn *websocket.Conn) (*relayv1.ServerFrame, error) {
	kind, raw, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary {
		return nil, err
	}
	var frame relayv1.ServerFrame
	if err = proto.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

func mustTestID(t *testing.T) []byte {
	t.Helper()
	value, err := waProtocol.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func uuidBytesEqual(raw []byte, id uuid.UUID) bool { return string(raw) == string(id[:]) }
