package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	relayv1 "github.com/hurf/work-anywhere/desktop/internal/remote/gen/waw/relay/v1"
)

const remoteStateKey = "remote.state.v1"
const remoteEventCoalesceWindow = 100 * time.Millisecond

var defaultScopes = []string{"status.read", "conversation.read", "conversation.create", "turn.start", "turn.interrupt", "action.read", "action.resolve", "attachment.write", "catalog.read"}

type settingsStore interface {
	PutSetting(string, any) error
	GetSetting(string, any) (bool, error)
	DeleteSetting(string) error
}

type logSink interface {
	Event(string, map[string]any)
}

type Pairing struct {
	ID             string    `json:"id"`
	MobileDeviceID string    `json:"mobileDeviceId"`
	DisplayName    string    `json:"displayName"`
	MobileEd25519  []byte    `json:"mobileEd25519"`
	MobileX25519   []byte    `json:"mobileX25519"`
	CapabilityHash []byte    `json:"capabilityHash"`
	Scopes         []string  `json:"scopes"`
	CreatedAt      time.Time `json:"createdAt"`
}

type persistedState struct {
	Enabled        bool               `json:"enabled"`
	RelayURL       string             `json:"relayUrl"`
	DeviceID       string             `json:"deviceId"`
	Ed25519Private []byte             `json:"ed25519Private"`
	Ed25519Public  []byte             `json:"ed25519Public"`
	X25519Private  []byte             `json:"x25519Private"`
	X25519Public   []byte             `json:"x25519Public"`
	CapabilityHash []byte             `json:"capabilityHash"`
	Pairings       map[string]Pairing `json:"pairings"`
}

type PairAttempt struct {
	ID                string    `json:"id"`
	URI               string    `json:"uri,omitempty"`
	ExpiresAt         time.Time `json:"expiresAt"`
	MobileDeviceID    string    `json:"mobileDeviceId,omitempty"`
	MobileDisplayName string    `json:"mobileDisplayName,omitempty"`
	DecisionSubmitted bool      `json:"decisionSubmitted,omitempty"`
	MobileEd25519     []byte    `json:"-"`
	MobileX25519      []byte    `json:"-"`
	MobileCapability  []byte    `json:"-"`
	proofValid        bool
	secret            []byte
	grantedScopes     []string
	grantExpiresAtMS  int64
	grantSignature    []byte
	decisionSubmitted bool
	decisionMessageID []byte
}

type Status struct {
	Enabled      bool          `json:"enabled"`
	RelayURL     string        `json:"relayUrl,omitempty"`
	DeviceID     string        `json:"deviceId,omitempty"`
	Fingerprint  string        `json:"fingerprint,omitempty"`
	Connected    bool          `json:"connected"`
	LastError    string        `json:"lastError,omitempty"`
	Pairings     []Pairing     `json:"pairings"`
	PairAttempts []PairAttempt `json:"pairAttempts"`
}

type Service struct {
	store     settingsStore
	control   func(protocol.ControlRequest) protocol.ControlResponse
	subscribe func() (<-chan protocol.Event, func())
	log       logSink
	ctx       context.Context

	mu           sync.RWMutex
	state        persistedState
	attempts     map[string]*PairAttempt
	sessions     map[string]*sessionState
	conn         *websocket.Conn
	connDeviceID uuid.UUID
	connected    bool
	lastError    string
	wake         chan struct{}
	writeMu      sync.Mutex
	sentControl  map[string]sentControlDelivery
}

type sentControlDelivery struct {
	kind      string
	sessionID string
}

type sessionState struct {
	pairing Pairing
	secure  *secureSession
	writeMu sync.Mutex
}

func New(stateStore settingsStore, control func(protocol.ControlRequest) protocol.ControlResponse, subscribe func() (<-chan protocol.Event, func()), logger logSink) (*Service, error) {
	service := &Service{store: stateStore, control: control, subscribe: subscribe, log: logger, attempts: make(map[string]*PairAttempt), sessions: make(map[string]*sessionState), wake: make(chan struct{}, 1), sentControl: make(map[string]sentControlDelivery)}
	service.state.Pairings = make(map[string]Pairing)
	if found, err := stateStore.GetSetting(remoteStateKey, &service.state); err != nil {
		return nil, err
	} else if found && service.state.Pairings == nil {
		service.state.Pairings = make(map[string]Pairing)
	}
	return service, nil
}

