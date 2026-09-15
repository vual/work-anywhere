package devmobile

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/pkg/endpoint/e2ee"
	waProtocol "github.com/vual/work-anywhere/relay/pkg/endpoint/protocol"
	"google.golang.org/protobuf/proto"
)

func TestRouteAckCorrelatesByOuterMessageID(t *testing.T) {
	client := New()
	waiter := make(chan routeResult, 1)
	client.pendingRoutes["message-1"] = waiter
	if !client.handleRouteAck(&relayv1.RouteAck{RoutedMessageId: []byte("message-1"), Accepted: false, ErrorCode: "queue_full"}) {
		t.Fatal("known route ack was not handled")
	}
	result := <-waiter
	if result.accepted || result.code != "queue_full" || len(client.pendingRoutes) != 0 {
		t.Fatalf("unexpected route result: %#v", result)
	}
	if client.handleRouteAck(&relayv1.RouteAck{RoutedMessageId: []byte("late"), Accepted: true}) {
		t.Fatal("late or unknown ACK must not be correlated")
	}
}

func TestEventSinkReceivesOnlyEventsAndCurrentTransportState(t *testing.T) {
	client := New()
	received := make([]string, 0, 2)
	client.SetEventSink(func(value string) { received = append(received, value) })
	client.dispatchControlPayload(json.RawMessage(`{"type":"response","id":"late","result":{"secret":true}}`))
	client.dispatchControlPayload(json.RawMessage(`{"type":"unknown","value":"ignored"}`))
	client.dispatchControlPayload(json.RawMessage(`{"type":"event","sequence":7,"event":"turn.completed"}`))
	if len(received) != 2 || received[0] != `{"state":"disconnected","type":"transport"}` || received[1] != `{"type":"event","sequence":7,"event":"turn.completed"}` {
		t.Fatalf("unexpected event stream: %#v", received)
	}
}

func TestPeerOfflineInvalidatesSecureTransport(t *testing.T) {
	client := New()
	client.connected = true
	client.pending["request"] = make(chan json.RawMessage)
	client.pendingRoutes["route"] = make(chan routeResult)
	received := make([]string, 0, 2)
	client.SetEventSink(func(value string) { received = append(received, value) })

	if !client.disconnectConnection(nil, "peer_offline", "Desktop peer is offline") {
		t.Fatal("active transport was not invalidated")
	}
	status := client.Status()
	if status.Connected || status.LastError != "Desktop peer is offline" {
		t.Fatalf("unexpected disconnected status: %#v", status)
	}
	if len(client.pending) != 0 || len(client.pendingRoutes) != 0 {
		t.Fatal("disconnect did not release pending calls")
	}
	if len(received) != 2 || received[0] != `{"state":"online","type":"transport"}` || received[1] != `{"errorCode":"peer_offline","state":"disconnected","type":"transport"}` {
		t.Fatalf("unexpected transport events: %#v", received)
	}
}

func TestBufferedEventsAreBoundedAndDrainedInOrder(t *testing.T) {
	client := New()
	for index := 0; index < 1002; index++ {
		client.dispatchControlPayload(json.RawMessage(fmt.Sprintf(`{"type":"event","sequence":%d}`, index)))
	}
	received := make([]string, 0, 1001)
	client.SetEventSink(func(value string) { received = append(received, value) })
	if len(received) != 1001 {
		t.Fatalf("received %d envelopes, want transport plus 1000 events", len(received))
	}
	if received[1] != `{"type":"event","sequence":2}` || received[len(received)-1] != `{"type":"event","sequence":1001}` {
		t.Fatalf("buffer did not retain the newest events in order: first=%s last=%s", received[1], received[len(received)-1])
	}
	if len(client.Status().Events) != 0 {
		t.Fatal("buffer must be empty after attaching a sink")
	}
}

func TestKnownResponseIsCorrelatedAndNotEmitted(t *testing.T) {
	client := New()
	waiter := make(chan json.RawMessage, 1)
	client.pending["request-1"] = waiter
	client.dispatchControlPayload(json.RawMessage(`{"type":"response","id":"request-1","result":{"ok":true}}`))
	select {
	case response := <-waiter:
		if string(response) != `{"type":"response","id":"request-1","result":{"ok":true}}` {
			t.Fatalf("unexpected response: %s", response)
		}
	default:
		t.Fatal("known response was not correlated")
	}
	if len(client.Status().Events) != 0 {
		t.Fatal("responses must never enter the event buffer")
	}
}

