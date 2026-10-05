package devmobile

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
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/pkg/endpoint/e2ee"
	waProtocol "github.com/vual/work-anywhere/relay/pkg/endpoint/protocol"
)

type QRPayload struct {
	RelayURL       string `json:"relayUrl"`
	PairingID      string `json:"pairingId"`
	Secret         string `json:"secret"`
	DaemonDeviceID string `json:"daemonDeviceId"`
	DaemonEd25519  string `json:"daemonEd25519"`
	DaemonX25519   string `json:"daemonX25519"`
	CapabilityHash string `json:"capabilityHash"`
	ComputerName   string `json:"computerName,omitempty"`
	ExpiresAtMS    int64  `json:"expiresAtMs"`
}

type Client struct {
	mu             sync.Mutex
	eventMu        sync.Mutex
	conn           *websocket.Conn
	writeMu        sync.Mutex
	reconnectMu    sync.Mutex
	secure         *e2ee.Session
	pairingID      uuid.UUID
	deviceID       uuid.UUID
	daemonID       uuid.UUID
	sessionID      []byte
	scopes         []string
	pending        map[string]chan json.RawMessage
	pendingRoutes  map[string]chan routeResult
	events         []json.RawMessage
	eventBytes     int
	eventOverflow  bool
	eventSink      func(string)
	lastError      string
	connected      bool
	cancel         context.CancelFunc
	credential     CredentialSet
	connectUntil   time.Time
	connectCode    string
	connectBackoff time.Duration
}

type routeResult struct {
	accepted bool
	code     string
}

type CallError struct {
	Code    string
	Message string
}

const sessionHandshakeTimeout = 30 * time.Second

func (e *CallError) Error() string { return e.Message }

type Status struct {
	Connected    bool              `json:"connected"`
	PairingID    string            `json:"pairingId,omitempty"`
	DeviceID     string            `json:"deviceId,omitempty"`
	DaemonID     string            `json:"daemonId,omitempty"`
	ComputerName string            `json:"computerName,omitempty"`
	Scopes       []string          `json:"scopes,omitempty"`
	LastError    string            `json:"lastError,omitempty"`
	Events       []json.RawMessage `json:"events"`
}

type CredentialSet struct {
	RelayURL         string   `json:"relayUrl"`
	PairingID        string   `json:"pairingId"`
	DaemonDeviceID   string   `json:"daemonDeviceId"`
	ComputerName     string   `json:"computerName,omitempty"`
	MobileDeviceID   string   `json:"mobileDeviceId"`
	Ed25519Private   []byte   `json:"ed25519Private"`
	X25519Private    []byte   `json:"x25519Private"`
	X25519Public     []byte   `json:"x25519Public"`
	DaemonX25519     []byte   `json:"daemonX25519"`
	DaemonCapability []byte   `json:"daemonCapability"`
	MobileCapability []byte   `json:"mobileCapability"`
	Scopes           []string `json:"scopes"`
}

func New() *Client {
	return &Client{pending: make(map[string]chan json.RawMessage), pendingRoutes: make(map[string]chan routeResult)}
}

const (
	maxBufferedEvents     = 256
	maxBufferedEventBytes = 2 << 20
)

// SetEventSink registers a callback for unsolicited control events received
// from the daemon. The callback is invoked outside the client's mutex.
func (c *Client) SetEventSink(sink func(string)) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.mu.Lock()
	c.eventSink = sink
	pending := append([]json.RawMessage(nil), c.events...)
	overflow := c.eventOverflow
	if sink != nil {
		c.events = nil
		c.eventBytes = 0
		c.eventOverflow = false
	}
	connected := c.connected
	c.mu.Unlock()
	if sink == nil {
		return
	}
	state := "disconnected"
	if connected {
		state = "online"
	}
	sink(transportEnvelope(state, ""))
	if overflow {
		sink(`{"type":"event","event":"conversation.invalidated"}`)
	}
	for _, event := range pending {
		sink(string(event))
	}
}

