// Package e2ee implements the endpoint-only Noise session. The Relay server
// routes the resulting ciphertext but never imports or uses this package to
// decrypt application payloads.
package e2ee

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/flynn/noise"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
)

const (
	SuiteName          = "Noise_KK_25519_ChaChaPoly_SHA256"
	ControlContentType = "desktop-control-json-v1"
	AttachmentType     = "attachment-chunk-v1"
)

var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

type Binding struct {
	WireMajor        uint32
	PairingID        []byte
	DaemonDeviceID   []byte
	MobileDeviceID   []byte
	SessionID        []byte
	DaemonCapability []byte
	MobileCapability []byte
}

func (b Binding) Prologue() ([]byte, error) {
	if b.WireMajor == 0 || len(b.PairingID) != 16 || len(b.DaemonDeviceID) != 16 || len(b.MobileDeviceID) != 16 || len(b.SessionID) != 16 || len(b.DaemonCapability) == 0 || len(b.MobileCapability) == 0 {
		return nil, errors.New("invalid Noise binding")
	}
	out := []byte("work-anywhere/noise/v1\x00")
	var major [4]byte
	binary.BigEndian.PutUint32(major[:], b.WireMajor)
	out = append(out, major[:]...)
	for _, field := range [][]byte{b.PairingID, b.DaemonDeviceID, b.MobileDeviceID, b.SessionID, b.DaemonCapability, b.MobileCapability} {
		if len(field) > 65535 {
			return nil, errors.New("Noise binding field is too large")
		}
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(field)))
		out = append(out, size[:]...)
		out = append(out, field...)
	}
	return out, nil
}

func GenerateStaticKey(random io.Reader) (noise.DHKey, error) {
	return suite.GenerateKeypair(random)
}

func NewHandshake(initiator bool, local noise.DHKey, peerPublic []byte, binding Binding, random io.Reader) (*noise.HandshakeState, error) {
	if len(local.Private) != 32 || len(local.Public) != 32 || len(peerPublic) != 32 {
		return nil, errors.New("invalid X25519 static key")
	}
	prologue, err := binding.Prologue()
	if err != nil {
		return nil, err
	}
	return noise.NewHandshakeState(noise.Config{CipherSuite: suite, Random: random, Pattern: noise.HandshakeKK, Initiator: initiator, Prologue: prologue, StaticKeypair: local, PeerStatic: append([]byte(nil), peerPublic...)})
}

type Session struct {
	send, receive   *noise.CipherState
	sendSequence    uint64
	receiveSequence uint64
	binding         Binding
	initiator       bool
	sendMu          sync.Mutex
	receiveMu       sync.Mutex
}

func NewSession(initiator bool, first, second *noise.CipherState, binding Binding) (*Session, error) {
	if first == nil || second == nil {
		return nil, errors.New("Noise handshake is incomplete")
	}
	if initiator {
		return &Session{send: first, receive: second, binding: binding, initiator: true}, nil
	}
	return &Session{send: second, receive: first, binding: binding}, nil
}

func (s *Session) Encrypt(contentType string, body []byte, expiresAt time.Time) ([]byte, uint64, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if contentType != ControlContentType && contentType != AttachmentType {
		return nil, 0, errors.New("unsupported encrypted content type")
	}
	if expiresAt.Before(time.Now()) {
		return nil, 0, errors.New("encrypted payload already expired")
	}
	if s.send == nil {
		return nil, 0, errors.New("session is closed")
	}
	s.sendSequence++
	sender, recipient := s.binding.DaemonDeviceID, s.binding.MobileDeviceID
	if s.initiator {
		sender, recipient = s.binding.MobileDeviceID, s.binding.DaemonDeviceID
	}
	payload := &relayv1.EncryptedPayload{Sequence: s.sendSequence, ContentType: contentType, CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: expiresAt.UnixMilli(), Body: body, PairingId: s.binding.PairingID, SessionId: s.binding.SessionID, SenderDeviceId: sender, RecipientDeviceId: recipient}
	plain, err := proto.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	ciphertext, err := s.send.Encrypt(nil, nil, plain)
	return ciphertext, s.sendSequence, err
}

func (s *Session) Decrypt(ciphertext []byte) (*relayv1.EncryptedPayload, error) {
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()
	if s.receive == nil {
		return nil, errors.New("session is closed")
	}
	plain, err := s.receive.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nil, err
	}
	var payload relayv1.EncryptedPayload
	if err = proto.Unmarshal(plain, &payload); err != nil {
		return nil, err
	}
	if payload.Sequence != s.receiveSequence+1 {
		return nil, errors.New("encrypted payload sequence gap")
	}
	expectedSender, expectedRecipient := s.binding.MobileDeviceID, s.binding.DaemonDeviceID
	if s.initiator {
		expectedSender, expectedRecipient = s.binding.DaemonDeviceID, s.binding.MobileDeviceID
	}
	if payload.ExpiresAtMs < time.Now().UnixMilli() || !equal(payload.PairingId, s.binding.PairingID) || !equal(payload.SessionId, s.binding.SessionID) || !equal(payload.SenderDeviceId, expectedSender) || !equal(payload.RecipientDeviceId, expectedRecipient) {
		return nil, errors.New("encrypted payload binding or expiry failed")
	}
	s.receiveSequence = payload.Sequence
	return &payload, nil
}

func (s *Session) Close() {
	s.sendMu.Lock()
	s.receiveMu.Lock()
	s.send, s.receive = nil, nil
	s.receiveMu.Unlock()
	s.sendMu.Unlock()
}

func equal(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	var difference byte
	for index := range first {
		difference |= first[index] ^ second[index]
	}
	return difference == 0
}