func TestReconnectBackoffPreservesRateLimitError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := New()
	client.credential = testCredential(server.URL)

	err := client.reconnect(context.Background())
	callError, ok := err.(*CallError)
	if !ok || callError.Code != "rate_limited" {
		t.Fatalf("reconnect error = %v, want rate_limited CallError", err)
	}
	if err = client.reconnect(context.Background()); err == nil {
		t.Fatal("second reconnect unexpectedly succeeded")
	} else if callError, ok = err.(*CallError); !ok || callError.Code != "rate_limited" {
		t.Fatalf("backoff error = %v, want rate_limited CallError", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("backoff allowed %d dial attempts, want 1", got)
	}
	if time.Until(client.connectUntil) <= 0 {
		t.Fatal("reconnect did not set a cooldown")
	}
}

func TestRestoreRetainsCredentialAndIdentityAfterRateLimit(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	credential := testCredential(server.URL)
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}

	client := New()
	err = client.Restore(context.Background(), raw)
	callError, ok := err.(*CallError)
	if !ok || callError.Code != "rate_limited" {
		t.Fatalf("restore error = %v, want rate_limited CallError", err)
	}
	if _, err = client.ExportCredentials(); err != nil {
		t.Fatalf("credentials were lost after transient restore failure: %v", err)
	}
	status := client.Status()
	if status.PairingID != credential.PairingID || status.DaemonID != credential.DaemonDeviceID || status.DeviceID != credential.MobileDeviceID {
		t.Fatalf("restored identity was lost: %#v", status)
	}
	if err = client.reconnect(context.Background()); err == nil {
		t.Fatal("reconnect during cooldown unexpectedly succeeded")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("restore cooldown allowed %d dial attempts, want 1", got)
	}
}

func TestSessionHandshakeFromFrameRejectsRouteAndIgnoresStaleSession(t *testing.T) {
	pairingID := uuid.New()
	daemonID := uuid.New()
	mobileID := uuid.New()
	openMessageID := newID()
	sessionID := newID()

	rejected := &relayv1.ServerFrame{Body: &relayv1.ServerFrame_RouteAck{RouteAck: &relayv1.RouteAck{RoutedMessageId: openMessageID, ErrorCode: "peer_offline"}}}
	if _, err := sessionHandshakeFromFrame(rejected, openMessageID, pairingID, sessionID, daemonID, mobileID); err == nil {
		t.Fatal("rejected SessionOpen route did not fail the handshake")
	} else if callError, ok := err.(*CallError); !ok || callError.Code != "peer_offline" {
		t.Fatalf("route rejection = %v, want peer_offline CallError", err)
	}

	stale := &relayv1.ServerFrame{Body: &relayv1.ServerFrame_SessionHandshake{SessionHandshake: &relayv1.SessionHandshake{PairingId: pairingID[:], SessionId: newID(), SenderDeviceId: daemonID[:], RecipientDeviceId: mobileID[:], Handshake: []byte("stale")}}}
	if candidate, err := sessionHandshakeFromFrame(stale, openMessageID, pairingID, sessionID, daemonID, mobileID); err != nil || candidate != nil {
		t.Fatalf("stale handshake candidate = %v, error = %v", candidate, err)
	}

	expectedMessage := &relayv1.SessionHandshake{PairingId: pairingID[:], SessionId: sessionID, SenderDeviceId: daemonID[:], RecipientDeviceId: mobileID[:], Handshake: []byte("expected")}
	expected := &relayv1.ServerFrame{Body: &relayv1.ServerFrame_SessionHandshake{SessionHandshake: expectedMessage}}
	if candidate, err := sessionHandshakeFromFrame(expected, openMessageID, pairingID, sessionID, daemonID, mobileID); err != nil || candidate != expectedMessage {
		t.Fatalf("matching handshake candidate = %v, error = %v", candidate, err)
	}
}