func transportEnvelope(state, errorCode string) string {
	value := map[string]any{"type": "transport", "state": state}
	if errorCode != "" {
		value["errorCode"] = errorCode
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (c *Client) emitTransport(state, errorCode string) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.mu.Lock()
	sink := c.eventSink
	c.mu.Unlock()
	if sink != nil {
		sink(transportEnvelope(state, errorCode))
	}
}

func (c *Client) dispatchControlPayload(body json.RawMessage) {
	var envelope struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return
	}
	if envelope.Type == "response" {
		c.mu.Lock()
		waiter := c.pending[envelope.ID]
		if waiter != nil {
			delete(c.pending, envelope.ID)
			waiter <- append(json.RawMessage(nil), body...)
		}
		c.mu.Unlock()
		return
	}
	if envelope.Type != "event" {
		return
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.mu.Lock()
	sink := c.eventSink
	if sink == nil {
		copyOfBody := append(json.RawMessage(nil), body...)
		c.events = append(c.events, copyOfBody)
		c.eventBytes += len(copyOfBody)
		for len(c.events) > maxBufferedEvents || c.eventBytes > maxBufferedEventBytes {
			c.eventBytes -= len(c.events[0])
			c.events = c.events[1:]
			c.eventOverflow = true
		}
	}
	c.mu.Unlock()
	if sink != nil {
		sink(string(body))
	}
}

func (c *Client) Pair(ctx context.Context, uri string) error {
	qr, secret, err := parseQR(uri)
	if err != nil {
		return err
	}
	defer func() {
		for index := range secret {
			secret[index] = 0
		}
	}()
	if qr.ExpiresAtMS <= time.Now().UnixMilli() {
		return errors.New("pairing QR has expired")
	}
	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	xKey, err := e2ee.GenerateStaticKey(rand.Reader)
	if err != nil {
		return err
	}
	mobileCapability := sha256.Sum256([]byte("work-anywhere/dev-mobile/desktop-control-json-v1"))
	pairingID, _ := uuid.Parse(qr.PairingID)
	daemonID, _ := uuid.Parse(qr.DaemonDeviceID)
	daemonEd, _ := base64.RawURLEncoding.DecodeString(qr.DaemonEd25519)
	daemonX, _ := base64.RawURLEncoding.DecodeString(qr.DaemonX25519)
	daemonCapability, _ := base64.RawURLEncoding.DecodeString(qr.CapabilityHash)
	pairURL, err := websocketEndpoint(qr.RelayURL, "/v1/pair")
	if err != nil {
		return err
	}
	pairConn, _, err := websocket.Dial(ctx, pairURL, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return err
	}
	challengeFrame, err := readFrame(ctx, pairConn)
	if err != nil || challengeFrame.GetServerChallenge() == nil {
		pairConn.CloseNow()
		return errors.New("pair endpoint did not provide a challenge")
	}
	challenge := challengeFrame.GetServerChallenge()
	proofInput := waProtocol.PairProofInput(pairingID[:], challenge.Challenge, daemonID[:], daemonEd, daemonX, edPublic, xKey.Public, daemonCapability, mobileCapability[:], qr.ExpiresAtMS)
	join := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: newID(), Body: &relayv1.ClientFrame_PairJoin{PairJoin: &relayv1.PairJoin{PairingId: pairingID[:], MobileEd25519PublicKey: edPublic, MobileX25519PublicKey: xKey.Public, DisplayName: "Development Mobile", Proof: waProtocol.PairProof(secret, proofInput), CapabilityHash: mobileCapability[:]}}}
	if err = writeFrame(ctx, pairConn, join); err != nil {
		pairConn.CloseNow()
		return err
	}
	resultFrame, err := readFrame(ctx, pairConn)
	pairConn.CloseNow()
	if err != nil || resultFrame.GetPairResult() == nil || !resultFrame.GetPairResult().Approved {
		return errors.New("pairing was rejected or timed out")
	}
	result := resultFrame.GetPairResult()
	mobileID, err := uuid.FromBytes(result.MobileDeviceId)
	if err != nil || !bytes.Equal(result.DaemonDeviceId, daemonID[:]) || !bytes.Equal(result.DaemonEd25519PublicKey, daemonEd) || !bytes.Equal(result.DaemonX25519PublicKey, daemonX) {
		return errors.New("pairing result does not match QR identity")
	}
	grantInput := waProtocol.PairGrantSignatureInput(pairingID[:], daemonID[:], mobileID[:], edPublic, xKey.Public, result.Scopes, result.GrantExpiresAtMs)
	if result.GrantExpiresAtMs < time.Now().UnixMilli() || !ed25519.Verify(ed25519.PublicKey(daemonEd), grantInput, result.GrantSignature) {
		return errors.New("pairing grant signature is invalid")
	}
	credential := CredentialSet{RelayURL: qr.RelayURL, PairingID: pairingID.String(), DaemonDeviceID: daemonID.String(), ComputerName: qr.ComputerName, MobileDeviceID: mobileID.String(), Ed25519Private: append([]byte(nil), edPrivate...), X25519Private: append([]byte(nil), xKey.Private...), X25519Public: append([]byte(nil), xKey.Public...), DaemonX25519: append([]byte(nil), daemonX...), DaemonCapability: append([]byte(nil), daemonCapability...), MobileCapability: append([]byte(nil), mobileCapability[:]...), Scopes: append([]string(nil), result.Scopes...)}
	c.storeCredential(credential, pairingID, daemonID, mobileID)
	defer wipeCredential(&credential)
	return c.reconnect(ctx)
}

