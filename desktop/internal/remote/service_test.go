package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	relayv1 "github.com/hurf/work-anywhere/desktop/internal/remote/gen/waw/relay/v1"
)

type memorySettings struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (m *memorySettings) PutSetting(key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := json.Marshal(value)
	if err == nil {
		m.values[key] = data
	}
	return err
}
func (m *memorySettings) GetSetting(key string, target any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.values[key]
	if data == nil {
		return false, nil
	}
	return true, json.Unmarshal(data, target)
}
func (m *memorySettings) DeleteSetting(key string) error {
	m.mu.Lock()
	delete(m.values, key)
	m.mu.Unlock()
	return nil
}

type discardLog struct{}

func (discardLog) Event(string, map[string]any) {}

func TestConfigureAndEnrollPersistsSecureIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/enroll/daemon" {
			http.NotFound(w, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) == "" || json.Valid(body) == false {
			t.Fatal("invalid enrollment request")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"deviceId":"fb6a9823-2632-49ab-a2e7-3df33890c179"}`))
	}))
	defer server.Close()
	settings := &memorySettings{values: make(map[string][]byte)}
	events := make(chan protocol.Event)
	service, err := New(settings, func(request protocol.ControlRequest) protocol.ControlResponse {
		return protocol.ControlResponse{Type: "response", ID: request.ID}
	}, func() (<-chan protocol.Event, func()) { return events, func() {} }, discardLog{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Configure(server.URL, false); err != nil {
		t.Fatal(err)
	}
	if err = service.Enroll(context.Background(), "one-time-token", "desktop"); err != nil {
		t.Fatal(err)
	}
	var state persistedState
	found, err := settings.GetSetting(remoteStateKey, &state)
	if err != nil || !found || len(state.Ed25519Private) != 64 || len(state.X25519Private) != 32 || state.DeviceID == "" {
		t.Fatalf("identity was not persisted: found=%v state=%+v err=%v", found, state, err)
	}
	status := service.Status()
	encoded, _ := json.Marshal(status)
	if string(encoded) == "" || containsBytes(encoded, state.Ed25519Private) || containsBytes(encoded, state.X25519Private) {
		t.Fatal("status exposed a private key")
	}
}

func TestNormalizeRelayURLAllowsPrivateIPDevelopment(t *testing.T) {
	allowed := []string{
		"http://127.0.0.1:18080",
		"http://172.20.31.206:18080",
		"http://192.168.1.20:18080/",
		"http://203.0.113.20:18080",
		"http://relay.example:18080",
		"http://[fd00::20]:18080",
		"https://203.0.113.20:18080",
		"https://relay.example:18080",
	}
	for _, value := range allowed {
		if _, err := normalizeRelayURL(value); err != nil {
			t.Errorf("normalizeRelayURL(%q) returned error: %v", value, err)
		}
	}

	denied := []string{
		"ftp://relay.example:18080",
	}
	for _, value := range denied {
		if _, err := normalizeRelayURL(value); err == nil {
			t.Errorf("normalizeRelayURL(%q) unexpectedly allowed unsupported scheme", value)
		}
	}
}

func TestQRRoundTripAndScopeAllowlist(t *testing.T) {
	payload := QRPayload{RelayURL: "https://relay.example", PairingID: "fb6a9823-2632-49ab-a2e7-3df33890c179", Secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", DaemonDeviceID: "bbab77cb-2e66-48bc-b72b-b817a4c3f3ab", ComputerName: "DESKTOP-TEST", ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}
	uri, err := payload.URI()
	if err != nil {
		t.Fatal(err)
	}
	restored, secret, err := ParseQR(uri)
	if err != nil || restored.PairingID != payload.PairingID || restored.ComputerName != payload.ComputerName || len(secret) != 32 {
		t.Fatalf("QR roundtrip failed: restored=%+v err=%v", restored, err)
	}
	if !scopeAllows([]string{"turn.start"}, "turn.start") ||
		!scopeAllows([]string{"turn.start"}, "plan.respond") ||
		!scopeAllows([]string{"turn.start"}, "conversation.settings.update") ||
		!scopeAllows([]string{"turn.start"}, "conversation.rename") ||
		!scopeAllows([]string{"turn.start"}, "conversation.queue.add") ||
		!scopeAllows([]string{"turn.start"}, "conversation.queue.update") ||
		!scopeAllows([]string{"turn.start"}, "conversation.queue.delete") ||
		!scopeAllows([]string{"turn.start"}, "conversation.queue.start") ||
		!scopeAllows([]string{"turn.start"}, "conversation.create") ||
		!scopeAllows([]string{"conversation.create"}, "conversation.create") ||
		scopeAllows([]string{"turn.start"}, "system.shutdown") {
		t.Fatal("remote method allowlist is incorrect")
	}
}

func TestPairingAttemptExpiryClearsSecret(t *testing.T) {
	attempt := &PairAttempt{ID: "expired", ExpiresAt: time.Now().Add(10 * time.Millisecond), secret: []byte{1, 2, 3}, MobileEd25519: []byte{4}}
	service := &Service{ctx: context.Background(), attempts: map[string]*PairAttempt{attempt.ID: attempt}}
	go service.expireAttempt(attempt)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		_, exists := service.attempts[attempt.ID]
		service.mu.RUnlock()
		if !exists {
			if attempt.secret != nil || attempt.MobileEd25519 != nil {
				t.Fatal("expired pairing material was not cleared")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expired pairing attempt was not removed")
}

func TestApprovedPairingResultIsValidatedBeforeAttemptIsCleared(t *testing.T) {
	id := uuid.New()
	signature := []byte{1, 2, 3}
	attempt := &PairAttempt{ID: id.String(), MobileDeviceID: uuid.NewString(), MobileDisplayName: "mobile", MobileEd25519: []byte{4}, MobileX25519: []byte{5}, MobileCapability: []byte{6}, grantedScopes: []string{"status.read"}, grantExpiresAtMS: 42, grantSignature: append([]byte(nil), signature...)}
	settings := &memorySettings{values: make(map[string][]byte)}
	service := &Service{store: settings, attempts: map[string]*PairAttempt{id.String(): attempt}, state: persistedState{Pairings: make(map[string]Pairing)}}
	result := &relayv1.PairResult{PairingId: id[:], Approved: true, Scopes: []string{"status.read"}, GrantExpiresAtMs: 42, GrantSignature: signature}
	if err := service.handlePairResult(result); err != nil {
		t.Fatal(err)
	}
	pairing, exists := service.state.Pairings[id.String()]
	if !exists || len(pairing.MobileEd25519) != 1 || len(pairing.MobileX25519) != 1 || attempt.secret != nil || attempt.grantSignature != nil {
		t.Fatalf("approved pairing was not persisted safely: exists=%v pairing=%+v", exists, pairing)
	}
}

func TestRejectedPairingRouteAckAllowsRetry(t *testing.T) {
	id := uuid.NewString()
	attempt := &PairAttempt{ID: id, decisionSubmitted: true, decisionMessageID: []byte{1, 2, 3}, grantedScopes: []string{"status.read"}, grantExpiresAtMS: 42, grantSignature: []byte{4}}
	service := &Service{attempts: map[string]*PairAttempt{id: attempt}}
	if err := service.handleRouteAck(&relayv1.RouteAck{Accepted: false, RoutedMessageId: []byte{1, 2, 3}, ErrorCode: "invalid_pairing_decision"}); err != nil {
		t.Fatal(err)
	}
	if attempt.decisionSubmitted || attempt.decisionMessageID != nil || attempt.grantedScopes != nil || attempt.grantExpiresAtMS != 0 || attempt.grantSignature != nil {
		t.Fatalf("rejected route ack did not reset attempt: %+v", attempt)
	}
}

func TestPeerOfflineRouteAckDropsOnlyTheStaleSession(t *testing.T) {
	staleSessionID := string(newMessageID())
	currentSessionID := string(newMessageID())
	staleMessageID := newMessageID()
	queuedStaleMessageID := newMessageID()
	currentMessageID := newMessageID()
	service := &Service{
		log: discardLog{},
		sessions: map[string]*sessionState{
			staleSessionID:   {},
			currentSessionID: {},
		},
		sentControl: map[string]sentControlDelivery{
			string(staleMessageID):       {kind: "event", sessionID: staleSessionID},
			string(queuedStaleMessageID): {kind: "event", sessionID: staleSessionID},
			string(currentMessageID):     {kind: "response", sessionID: currentSessionID},
		},
	}

	if err := service.handleRouteAck(&relayv1.RouteAck{RoutedMessageId: staleMessageID, ErrorCode: "peer_offline"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := service.sessions[staleSessionID]; exists {
		t.Fatal("peer_offline ACK retained the stale Noise session")
	}
	if _, exists := service.sentControl[string(queuedStaleMessageID)]; exists {
		t.Fatal("peer_offline ACK retained queued deliveries for the stale session")
	}
	if service.sessions[currentSessionID] == nil || service.sentControl[string(currentMessageID)].sessionID != currentSessionID {
		t.Fatal("peer_offline ACK removed an unrelated current session")
	}
}

func TestRemoteEventKeyCoalescesStreamingButPreservesTerminalAndActionHints(t *testing.T) {
	base := protocol.Event{InstanceID: "agent-1", ConversationID: "thread-1"}
	streaming := base
	streaming.Event = "provider.item/agentMessage/delta"
	updated := base
	updated.Event = "provider.turn/diff/updated"
	completed := base
	completed.Event = "provider.turn/completed"
	action := base
	action.Event = "action.created"

	if remoteEventKey(streaming) != remoteEventKey(updated) {
		t.Fatal("ordinary invalidation events for one conversation were not coalesced")
	}
	if remoteEventKey(completed) == remoteEventKey(streaming) {
		t.Fatal("terminal notification was coalesced away with streaming updates")
	}
	if remoteEventKey(action) == remoteEventKey(streaming) || remoteEventKey(action) == remoteEventKey(completed) {
		t.Fatal("action notification was not preserved as a separate invalidation")
	}
}

func TestInstallSessionReplacesPreviousSessionForPairing(t *testing.T) {
	oldSessionID := uuid.NewString()
	newSessionID := uuid.NewString()
	pairingID := uuid.NewString()
	service := &Service{sessions: map[string]*sessionState{
		oldSessionID: {pairing: Pairing{ID: pairingID}},
	}}
	newSession := &sessionState{pairing: Pairing{ID: pairingID}}

	service.installSession(newSessionID, newSession)

	if _, exists := service.sessions[oldSessionID]; exists {
		t.Fatal("previous session was not removed")
	}
	if service.sessions[newSessionID] != newSession {
		t.Fatal("new session was not installed")
	}
}

func TestLowPriorityOverflowQueuesOneInvalidation(t *testing.T) {
	session := &sessionState{
		high:        make(chan outboundControl, 4),
		low:         make(chan outboundControl, 1),
		lowBytes:    sessionQueueBytes,
		invalidated: make(map[string]bool),
	}
	service := &Service{sessions: map[string]*sessionState{"session": session}}
	event := protocol.Event{Type: "event", Event: "conversation.delta", ConversationID: "thread-1", Payload: map[string]any{"delta": "x"}}
	if err := service.sendControl(session, event); err != nil {
		t.Fatal(err)
	}
	if err := service.sendControl(session, event); err != nil {
		t.Fatal(err)
	}
	if got := len(session.high); got != 1 {
		t.Fatalf("queued invalidations = %d, want 1", got)
	}
	queued := <-session.high
	if queued.event == nil || queued.event.Event != "conversation.invalidated" || queued.event.Payload != nil {
		t.Fatalf("unexpected overflow event: %#v", queued.event)
	}
}

func TestHighPriorityOverflowDropsSession(t *testing.T) {
	session := &sessionState{
		high:        make(chan outboundControl, 1),
		low:         make(chan outboundControl, 1),
		highBytes:   sessionQueueBytes,
		invalidated: make(map[string]bool),
	}
	service := &Service{sessions: map[string]*sessionState{"session": session}}
	err := service.sendControl(session, protocol.ControlResponse{Type: "response", ID: "request"})
	if err == nil {
		t.Fatal("high priority overflow was accepted")
	}
	if len(service.sessions) != 0 {
		t.Fatal("overflowed session was not dropped")
	}
}

func TestSlowSessionDoesNotBlockAnotherSession(t *testing.T) {
	newSession := func() *sessionState {
		return &sessionState{
			pairing:     Pairing{Scopes: []string{"conversation.read"}},
			high:        make(chan outboundControl, 4),
			low:         make(chan outboundControl, 4),
			invalidated: make(map[string]bool),
		}
	}
	slow := newSession()
	fast := newSession()
	slow.lowBytes = sessionQueueBytes
	service := &Service{sessions: map[string]*sessionState{"slow": slow, "fast": fast}}
	service.forwardEvent(protocol.Event{
		Type:           "event",
		Event:          "conversation.delta",
		InstanceID:     "agent-1",
		ConversationID: "thread-1",
		Payload:        map[string]any{"delta": "text"},
	})
	if len(slow.high) != 1 || len(slow.low) != 0 {
		t.Fatalf("slow session queues high=%d low=%d", len(slow.high), len(slow.low))
	}
	if len(fast.low) != 1 || len(fast.high) != 0 {
		t.Fatalf("fast session queues high=%d low=%d", len(fast.high), len(fast.low))
	}
	if len(service.sessions) != 2 {
		t.Fatal("low-priority pressure dropped a healthy session")
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for index := 0; index <= len(haystack)-len(needle); index++ {
		match := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