func TestRestoredConnectionOutlivesCallerAndIgnoresStaleHandshake(t *testing.T) {
	_, mobilePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mobileX, err := e2ee.GenerateStaticKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	daemonX, err := e2ee.GenerateStaticKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pairingID, daemonID, mobileID := uuid.New(), uuid.New(), uuid.New()
	daemonCapability := []byte("daemon-capability")
	mobileCapability := []byte("mobile-capability")
	handlerErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, acceptErr := websocket.Accept(w, request, nil)
		if acceptErr != nil {
			handlerErrors <- acceptErr
			return
		}
		defer connection.CloseNow()
		ctx := request.Context()
		challenge := &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, MessageId: newID(), Body: &relayv1.ServerFrame_ServerChallenge{ServerChallenge: &relayv1.ServerChallenge{ConnectionId: newID(), Challenge: make([]byte, 32), ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli()}}}
		if writeErr := writeServerFrameForTest(ctx, connection, challenge); writeErr != nil {
			handlerErrors <- writeErr
			return
		}
		if _, readErr := readClientFrameForTest(ctx, connection); readErr != nil {
			handlerErrors <- readErr
			return
		}
		if writeErr := writeServerFrameForTest(ctx, connection, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, MessageId: newID(), Body: &relayv1.ServerFrame_AuthOk{AuthOk: &relayv1.AuthOk{DeviceId: mobileID[:]}}}); writeErr != nil {
			handlerErrors <- writeErr
			return
		}
		openFrame, readErr := readClientFrameForTest(ctx, connection)
		if readErr != nil {
			handlerErrors <- readErr
			return
		}
		open := openFrame.GetSessionOpen()
		binding := e2ee.Binding{WireMajor: waProtocol.WireMajor, PairingID: pairingID[:], DaemonDeviceID: daemonID[:], MobileDeviceID: mobileID[:], SessionID: open.SessionId, DaemonCapability: daemonCapability, MobileCapability: mobileCapability}
		responder, handshakeErr := e2ee.NewHandshake(false, daemonX, mobileX.Public, binding, rand.Reader)
		if handshakeErr != nil {
			handlerErrors <- handshakeErr
			return
		}
		if _, _, _, handshakeErr = responder.ReadMessage(nil, open.Handshake); handshakeErr != nil {
			handlerErrors <- handshakeErr
			return
		}
		response, _, _, handshakeErr := responder.WriteMessage(nil, nil)
		if handshakeErr != nil {
			handlerErrors <- handshakeErr
			return
		}
		frames := []*relayv1.ServerFrame{
			{WireMajor: waProtocol.WireMajor, MessageId: newID(), Body: &relayv1.ServerFrame_RouteAck{RouteAck: &relayv1.RouteAck{RoutedMessageId: openFrame.MessageId, Accepted: true}}},
			{WireMajor: waProtocol.WireMajor, MessageId: newID(), Body: &relayv1.ServerFrame_SessionHandshake{SessionHandshake: &relayv1.SessionHandshake{PairingId: pairingID[:], SessionId: newID(), SenderDeviceId: daemonID[:], RecipientDeviceId: mobileID[:], Handshake: []byte("stale")}}},
			{WireMajor: waProtocol.WireMajor, MessageId: newID(), Body: &relayv1.ServerFrame_SessionHandshake{SessionHandshake: &relayv1.SessionHandshake{PairingId: pairingID[:], SessionId: open.SessionId, SenderDeviceId: daemonID[:], RecipientDeviceId: mobileID[:], Handshake: response}}},
		}
		for _, frame := range frames {
			if writeErr := writeServerFrameForTest(ctx, connection, frame); writeErr != nil {
				handlerErrors <- writeErr
				return
			}
		}
		<-ctx.Done()
	}))
	defer server.Close()
	credential := CredentialSet{RelayURL: server.URL, PairingID: pairingID.String(), DaemonDeviceID: daemonID.String(), MobileDeviceID: mobileID.String(), Ed25519Private: mobilePrivate, X25519Private: mobileX.Private, X25519Public: mobileX.Public, DaemonX25519: daemonX.Public, DaemonCapability: daemonCapability, MobileCapability: mobileCapability}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	client := New()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	if err = client.Restore(ctx, raw); err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if !client.Status().Connected {
		t.Fatal("successful connection inherited the canceled restore context")
	}
	select {
	case err = <-handlerErrors:
		t.Fatalf("test Relay failed: %v", err)
	default:
	}
}

func readClientFrameForTest(ctx context.Context, connection *websocket.Conn) (*relayv1.ClientFrame, error) {
	kind, raw, err := connection.Read(ctx)
	if err != nil {
		return nil, err
	}
	if kind != websocket.MessageBinary {
		return nil, fmt.Errorf("client sent websocket message kind %v", kind)
	}
	var frame relayv1.ClientFrame
	if err = proto.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

func writeServerFrameForTest(ctx context.Context, connection *websocket.Conn, frame *relayv1.ServerFrame) error {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageBinary, raw)
}

func testCredential(relayURL string) CredentialSet {
	return CredentialSet{
		RelayURL:         relayURL,
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
}