func (c *Client) ExportCredentials() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.credential.PairingID == "" {
		return nil, errors.New("client has no pairing credentials")
	}
	return json.Marshal(c.credential)
}

// LoadCredentials validates and installs a credential without opening a
// network connection. It is used by the multi-pairing Mobile registry to
// restore inactive pairings without creating extra long-lived sockets.
func (c *Client) LoadCredentials(raw []byte) error {
	var credential CredentialSet
	if err := json.Unmarshal(raw, &credential); err != nil {
		return errors.New("invalid stored pairing credentials")
	}
	pairingID, pairErr := uuid.Parse(credential.PairingID)
	daemonID, daemonErr := uuid.Parse(credential.DaemonDeviceID)
	mobileID, mobileErr := uuid.Parse(credential.MobileDeviceID)
	if pairErr != nil || daemonErr != nil || mobileErr != nil || len(credential.Ed25519Private) != ed25519.PrivateKeySize || len(credential.X25519Private) != 32 || len(credential.X25519Public) != 32 || len(credential.DaemonX25519) != 32 || len(credential.DaemonCapability) == 0 || len(credential.MobileCapability) == 0 || credential.RelayURL == "" {
		return errors.New("stored pairing credentials are incomplete")
	}
	c.storeCredential(credential, pairingID, daemonID, mobileID)
	wipeCredential(&credential)
	return nil
}

func (c *Client) Restore(ctx context.Context, raw []byte) error {
	if err := c.LoadCredentials(raw); err != nil {
		return err
	}
	return c.reconnect(ctx)
}

// UpdateRelayURL changes the origin used by the next connection attempt and
// invalidates the current transport. The caller is responsible for applying
// its own origin validation before invoking this method.
func (c *Client) UpdateRelayURL(relayURL string) {
	c.mu.Lock()
	if c.credential.RelayURL == relayURL {
		c.mu.Unlock()
		return
	}
	c.credential.RelayURL = relayURL
	c.connectUntil, c.connectCode, c.connectBackoff = time.Time{}, "", 0
	connection := c.conn
	c.mu.Unlock()
	if connection != nil {
		c.disconnectConnection(connection, "relay_changed", "Relay origin changed")
	}
}

