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
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/internal/e2ee"
	waProtocol "github.com/vual/work-anywhere/relay/internal/protocol"
)

type QRPayload struct {
	RelayURL       string `json:"relayUrl"`
	PairingID      string `json:"pairingId"`
	Secret         string `json:"secret"`
	DaemonDeviceID string `json:"daemonDeviceId"`
	DaemonEd25519  string `json:"daemonEd25519"`
	DaemonX25519   string `json:"daemonX25519"`
	CapabilityHash string `json:"capabilityHash"`
	ExpiresAtMS    int64  `json:"expiresAtMs"`
}

type Client struct {
	mu        sync.Mutex
	conn      *websocket.Conn
	writeMu   sync.Mutex
	secure    *e2ee.Session
	pairingID uuid.UUID
	deviceID  uuid.UUID
	daemonID  uuid.UUID
	sessionID []byte
	scopes    []string
	pending   map[string]chan json.RawMessage
	events    []json.RawMessage
	lastError string
	connected bool
	cancel    context.CancelFunc
}

type Status struct {
	Connected bool              `json:"connected"`
	PairingID string            `json:"pairingId,omitempty"`
	DeviceID  string            `json:"deviceId,omitempty"`
	DaemonID  string            `json:"daemonId,omitempty"`
	Scopes    []string          `json:"scopes,omitempty"`
	LastError string            `json:"lastError,omitempty"`
	Events    []json.RawMessage `json:"events"`
}

func New() *Client { return &Client{pending: make(map[string]chan json.RawMessage)} }

func (c *Client) Pair(ctx context.Context, uri string) error {
	qr, secret, err := parseQR(uri)
	if err != nil {
		return err
	}
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
	for index := range secret {
		secret[index] = 0
	}
	return c.connect(ctx, qr.RelayURL, pairingID, daemonID, mobileID, edPrivate, xKey, daemonX, daemonCapability, mobileCapability[:], result.Scopes)
}