func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	backoff := time.Second
	for {
		state := s.snapshotState()
		if !state.Enabled || state.RelayURL == "" || state.DeviceID == "" {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}
		err := s.connectOnce(ctx, state)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		s.lastError = errString(err)
		s.connected = false
		s.conn = nil
		s.connDeviceID = uuid.Nil
		s.sessions = make(map[string]*sessionState)
		s.sentControl = make(map[string]sentControlDelivery)
		s.mu.Unlock()
		if err != nil {
			s.log.Event("remote.disconnected", map[string]any{"error": err.Error()})
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
			backoff = time.Second
		case <-timer.C:
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

func (s *Service) Configure(relayURL string, enabled bool) error {
	normalized, err := normalizeRelayURL(relayURL)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state.RelayURL = normalized
	s.state.Enabled = enabled
	err = s.persistLocked()
	connection := s.conn
	s.mu.Unlock()
	if connection != nil {
		connection.CloseNow()
	}
	s.signalWake()
	return err
}

func (s *Service) Enroll(ctx context.Context, token, displayName string) error {
	if token == "" {
		return errors.New("enrollment token is required")
	}
	s.mu.Lock()
	if err := s.ensureIdentityLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	state := s.state
	s.mu.Unlock()
	if state.RelayURL == "" {
		return errors.New("configure relay URL before enrollment")
	}
	if displayName == "" {
		displayName, _ = os.Hostname()
	}
	body, _ := json.Marshal(map[string]string{"token": token, "displayName": displayName, "ed25519PublicKey": base64.StdEncoding.EncodeToString(state.Ed25519Public), "x25519PublicKey": base64.StdEncoding.EncodeToString(state.X25519Public), "capabilityHash": base64.StdEncoding.EncodeToString(state.CapabilityHash)})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, state.RelayURL+"/v1/enroll/daemon", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("relay enrollment rejected with status %d", response.StatusCode)
	}
	var result struct {
		DeviceID string `json:"deviceId"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || uuid.Validate(result.DeviceID) != nil {
		return errors.New("relay returned an invalid device ID")
	}
	s.mu.Lock()
	s.state.DeviceID = result.DeviceID
	s.state.Enabled = true
	err = s.persistLocked()
	connection := s.conn
	s.mu.Unlock()
	// Enrollment creates a new Relay device identity. Do not keep using a
	// WebSocket authenticated as the previous identity.
	if connection != nil {
		connection.CloseNow()
	}
	s.signalWake()
	return err
}

func (s *Service) StartPairing(ttl time.Duration) (PairAttempt, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl < 10*time.Second || ttl > 5*time.Minute {
		return PairAttempt{}, errors.New("pairing TTL must be between 10 seconds and 5 minutes")
	}
	s.mu.Lock()
	state := s.state
	connected := s.connected
	s.mu.Unlock()
	if !connected {
		return PairAttempt{}, errors.New("relay is not connected")
	}
	pairingID := uuid.New()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return PairAttempt{}, err
	}
	expires := time.Now().Add(ttl)
	computerName, _ := os.Hostname()
	qr := QRPayload{RelayURL: state.RelayURL, PairingID: pairingID.String(), Secret: base64.RawURLEncoding.EncodeToString(secret), DaemonDeviceID: state.DeviceID, DaemonEd25519: base64.RawURLEncoding.EncodeToString(state.Ed25519Public), DaemonX25519: base64.RawURLEncoding.EncodeToString(state.X25519Public), CapabilityHash: base64.RawURLEncoding.EncodeToString(state.CapabilityHash), ComputerName: computerName, ExpiresAtMS: expires.UnixMilli()}
	uri, err := qr.URI()
	if err != nil {
		return PairAttempt{}, err
	}
	attempt := &PairAttempt{ID: pairingID.String(), URI: uri, ExpiresAt: expires, secret: secret}
	s.mu.Lock()
	s.attempts[attempt.ID] = attempt
	s.mu.Unlock()
	err = s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_PairStart{PairStart: &relayv1.PairStart{PairingId: pairingID[:], ExpiresAtMs: expires.UnixMilli()}}})
	if err != nil {
		s.mu.Lock()
		if s.attempts[attempt.ID] == attempt {
			delete(s.attempts, attempt.ID)
			wipeAttempt(attempt)
		}
		s.mu.Unlock()
		return PairAttempt{}, err
	}
	go s.expireAttempt(attempt)
	return publicAttempt(attempt), nil
}

func (s *Service) ResolvePairing(id string, approved bool, scopes []string) error {
	s.mu.Lock()
	attempt := s.attempts[id]
	state := s.state
	if attempt == nil || time.Now().After(attempt.ExpiresAt) || !attempt.proofValid || attempt.MobileDeviceID == "" {
		s.mu.Unlock()
		return errors.New("pairing request is not ready or has expired")
	}
	if attempt.decisionSubmitted {
		s.mu.Unlock()
		return errors.New("pairing decision was already submitted")
	}
	pairingID, _ := uuid.Parse(id)
	mobileID, _ := uuid.Parse(attempt.MobileDeviceID)
	decision := &relayv1.PairDecision{PairingId: pairingID[:], Approved: approved, MobileDeviceId: mobileID[:], Reason: "rejected_by_user"}
	if approved {
		if len(scopes) == 0 {
			scopes = append([]string(nil), defaultScopes...)
		}
		if err := validateScopes(scopes); err != nil {
			s.mu.Unlock()
			return err
		}
		decision.Scopes = append([]string(nil), scopes...)
		decision.GrantExpiresAtMs = attempt.ExpiresAt.UnixMilli()
		daemonID, _ := uuid.Parse(state.DeviceID)
		input := pairGrantSignatureInput(pairingID[:], daemonID[:], mobileID[:], attempt.MobileEd25519, attempt.MobileX25519, scopes, decision.GrantExpiresAtMs)
		decision.GrantSignature = ed25519.Sign(ed25519.PrivateKey(state.Ed25519Private), input)
		attempt.grantedScopes = append([]string(nil), scopes...)
		attempt.grantExpiresAtMS = decision.GrantExpiresAtMs
		attempt.grantSignature = append([]byte(nil), decision.GrantSignature...)
	}
	attempt.decisionSubmitted = true
	messageID := newMessageID()
	attempt.decisionMessageID = append(attempt.decisionMessageID[:0], messageID...)
	s.mu.Unlock()
	err := s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: messageID, Body: &relayv1.ClientFrame_PairDecision{PairDecision: decision}})
	if err != nil {
		s.mu.Lock()
		if s.attempts[id] == attempt {
			attempt.decisionSubmitted = false
			attempt.decisionMessageID = nil
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Service) RevokePairing(id string) error {
	pairingID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	s.mu.RLock()
	_, exists := s.state.Pairings[id]
	s.mu.RUnlock()
	if !exists {
		return errors.New("pairing not found")
	}
	return s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_PairRevoke{PairRevoke: &relayv1.PairRevoke{PairingId: pairingID[:]}}})
}

func (s *Service) CancelPairing(id string) error {
	pairingID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	attempt := s.attempts[id]
	if attempt != nil {
		if attempt.decisionSubmitted {
			s.mu.Unlock()
			return errors.New("pairing decision was already submitted")
		}
		wipeAttempt(attempt)
		delete(s.attempts, id)
	}
	s.mu.Unlock()
	if attempt == nil {
		return errors.New("pairing attempt not found")
	}
	return s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_PairCancel{PairCancel: &relayv1.PairCancel{PairingId: pairingID[:]}}})
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := Status{Enabled: s.state.Enabled, RelayURL: s.state.RelayURL, DeviceID: s.state.DeviceID, Connected: s.connected, LastError: s.lastError}
	if len(s.state.Ed25519Public) > 0 {
		digest := sha256.Sum256(s.state.Ed25519Public)
		status.Fingerprint = base64.RawURLEncoding.EncodeToString(digest[:12])
	}
	for _, pairing := range s.state.Pairings {
		pairing.MobileEd25519, pairing.MobileX25519, pairing.CapabilityHash = nil, nil, nil
		status.Pairings = append(status.Pairings, pairing)
	}
	for _, attempt := range s.attempts {
		status.PairAttempts = append(status.PairAttempts, publicAttempt(attempt))
	}
	sort.Slice(status.Pairings, func(i, j int) bool { return status.Pairings[i].CreatedAt.Before(status.Pairings[j].CreatedAt) })
	sort.Slice(status.PairAttempts, func(i, j int) bool { return status.PairAttempts[i].ExpiresAt.Before(status.PairAttempts[j].ExpiresAt) })
	return status
}

func (s *Service) Reconnect() {
	s.mu.RLock()
	connection := s.conn
	s.mu.RUnlock()
	if connection != nil {
		connection.CloseNow()
	}
	s.signalWake()
}

func (s *Service) connectOnce(ctx context.Context, state persistedState) error {
	endpoint, err := websocketURL(state.RelayURL, "/v1/connect")
	if err != nil {
		return err
	}
	connection, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return err
	}
	defer connection.CloseNow()
	connection.SetReadLimit(2 << 20)
	challengeFrame, err := readServerFrame(ctx, connection)
	if err != nil || challengeFrame.GetServerChallenge() == nil {
		return errors.New("relay did not provide an authentication challenge")
	}
	challenge := challengeFrame.GetServerChallenge()
	deviceID, _ := uuid.Parse(state.DeviceID)
	signature := ed25519.Sign(ed25519.PrivateKey(state.Ed25519Private), authSignatureInput(challenge.ConnectionId, challenge.Challenge, challenge.ExpiresAtMs, state.CapabilityHash))
	auth := &relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_ClientAuth{ClientAuth: &relayv1.ClientAuth{DeviceId: deviceID[:], Role: relayv1.DeviceRole_DEVICE_ROLE_DAEMON, CapabilityHash: state.CapabilityHash, Signature: signature}}}
	if err = writeClientFrame(ctx, connection, auth); err != nil {
		return err
	}
	response, err := readServerFrame(ctx, connection)
	if err != nil || response.GetAuthOk() == nil {
		return errors.New("relay device authentication failed")
	}
	s.mu.Lock()
	s.conn, s.connDeviceID, s.connected, s.lastError = connection, deviceID, true, ""
	s.mu.Unlock()
	s.log.Event("remote.connected", map[string]any{"relay": state.RelayURL, "deviceId": state.DeviceID})
	events, unsubscribe := s.subscribe()
	defer unsubscribe()
	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	go s.forwardEvents(eventCtx, events)
	for {
		frame, readErr := readServerFrame(ctx, connection)
		if readErr != nil {
			return readErr
		}
		if err = s.handleFrame(frame); err != nil {
			s.log.Event("remote.frame_rejected", map[string]any{"error": err.Error()})
		}
	}
}

func (s *Service) handleFrame(frame *relayv1.ServerFrame) error {
	if frame.WireMajor != wireMajor || len(frame.MessageId) != messageIDBytes {
		return errors.New("unsupported Relay frame")
	}
	switch {
	case frame.GetRouteAck() != nil:
		return s.handleRouteAck(frame.GetRouteAck())
	case frame.GetPairHello() != nil:
		return s.handlePairHello(frame.GetPairHello())
	case frame.GetPairResult() != nil:
		return s.handlePairResult(frame.GetPairResult())
	case frame.GetPairRevoked() != nil:
		return s.handlePairRevoked(frame.GetPairRevoked())
	case frame.GetSessionOpen() != nil:
		return s.handleSessionOpen(frame.GetSessionOpen())
	case frame.GetSessionData() != nil:
		return s.handleSessionData(frame.GetSessionData())
	case frame.GetSessionClose() != nil:
		s.mu.Lock()
		delete(s.sessions, string(frame.GetSessionClose().SessionId))
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) handleRouteAck(ack *relayv1.RouteAck) error {
	s.mu.Lock()
	delivery, tracked := s.sentControl[string(ack.RoutedMessageId)]
	delete(s.sentControl, string(ack.RoutedMessageId))
	if ack.Accepted {
		s.mu.Unlock()
		return nil
	}
	for _, attempt := range s.attempts {
		if same(attempt.decisionMessageID, ack.RoutedMessageId) {
			attempt.decisionSubmitted = false
			attempt.decisionMessageID = nil
			attempt.grantedScopes = nil
			attempt.grantExpiresAtMS = 0
			attempt.grantSignature = nil
			s.lastError = "Relay rejected pairing decision: " + ack.ErrorCode
			s.mu.Unlock()
			return nil
		}
	}
	if tracked && ack.ErrorCode == "peer_offline" {
		delete(s.sessions, delivery.sessionID)
		// Once Relay says the Mobile peer is gone, every already queued event
		// for this Noise session is stale too. Forget those correlations so a
		// streaming turn cannot produce an ACK/log storm that disconnects the
		// daemon itself.
		for messageID, pending := range s.sentControl {
			if pending.sessionID == delivery.sessionID {
				delete(s.sentControl, messageID)
			}
		}
	}
	s.mu.Unlock()
	if tracked {
		s.log.Event("remote.delivery_rejected", map[string]any{"kind": delivery.kind, "errorCode": ack.ErrorCode})
	}
	return nil
}

func (s *Service) handlePairHello(hello *relayv1.PairHello) error {
	id, err := uuid.FromBytes(hello.PairingId)
	if err != nil {
		return err
	}
	s.mu.Lock()
	attempt := s.attempts[id.String()]
	state := s.state
	if attempt == nil || time.Now().After(attempt.ExpiresAt) {
		s.mu.Unlock()
		return errors.New("unknown or expired pairing attempt")
	}
	daemonID, _ := uuid.Parse(state.DeviceID)
	input := pairProofInput(hello.PairingId, hello.Challenge, daemonID[:], state.Ed25519Public, state.X25519Public, hello.MobileEd25519PublicKey, hello.MobileX25519PublicKey, state.CapabilityHash, hello.CapabilityHash, attempt.ExpiresAt.UnixMilli())
	valid := len(hello.CandidateMobileDeviceId) == 16 && len(hello.MobileEd25519PublicKey) == 32 && len(hello.MobileX25519PublicKey) == 32 && verifyPairProof(attempt.secret, hello.Proof, input)
	if !valid {
		s.mu.Unlock()
		return s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_PairDecision{PairDecision: &relayv1.PairDecision{PairingId: hello.PairingId, Approved: false, Reason: "invalid_pairing_proof"}}})
	}
	mobileID, _ := uuid.FromBytes(hello.CandidateMobileDeviceId)
	attempt.MobileDeviceID = mobileID.String()
	attempt.MobileDisplayName = hello.DisplayName
	attempt.MobileEd25519 = append([]byte(nil), hello.MobileEd25519PublicKey...)
	attempt.MobileX25519 = append([]byte(nil), hello.MobileX25519PublicKey...)
	attempt.MobileCapability = append([]byte(nil), hello.CapabilityHash...)
	attempt.proofValid = true
	s.mu.Unlock()
	return nil
}

func (s *Service) handlePairResult(result *relayv1.PairResult) error {
	id, err := uuid.FromBytes(result.PairingId)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.attempts[id.String()]
	if attempt == nil {
		return nil
	}
	defer delete(s.attempts, id.String())
	if !result.Approved {
		wipeAttempt(attempt)
		return nil
	}
	if result.GrantExpiresAtMs != attempt.grantExpiresAtMS || !same(result.GrantSignature, attempt.grantSignature) || !equalStrings(result.Scopes, attempt.grantedScopes) {
		wipeAttempt(attempt)
		return errors.New("Relay returned a pairing grant that does not match the approved grant")
	}
	pairing := Pairing{ID: id.String(), MobileDeviceID: attempt.MobileDeviceID, DisplayName: attempt.MobileDisplayName, MobileEd25519: append([]byte(nil), attempt.MobileEd25519...), MobileX25519: append([]byte(nil), attempt.MobileX25519...), CapabilityHash: append([]byte(nil), attempt.MobileCapability...), Scopes: append([]string(nil), result.Scopes...), CreatedAt: time.Now()}
	wipeAttempt(attempt)
	s.state.Pairings[pairing.ID] = pairing
	return s.persistLocked()
}

func (s *Service) handlePairRevoked(revoked *relayv1.PairRevoked) error {
	id, err := uuid.FromBytes(revoked.PairingId)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.state.Pairings, id.String())
	for sessionID, session := range s.sessions {
		if session.pairing.ID == id.String() {
			delete(s.sessions, sessionID)
		}
	}
	err = s.persistLocked()
	s.mu.Unlock()
	return err
}

func (s *Service) handleSessionOpen(open *relayv1.SessionOpen) error {
	pairingID, err := uuid.FromBytes(open.PairingId)
	if err != nil || len(open.SessionId) != 16 {
		return errors.New("invalid session open")
	}
	s.mu.RLock()
	pairing, ok := s.state.Pairings[pairingID.String()]
	state := s.state
	s.mu.RUnlock()
	if !ok || pairing.MobileDeviceID == "" {
		return errors.New("session pairing is not authorized")
	}
	daemonID, _ := uuid.Parse(state.DeviceID)
	mobileID, _ := uuid.Parse(pairing.MobileDeviceID)
	if !same(open.RecipientDeviceId, daemonID[:]) || !same(open.SenderDeviceId, mobileID[:]) {
		return errors.New("session endpoint mismatch")
	}
	binding := noiseBinding{PairingID: open.PairingId, DaemonID: daemonID[:], MobileID: mobileID[:], SessionID: open.SessionId, DaemonCapability: state.CapabilityHash, MobileCapability: pairing.CapabilityHash}
	handshake, err := newResponderHandshake(state.X25519Private, state.X25519Public, pairing.MobileX25519, binding, rand.Reader)
	if err != nil {
		return err
	}
	if _, _, _, err = handshake.ReadMessage(nil, open.Handshake); err != nil {
		return err
	}
	message, first, second, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		return err
	}
	if err = s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_SessionHandshake{SessionHandshake: &relayv1.SessionHandshake{PairingId: open.PairingId, SessionId: open.SessionId, RecipientDeviceId: mobileID[:], Handshake: message}}}); err != nil {
		return err
	}
	// Install only after the handshake frame has been queued. This prevents a
	// concurrent event from reaching the new socket before Mobile can finish
	// establishing the new Noise session.
	s.installSession(string(open.SessionId), &sessionState{pairing: pairing, secure: newResponderSession(first, second, binding)})
	return nil
}

// installSession replaces a previous session for the same pairing. Relay
// registers one live connection per device, so keeping an old session would
// allow its asynchronous events to be routed to the replacement connection.
func (s *Service) installSession(sessionID string, session *sessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingID, existing := range s.sessions {
		if existingID != sessionID && existing.pairing.ID == session.pairing.ID {
			delete(s.sessions, existingID)
		}
	}
	s.sessions[sessionID] = session
}

func (s *Service) handleSessionData(data *relayv1.SessionData) error {
	s.mu.RLock()
	session := s.sessions[string(data.SessionId)]
	s.mu.RUnlock()
	if session == nil || !same(data.PairingId, mustUUIDBytes(session.pairing.ID)) {
		return errors.New("unknown secure session")
	}
	payload, err := session.secure.decrypt(data.Ciphertext)
	if err != nil || payload.Sequence != data.SenderSequence {
		s.mu.Lock()
		delete(s.sessions, string(data.SessionId))
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("outer and inner sequence mismatch")
	}
	var request protocol.ControlRequest
	if json.Unmarshal(payload.Body, &request) != nil || request.Type != "request" || request.ID == "" {
		s.closeSession(data.SessionId)
		return errors.New("invalid remote control request")
	}
	if !scopeAllows(session.pairing.Scopes, request.Method) {
		return s.sendControl(session, protocol.ControlResponse{Type: "response", ID: request.ID, Contract: request.Contract, Error: &protocol.ControlError{Code: "forbidden", Message: "pairing scope does not allow method"}})
	}
	response := s.control(request)
	if request.Method == "system.status" && response.Error == nil {
		if result, ok := response.Result.(map[string]any); ok {
			redacted := make(map[string]any, len(result))
			for key, value := range result {
				if key != "remote" {
					redacted[key] = value
				}
			}
			response.Result = redacted
		}
	}
	return s.sendControl(session, response)
}

func (s *Service) sendControl(session *sessionState, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Encrypting advances the Noise send sequence. Keep encryption and the
	// corresponding Relay write together so concurrent responses/events cannot
	// put a later sequence number on the wire first.
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	ciphertext, sequence, err := session.secure.encrypt(body, time.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	pairingID, _ := uuid.Parse(session.pairing.ID)
	mobileID, _ := uuid.Parse(session.pairing.MobileDeviceID)
	sessionID := session.secure.binding.SessionID
	messageID := newMessageID()
	kind := "response"
	if _, ok := value.(protocol.Event); ok {
		kind = "event"
	}
	s.mu.Lock()
	if s.sentControl == nil {
		s.sentControl = make(map[string]sentControlDelivery)
	}
	s.sentControl[string(messageID)] = sentControlDelivery{kind: kind, sessionID: string(sessionID)}
	s.mu.Unlock()
	err = s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: messageID, Body: &relayv1.ClientFrame_SessionData{SessionData: &relayv1.SessionData{PairingId: pairingID[:], SessionId: sessionID, RecipientDeviceId: mobileID[:], SenderSequence: sequence, TrafficClass: relayv1.TrafficClass_TRAFFIC_CLASS_CONTROL, CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli(), Ciphertext: ciphertext}}})
	if err != nil {
		s.mu.Lock()
		delete(s.sentControl, string(messageID))
		s.mu.Unlock()
	}
	return err
}

func (s *Service) closeSession(sessionID []byte) {
	s.mu.Lock()
	delete(s.sessions, string(sessionID))
	s.mu.Unlock()
}

func (s *Service) forwardEvents(ctx context.Context, events <-chan protocol.Event) {
	pending := make(map[string]protocol.Event)
	var timer *time.Timer
	var timerC <-chan time.Time
	flush := func() {
		for _, event := range pending {
			s.forwardEvent(event)
		}
		clear(pending)
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-events:
			if !ok {
				if timer != nil {
					timer.Stop()
				}
				return
			}
			event.Contract = protocol.MobileControlContract
			// Events are invalidation hints for Mobile. The next bounded snapshot
			// carries the authoritative timeline, so never send provider payloads
			// (which can contain large command output/diffs) over the relay event
			// path.
			event.Payload = nil
			pending[remoteEventKey(event)] = event
			if timer == nil {
				timer = time.NewTimer(remoteEventCoalesceWindow)
				timerC = timer.C
			}
		case <-timerC:
			flush()
			timer = nil
			timerC = nil
		}
	}
}

func remoteEventKey(event protocol.Event) string {
	lower := strings.ToLower(event.Event)
	class := "conversation"
	if strings.Contains(lower, "action") {
		class = "action"
	} else if strings.Contains(lower, "complete") || strings.Contains(lower, "failed") || strings.Contains(lower, "interrupt") {
		class = "terminal"
	}
	return event.InstanceID + "\x00" + event.ConversationID + "\x00" + class
}

func (s *Service) forwardEvent(event protocol.Event) {
	s.mu.RLock()
	sessions := make([]*sessionState, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.RUnlock()
	for _, session := range sessions {
		method := "conversation.subscribe"
		if strings.Contains(strings.ToLower(event.Event), "action") {
			method = "action.list"
		}
		if scopeAllows(session.pairing.Scopes, method) {
			_ = s.sendControl(session, event)
			s.sendNotificationHint(session.pairing, event)
		}
	}
}

func (s *Service) sendNotificationHint(pairing Pairing, event protocol.Event) {
	hint := relayv1.NotificationHint_NOTIFICATION_HINT_UNSPECIFIED
	lower := strings.ToLower(event.Event)
	if strings.Contains(lower, "action") && (strings.Contains(lower, "request") || strings.Contains(lower, "pending")) {
		hint = relayv1.NotificationHint_NOTIFICATION_HINT_ACTION_REQUIRED
	} else if strings.Contains(lower, "complete") || strings.Contains(lower, "failed") || strings.Contains(lower, "interrupt") {
		hint = relayv1.NotificationHint_NOTIFICATION_HINT_TURN_COMPLETED
	}
	if hint == relayv1.NotificationHint_NOTIFICATION_HINT_UNSPECIFIED {
		return
	}
	pairingID, _ := uuid.Parse(pairing.ID)
	mobileID, _ := uuid.Parse(pairing.MobileDeviceID)
	collapseDigest := sha256.Sum256(append(pairingID[:], byte(hint)))
	_ = s.send(&relayv1.ClientFrame{WireMajor: wireMajor, WireMinor: wireMinor, MessageId: newMessageID(), Body: &relayv1.ClientFrame_Notify{Notify: &relayv1.Notify{PairingId: pairingID[:], RecipientDeviceId: mobileID[:], NotificationId: newMessageID(), Hint: hint, CollapseKey: base64.RawURLEncoding.EncodeToString(collapseDigest[:12]), ExpiresAtMs: time.Now().Add(15 * time.Minute).UnixMilli()}}})
}

func (s *Service) send(frame *relayv1.ClientFrame) error {
	s.mu.RLock()
	connection := s.conn
	stateDeviceID := s.state.DeviceID
	connectionDeviceID := s.connDeviceID
	ctx := s.ctx
	s.mu.RUnlock()
	if connection == nil || ctx == nil {
		return errors.New("relay is not connected")
	}
	if stateDeviceID == "" || connectionDeviceID == uuid.Nil || stateDeviceID != connectionDeviceID.String() {
		return errors.New("relay identity changed; reconnect is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return writeClientFrame(writeCtx, connection, frame)
}

func (s *Service) ensureIdentityLocked() error {
	if len(s.state.Ed25519Private) == ed25519.PrivateKeySize && len(s.state.X25519Private) == 32 {
		return nil
	}
	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	xKey, err := noiseSuite.GenerateKeypair(rand.Reader)
	if err != nil {
		return err
	}
	capability := sha256.Sum256([]byte(strings.Join(defaultScopes, "\n") + "\ndesktop-control-json-v1"))
	s.state.Ed25519Private, s.state.Ed25519Public = edPrivate, edPublic
	s.state.X25519Private, s.state.X25519Public = xKey.Private, xKey.Public
	s.state.CapabilityHash = capability[:]
	if s.state.Pairings == nil {
		s.state.Pairings = make(map[string]Pairing)
	}
	return s.persistLocked()
}

func (s *Service) persistLocked() error { return s.store.PutSetting(remoteStateKey, s.state) }

func (s *Service) snapshotState() persistedState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func scopeAllows(scopes []string, method string) bool {
	required := map[string]string{
		"system.status": "status.read", "instance.list": "status.read", "instance.get": "status.read", "agent.list": "status.read",
		"project.list":      "conversation.read",
		"conversation.list": "conversation.read", "conversation.read": "conversation.read", "conversation.timeline.list": "conversation.read", "conversation.content.read": "conversation.read", "conversation.content.resolve": "conversation.read", "conversation.subscribe": "conversation.read",
		"conversation.create": "conversation.create",
		"turn.start":          "turn.start", "plan.respond": "turn.start", "conversation.settings.update": "turn.start",
		"conversation.queue.add": "turn.start", "conversation.queue.update": "turn.start", "conversation.queue.delete": "turn.start", "conversation.queue.start": "turn.start", "turn.interrupt": "turn.interrupt",
		"action.list": "action.read", "action.resolve": "action.resolve",
		"attachment.begin": "attachment.write", "attachment.chunk": "attachment.write", "attachment.commit": "attachment.write", "attachment.abort": "attachment.write",
		"catalog.models": "catalog.read", "catalog.permissionProfiles": "catalog.read",
	}[method]
	if required == "" {
		return false
	}
	for _, scope := range scopes {
		if scope == required || (method == "conversation.create" && scope == "turn.start") {
			return true
		}
	}
	return false
}

func validateScopes(scopes []string) error {
	allowed := make(map[string]bool, len(defaultScopes))
	for _, scope := range defaultScopes {
		allowed[scope] = true
	}
	seen := make(map[string]bool)
	for _, scope := range scopes {
		if !allowed[scope] || seen[scope] {
			return errors.New("invalid or duplicate remote scope")
		}
		seen[scope] = true
	}
	if len(seen) == 0 {
		return errors.New("at least one remote scope is required")
	}
	return nil
}

func equalStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	left, right := append([]string(nil), first...), append([]string(nil), second...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func normalizeRelayURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("relay URL must be an origin without path, query, or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("relay URL must use HTTP or HTTPS")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func websocketURL(origin, path string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = path
	return parsed.String(), nil
}

func readServerFrame(ctx context.Context, connection *websocket.Conn) (*relayv1.ServerFrame, error) {
	kind, raw, err := connection.Read(ctx)
	if err != nil || kind != websocket.MessageBinary {
		return nil, errors.New("relay binary frame read failed")
	}
	var frame relayv1.ServerFrame
	if err = proto.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

func writeClientFrame(ctx context.Context, connection *websocket.Conn, frame *relayv1.ClientFrame) error {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageBinary, raw)
}

func newMessageID() []byte {
	value := make([]byte, messageIDBytes)
	_, _ = rand.Read(value)
	return value
}

func publicAttempt(attempt *PairAttempt) PairAttempt {
	return PairAttempt{ID: attempt.ID, URI: attempt.URI, ExpiresAt: attempt.ExpiresAt, MobileDeviceID: attempt.MobileDeviceID, MobileDisplayName: attempt.MobileDisplayName, DecisionSubmitted: attempt.decisionSubmitted}
}

func (s *Service) expireAttempt(attempt *PairAttempt) {
	timer := time.NewTimer(time.Until(attempt.ExpiresAt))
	defer timer.Stop()
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()
	if ctx == nil {
		<-timer.C
	} else {
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	s.mu.Lock()
	if s.attempts[attempt.ID] == attempt {
		wipeSecret(attempt)
		if attempt.decisionSubmitted && (ctx == nil || ctx.Err() == nil) {
			s.mu.Unlock()
			time.AfterFunc(30*time.Second, func() { s.removeAttempt(attempt) })
			return
		}
		delete(s.attempts, attempt.ID)
		wipeAttemptMetadata(attempt)
	}
	s.mu.Unlock()
}

func wipeAttempt(attempt *PairAttempt) {
	wipeSecret(attempt)
	wipeAttemptMetadata(attempt)
}

func wipeSecret(attempt *PairAttempt) {
	for index := range attempt.secret {
		attempt.secret[index] = 0
	}
	attempt.secret = nil
}

func wipeAttemptMetadata(attempt *PairAttempt) {
	attempt.MobileEd25519 = nil
	attempt.MobileX25519 = nil
	attempt.MobileCapability = nil
	attempt.grantSignature = nil
}

func (s *Service) removeAttempt(attempt *PairAttempt) {
	s.mu.Lock()
	if s.attempts[attempt.ID] == attempt {
		delete(s.attempts, attempt.ID)
		wipeAttempt(attempt)
	}
	s.mu.Unlock()
}

func mustUUIDBytes(value string) []byte {
	id, _ := uuid.Parse(value)
	return id[:]
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