func (c *Client) connect(parent context.Context, relayURL string, pairingID, daemonID, mobileID uuid.UUID, edPrivate ed25519.PrivateKey, xKey noise.DHKey, daemonX, daemonCapability, mobileCapability []byte, scopes []string) error {
	establishCtx, establishCancel := context.WithTimeout(parent, sessionHandshakeTimeout)
	defer establishCancel()
	endpoint, err := websocketEndpoint(relayURL, "/v1/connect")
	if err != nil {
		return err
	}
	connection, response, err := websocket.Dial(establishCtx, endpoint, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if response != nil && response.StatusCode == http.StatusTooManyRequests {
			return &CallError{Code: "rate_limited", Message: "Relay rate limited the mobile connection"}
		}
		return err
	}
	// coder/websocket defaults to a 32 KiB message read limit, while control
	// responses can be much larger than that (within the protocol frame limit).
	connection.SetReadLimit(waProtocol.MaxFrameBytes)
	keepConnection := false
	defer func() {
		if !keepConnection {
			connection.CloseNow()
		}
	}()
	challengeFrame, err := readFrame(establishCtx, connection)
	if err != nil || challengeFrame.GetServerChallenge() == nil {
		return errors.New("connect endpoint did not provide a challenge")
	}
	challenge := challengeFrame.GetServerChallenge()
	signature := ed25519.Sign(edPrivate, waProtocol.AuthSignatureInput(challenge.ConnectionId, challenge.Challenge, challenge.ExpiresAtMs, mobileCapability))
	auth := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: newID(), Body: &relayv1.ClientFrame_ClientAuth{ClientAuth: &relayv1.ClientAuth{DeviceId: mobileID[:], Role: relayv1.DeviceRole_DEVICE_ROLE_MOBILE, CapabilityHash: mobileCapability, Signature: signature}}}
	if err = writeFrame(establishCtx, connection, auth); err != nil {
		return err
	}
	authResult, err := readFrame(establishCtx, connection)
	if err != nil || authResult.GetAuthOk() == nil {
		return errors.New("mobile authentication failed")
	}
	sessionID := newID()
	binding := e2ee.Binding{WireMajor: waProtocol.WireMajor, PairingID: pairingID[:], DaemonDeviceID: daemonID[:], MobileDeviceID: mobileID[:], SessionID: sessionID, DaemonCapability: daemonCapability, MobileCapability: mobileCapability}
	handshake, err := e2ee.NewHandshake(true, xKey, daemonX, binding, rand.Reader)
	if err != nil {
		return err
	}
	firstMessage, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		return err
	}
	openMessageID := newID()
	open := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: openMessageID, Body: &relayv1.ClientFrame_SessionOpen{SessionOpen: &relayv1.SessionOpen{PairingId: pairingID[:], SessionId: sessionID, RecipientDeviceId: daemonID[:], Handshake: firstMessage}}}
	if err = writeFrame(establishCtx, connection, open); err != nil {
		return err
	}
	handshakeMessage, err := waitForSessionHandshake(establishCtx, connection, openMessageID, pairingID, sessionID, daemonID, mobileID)
	if err != nil {
		return err
	}
	_, first, second, err := handshake.ReadMessage(nil, handshakeMessage.Handshake)
	if err != nil {
		return err
	}
	secure, _ := e2ee.NewSession(true, first, second, binding)
	connectionCtx, connectionCancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.conn, c.secure, c.cancel = connection, secure, connectionCancel
	c.pairingID, c.deviceID, c.daemonID, c.sessionID = pairingID, mobileID, daemonID, sessionID
	c.scopes, c.connected, c.lastError = append([]string(nil), scopes...), true, ""
	c.connectUntil, c.connectCode, c.connectBackoff = time.Time{}, "", 0
	c.mu.Unlock()
	c.emitTransport("online", "")
	go c.readLoop(connectionCtx, connection)
	keepConnection = true
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	id := uuid.NewString()
	request, _ := json.Marshal(map[string]any{"type": "request", "id": id, "contract": "mobile-control-v1", "method": method, "params": params})
	c.mu.Lock()
	connected := c.connected && c.secure != nil
	c.mu.Unlock()
	if !connected {
		if err := c.reconnect(ctx); err != nil {
			if callError, ok := err.(*CallError); ok {
				return nil, callError
			}
			return nil, &CallError{Code: "peer_offline", Message: "mobile endpoint is not connected: " + err.Error()}
		}
	}
	c.mu.Lock()
	if !c.connected || c.secure == nil {
		c.mu.Unlock()
		return nil, &CallError{Code: "peer_offline", Message: "mobile endpoint is not connected"}
	}
	waiter := make(chan json.RawMessage, 1)
	c.pending[id] = waiter
	secure, pairingID, daemonID, sessionID, connection := c.secure, c.pairingID, c.daemonID, append([]byte(nil), c.sessionID...), c.conn
	c.mu.Unlock()
	var routeWaiter chan routeResult
	var routedMessageID []byte
	routeAccepted := false
	c.writeMu.Lock()
	ciphertext, sequence, err := secure.Encrypt(e2ee.ControlContentType, request, time.Now().Add(time.Minute))
	if err == nil {
		routedMessageID = newID()
		routeWaiter = make(chan routeResult, 1)
		c.mu.Lock()
		c.pendingRoutes[string(routedMessageID)] = routeWaiter
		c.mu.Unlock()
		frame := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: routedMessageID, Body: &relayv1.ClientFrame_SessionData{SessionData: &relayv1.SessionData{PairingId: pairingID[:], SessionId: sessionID, RecipientDeviceId: daemonID[:], SenderSequence: sequence, TrafficClass: relayv1.TrafficClass_TRAFFIC_CLASS_CONTROL, CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli(), Ciphertext: ciphertext}}}
		err = writeFrame(ctx, connection, frame)
	}
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.pendingRoutes, string(routedMessageID))
		c.mu.Unlock()
		return nil, err
	}
	select {
	case route, ok := <-routeWaiter:
		if !ok {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, &CallError{Code: "peer_offline", Message: "Relay connection closed before the request was routed"}
		}
		if !route.accepted {
			code := route.code
			if code == "" {
				code = "peer_offline"
			}
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			if code == "peer_offline" {
				c.disconnectConnection(connection, code, "Desktop peer is offline")
			}
			return nil, &CallError{Code: code, Message: "Relay rejected the request: " + code}
		}
		routeAccepted = true
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.pendingRoutes, string(routedMessageID))
		c.mu.Unlock()
		return nil, &CallError{Code: "request_timeout", Message: ctx.Err().Error()}
	}
	select {
	case response, ok := <-waiter:
		if !ok {
			if routeAccepted {
				return nil, &CallError{Code: "ambiguous_write", Message: "Relay connection closed after the request was accepted; the response was not received"}
			}
			return nil, &CallError{Code: "peer_offline", Message: "Relay connection closed before the response was received"}
		}
		return response, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, &CallError{Code: "request_timeout", Message: ctx.Err().Error()}
	}
}