func (c *Client) connect(parent context.Context, relayURL string, pairingID, daemonID, mobileID uuid.UUID, edPrivate ed25519.PrivateKey, xKey noise.DHKey, daemonX, daemonCapability, mobileCapability []byte, scopes []string) error {
	ctx, cancel := context.WithCancel(parent)
	endpoint, _ := websocketEndpoint(relayURL, "/v1/connect")
	connection, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		cancel()
		return err
	}
	keepConnection := false
	defer func() {
		if !keepConnection {
			connection.CloseNow()
			cancel()
		}
	}()
	challengeFrame, err := readFrame(ctx, connection)
	if err != nil || challengeFrame.GetServerChallenge() == nil {
		connection.CloseNow()
		cancel()
		return errors.New("connect endpoint did not provide a challenge")
	}
	challenge := challengeFrame.GetServerChallenge()
	signature := ed25519.Sign(edPrivate, waProtocol.AuthSignatureInput(challenge.ConnectionId, challenge.Challenge, challenge.ExpiresAtMs, mobileCapability))
	auth := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: newID(), Body: &relayv1.ClientFrame_ClientAuth{ClientAuth: &relayv1.ClientAuth{DeviceId: mobileID[:], Role: relayv1.DeviceRole_DEVICE_ROLE_MOBILE, CapabilityHash: mobileCapability, Signature: signature}}}
	if err = writeFrame(ctx, connection, auth); err != nil {
		connection.CloseNow()
		cancel()
		return err
	}
	authResult, err := readFrame(ctx, connection)
	if err != nil || authResult.GetAuthOk() == nil {
		connection.CloseNow()
		cancel()
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
	open := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: newID(), Body: &relayv1.ClientFrame_SessionOpen{SessionOpen: &relayv1.SessionOpen{PairingId: pairingID[:], SessionId: sessionID, RecipientDeviceId: daemonID[:], Handshake: firstMessage}}}
	if err = writeFrame(ctx, connection, open); err != nil {
		return err
	}
	var handshakeFrame *relayv1.ServerFrame
	for handshakeFrame == nil || handshakeFrame.GetSessionHandshake() == nil {
		handshakeFrame, err = readFrame(ctx, connection)
		if err != nil {
			return err
		}
	}
	_, first, second, err := handshake.ReadMessage(nil, handshakeFrame.GetSessionHandshake().Handshake)
	if err != nil {
		return err
	}
	secure, _ := e2ee.NewSession(true, first, second, binding)
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.conn, c.secure, c.cancel = connection, secure, cancel
	c.pairingID, c.deviceID, c.daemonID, c.sessionID = pairingID, mobileID, daemonID, sessionID
	c.scopes, c.connected, c.lastError = append([]string(nil), scopes...), true, ""
	c.mu.Unlock()
	go c.readLoop(ctx, connection)
	keepConnection = true
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	id := uuid.NewString()
	request, _ := json.Marshal(map[string]any{"type": "request", "id": id, "method": method, "params": params})
	c.mu.Lock()
	if !c.connected || c.secure == nil {
		c.mu.Unlock()
		return nil, errors.New("development mobile is not connected")
	}
	waiter := make(chan json.RawMessage, 1)
	c.pending[id] = waiter
	secure, pairingID, daemonID, sessionID, connection := c.secure, c.pairingID, c.daemonID, append([]byte(nil), c.sessionID...), c.conn
	c.mu.Unlock()
	ciphertext, sequence, err := secure.Encrypt(e2ee.ControlContentType, request, time.Now().Add(time.Minute))
	if err == nil {
		frame := &relayv1.ClientFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: newID(), Body: &relayv1.ClientFrame_SessionData{SessionData: &relayv1.SessionData{PairingId: pairingID[:], SessionId: sessionID, RecipientDeviceId: daemonID[:], SenderSequence: sequence, TrafficClass: relayv1.TrafficClass_TRAFFIC_CLASS_CONTROL, CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli(), Ciphertext: ciphertext}}}
		c.writeMu.Lock()
		err = writeFrame(ctx, connection, frame)
		c.writeMu.Unlock()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case response := <-waiter:
		return response, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := Status{Connected: c.connected, LastError: c.lastError, Scopes: append([]string(nil), c.scopes...), Events: append([]json.RawMessage(nil), c.events...)}
	if c.pairingID != uuid.Nil {
		status.PairingID, status.DeviceID, status.DaemonID = c.pairingID.String(), c.deviceID.String(), c.daemonID.String()
	}
	return status
}

func (c *Client) readLoop(ctx context.Context, connection *websocket.Conn) {
	for {
		frame, err := readFrame(ctx, connection)
		if err != nil {
			c.mu.Lock()
			if c.conn == connection {
				c.connected, c.lastError = false, err.Error()
			}
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		activePairingID := c.pairingID
		c.mu.Unlock()
		if revoked := frame.GetPairRevoked(); revoked != nil && bytes.Equal(revoked.PairingId, activePairingID[:]) {
			c.mu.Lock()
			c.connected, c.lastError = false, "pairing was revoked"
			c.mu.Unlock()
			connection.CloseNow()
			return
		}
		data := frame.GetSessionData()
		if data == nil {
			continue
		}
		c.mu.Lock()
		secure := c.secure
		c.mu.Unlock()
		payload, decryptErr := secure.Decrypt(data.Ciphertext)
		if decryptErr != nil || payload.Sequence != data.SenderSequence {
			connection.CloseNow()
			continue
		}
		var envelope struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(payload.Body, &envelope) != nil {
			continue
		}
		c.mu.Lock()
		if envelope.Type == "response" && c.pending[envelope.ID] != nil {
			waiter := c.pending[envelope.ID]
			delete(c.pending, envelope.ID)
			waiter <- append(json.RawMessage(nil), payload.Body...)
		} else {
			c.events = append(c.events, append(json.RawMessage(nil), payload.Body...))
			if len(c.events) > 1000 {
				c.events = c.events[len(c.events)-1000:]
			}
		}
		c.mu.Unlock()
	}
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
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) {
		return "", errors.New("invalid Relay origin")
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