func (c *Client) reconnect(ctx context.Context) error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	c.mu.Lock()
	if c.connected && c.secure != nil {
		c.mu.Unlock()
		return nil
	}
	if time.Now().Before(c.connectUntil) {
		code := c.connectCode
		if code == "" {
			code = "peer_offline"
		}
		message := c.lastError
		if message == "" {
			message = "mobile endpoint reconnect is backing off"
		}
		c.mu.Unlock()
		return &CallError{Code: code, Message: message}
	}
	credential := cloneCredential(c.credential)
	c.mu.Unlock()
	defer wipeCredential(&credential)
	if credential.PairingID == "" {
		return errors.New("no stored pairing credentials")
	}
	pairingID, err := uuid.Parse(credential.PairingID)
	if err != nil {
		return err
	}
	daemonID, err := uuid.Parse(credential.DaemonDeviceID)
	if err != nil {
		return err
	}
	mobileID, err := uuid.Parse(credential.MobileDeviceID)
	if err != nil {
		return err
	}
	xKey := noise.DHKey{Private: credential.X25519Private, Public: credential.X25519Public}
	err = c.connect(ctx, credential.RelayURL, pairingID, daemonID, mobileID, ed25519.PrivateKey(credential.Ed25519Private), xKey, credential.DaemonX25519, credential.DaemonCapability, credential.MobileCapability, credential.Scopes)
	if err == nil {
		return nil
	}
	c.recordConnectFailure(err)
	if callError, ok := err.(*CallError); ok {
		return callError
	}
	return err
}

func (c *Client) storeCredential(credential CredentialSet, pairingID, daemonID, mobileID uuid.UUID) {
	stored := cloneCredential(credential)
	c.mu.Lock()
	samePairing := c.credential.PairingID == credential.PairingID
	wipeCredential(&c.credential)
	c.credential = stored
	if !samePairing {
		c.connectUntil, c.connectCode, c.connectBackoff = time.Time{}, "", 0
	}
	if !c.connected {
		c.pairingID, c.daemonID, c.deviceID = pairingID, daemonID, mobileID
		c.scopes = append([]string(nil), credential.Scopes...)
	}
	c.mu.Unlock()
}

func (c *Client) recordConnectFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connectBackoff == 0 {
		c.connectBackoff = 500 * time.Millisecond
	} else {
		c.connectBackoff *= 2
		if c.connectBackoff > 30*time.Second {
			c.connectBackoff = 30 * time.Second
		}
	}
	c.connectUntil = time.Now().Add(c.connectBackoff)
	c.connectCode = "peer_offline"
	if callError, ok := err.(*CallError); ok {
		c.connectCode = callError.Code
	}
	c.lastError = err.Error()
}

func waitForSessionHandshake(ctx context.Context, connection *websocket.Conn, openMessageID []byte, pairingID uuid.UUID, sessionID []byte, daemonID, mobileID uuid.UUID) (*relayv1.SessionHandshake, error) {
	waitCtx, cancel := context.WithTimeout(ctx, sessionHandshakeTimeout)
	defer cancel()
	for {
		frame, err := readFrame(waitCtx, connection)
		if err != nil {
			if waitCtx.Err() != nil {
				return nil, &CallError{Code: "request_timeout", Message: "timed out waiting for the secure session handshake"}
			}
			return nil, err
		}
		candidate, err := sessionHandshakeFromFrame(frame, openMessageID, pairingID, sessionID, daemonID, mobileID)
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			return candidate, nil
		}
	}
}

func sessionHandshakeFromFrame(frame *relayv1.ServerFrame, openMessageID []byte, pairingID uuid.UUID, sessionID []byte, daemonID, mobileID uuid.UUID) (*relayv1.SessionHandshake, error) {
	if ack := frame.GetRouteAck(); ack != nil && bytes.Equal(ack.RoutedMessageId, openMessageID) {
		if !ack.Accepted {
			code := ack.ErrorCode
			if code == "" {
				code = "peer_offline"
			}
			return nil, &CallError{Code: code, Message: "Relay rejected the secure session request: " + code}
		}
		return nil, nil
	}
	if protocolError := frame.GetProtocolError(); protocolError != nil && bytes.Equal(protocolError.RelatedMessageId, openMessageID) {
		code := protocolError.Code
		if code == "" {
			code = "temporarily_unavailable"
		}
		return nil, &CallError{Code: code, Message: protocolError.Message}
	}
	if revoked := frame.GetPairRevoked(); revoked != nil && bytes.Equal(revoked.PairingId, pairingID[:]) {
		return nil, &CallError{Code: "pairing_revoked", Message: "pairing was revoked while opening the secure session"}
	}
	candidate := frame.GetSessionHandshake()
	if candidate == nil || !bytes.Equal(candidate.PairingId, pairingID[:]) || !bytes.Equal(candidate.SessionId, sessionID) || !bytes.Equal(candidate.SenderDeviceId, daemonID[:]) || !bytes.Equal(candidate.RecipientDeviceId, mobileID[:]) {
		return nil, nil
	}
	return candidate, nil
}

// Disconnect closes the active transport while retaining the pairing
// credential so the client can be selected and reconnected later.
func (c *Client) Disconnect() {
	c.shutdown(false)
	c.emitTransport("disconnected", "")
}

// Close releases the transport and permanently wipes the pairing credential.
func (c *Client) Close() {
	c.shutdown(true)
	c.emitTransport("closed", "")
}

func (c *Client) shutdown(wipe bool) {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.conn != nil {
		c.conn.CloseNow()
		c.conn = nil
	}
	if c.secure != nil {
		c.secure.Close()
		c.secure = nil
	}
	c.connected = false
	if wipe {
		wipeCredential(&c.credential)
	}
	for id, waiter := range c.pending {
		delete(c.pending, id)
		close(waiter)
	}
	for id, waiter := range c.pendingRoutes {
		delete(c.pendingRoutes, id)
		close(waiter)
	}
	c.mu.Unlock()
}

func wipeCredential(value *CredentialSet) {
	for _, field := range [][]byte{value.Ed25519Private, value.X25519Private, value.X25519Public, value.DaemonX25519, value.DaemonCapability, value.MobileCapability} {
		for index := range field {
			field[index] = 0
		}
	}
	*value = CredentialSet{}
}

func cloneCredential(value CredentialSet) CredentialSet {
	return CredentialSet{
		RelayURL:         value.RelayURL,
		PairingID:        value.PairingID,
		DaemonDeviceID:   value.DaemonDeviceID,
		ComputerName:     value.ComputerName,
		MobileDeviceID:   value.MobileDeviceID,
		Ed25519Private:   append([]byte(nil), value.Ed25519Private...),
		X25519Private:    append([]byte(nil), value.X25519Private...),
		X25519Public:     append([]byte(nil), value.X25519Public...),
		DaemonX25519:     append([]byte(nil), value.DaemonX25519...),
		DaemonCapability: append([]byte(nil), value.DaemonCapability...),
		MobileCapability: append([]byte(nil), value.MobileCapability...),
		Scopes:           append([]string(nil), value.Scopes...),
	}
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := Status{Connected: c.connected, LastError: c.lastError, ComputerName: c.credential.ComputerName, Scopes: append([]string(nil), c.scopes...), Events: append([]json.RawMessage(nil), c.events...)}
	if c.pairingID != uuid.Nil {
		status.PairingID, status.DeviceID, status.DaemonID = c.pairingID.String(), c.deviceID.String(), c.daemonID.String()
	}
	return status
}

// disconnectConnection invalidates both the Relay transport and the Noise
// session. A peer_offline route ACK means the daemon no longer has the matching
// in-memory session, so retaining a green/connected state would make every
// subsequent call reuse a channel that cannot ever receive a response.
func (c *Client) disconnectConnection(connection *websocket.Conn, errorCode, message string) bool {
	c.mu.Lock()
	if c.conn != connection {
		c.mu.Unlock()
		return false
	}
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.conn = nil
	if c.secure != nil {
		c.secure.Close()
		c.secure = nil
	}
	c.connected = false
	c.lastError = message
	for id, waiter := range c.pending {
		delete(c.pending, id)
		close(waiter)
	}
	for id, waiter := range c.pendingRoutes {
		delete(c.pendingRoutes, id)
		close(waiter)
	}
	c.mu.Unlock()
	if connection != nil {
		connection.CloseNow()
	}
	c.emitTransport("disconnected", errorCode)
	return true
}

func (c *Client) readLoop(ctx context.Context, connection *websocket.Conn) {
	for {
		frame, err := readFrame(ctx, connection)
		if err != nil {
			c.disconnectConnection(connection, "peer_offline", err.Error())
			return
		}
		c.mu.Lock()
		activePairingID := c.pairingID
		c.mu.Unlock()
		if revoked := frame.GetPairRevoked(); revoked != nil && bytes.Equal(revoked.PairingId, activePairingID[:]) {
			c.mu.Lock()
			c.connected, c.lastError = false, "pairing was revoked"
			for id, waiter := range c.pending {
				delete(c.pending, id)
				close(waiter)
			}
			for id, waiter := range c.pendingRoutes {
				delete(c.pendingRoutes, id)
				close(waiter)
			}
			c.mu.Unlock()
			c.emitTransport("revoked", "pairing_revoked")
			connection.CloseNow()
			return
		}
		if ack := frame.GetRouteAck(); ack != nil {
			c.handleRouteAck(ack)
			continue
		}
		data := frame.GetSessionData()
		if data == nil {
			continue
		}
		c.mu.Lock()
		secure := c.secure
		activeConnection := c.conn == connection
		activeSessionID := append([]byte(nil), c.sessionID...)
		c.mu.Unlock()
		if !activeConnection || secure == nil {
			return
		}
		// A daemon may have emitted a late frame from the previous Noise
		// session while Relay was replacing this device's connection. It is
		// not decryptable with the current session and must not tear down the
		// replacement connection.
		if !bytes.Equal(data.SessionId, activeSessionID) {
			continue
		}
		payload, decryptErr := secure.Decrypt(data.Ciphertext)
		if decryptErr != nil || payload.Sequence != data.SenderSequence {
			connection.CloseNow()
			continue
		}
		c.dispatchControlPayload(payload.Body)
	}
}

func (c *Client) handleRouteAck(ack *relayv1.RouteAck) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	waiter := c.pendingRoutes[string(ack.RoutedMessageId)]
	if waiter == nil {
		return false
	}
	delete(c.pendingRoutes, string(ack.RoutedMessageId))
	waiter <- routeResult{accepted: ack.Accepted, code: ack.ErrorCode}
	return true
}

func parseQR(uri string) (QRPayload, []byte, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "waw" || parsed.Host != "pair" || parsed.Query().Get("v") != "1" {
		return QRPayload{}, nil, errors.New("invalid pairing URI")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parsed.Query().Get("payload"))
	if err != nil {
		return QRPayload{}, nil, err
	}
	var qr QRPayload
	secret := []byte(nil)
	if err = json.Unmarshal(raw, &qr); err == nil {
		secret, err = base64.RawURLEncoding.DecodeString(qr.Secret)
	}
	if err != nil || len(secret) != 32 || uuid.Validate(qr.PairingID) != nil || uuid.Validate(qr.DaemonDeviceID) != nil {
		return QRPayload{}, nil, errors.New("invalid pairing payload")
	}
	return qr, secret, nil
}

func websocketEndpoint(origin, path string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid Relay HTTP(S) origin")
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = path
	return parsed.String(), nil
}

func readFrame(ctx context.Context, connection *websocket.Conn) (*relayv1.ServerFrame, error) {
	kind, raw, err := connection.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Relay frame: %w", err)
	}
	if kind != websocket.MessageBinary {
		return nil, errors.New("Relay sent a non-binary frame")
	}
	var frame relayv1.ServerFrame
	if err = proto.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

func writeFrame(ctx context.Context, connection *websocket.Conn, frame *relayv1.ClientFrame) error {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageBinary, raw)
}

func newID() []byte {
	value, _ := waProtocol.NewID()
	return value
}

func stableScopes(scopes []string) []string {
	result := append([]string(nil), scopes...)
	sort.Strings(result)
	return result
}
