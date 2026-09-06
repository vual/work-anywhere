package remote

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/flynn/noise"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/hurf/work-anywhere/desktop/internal/remote/gen/waw/relay/v1"
)

var noiseSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

type noiseBinding struct {
	PairingID, DaemonID, MobileID, SessionID, DaemonCapability, MobileCapability []byte
}

func (b noiseBinding) prologue() ([]byte, error) {
	if len(b.PairingID) != 16 || len(b.DaemonID) != 16 || len(b.MobileID) != 16 || len(b.SessionID) != 16 || len(b.DaemonCapability) == 0 || len(b.MobileCapability) == 0 {
		return nil, errors.New("invalid Noise binding")
	}
	result := []byte("work-anywhere/noise/v1\x00")
	result = binary.BigEndian.AppendUint32(result, wireMajor)
	for _, field := range [][]byte{b.PairingID, b.DaemonID, b.MobileID, b.SessionID, b.DaemonCapability, b.MobileCapability} {
		if len(field) > 65535 {
			return nil, errors.New("Noise binding field is too large")
		}
		result = binary.BigEndian.AppendUint16(result, uint16(len(field)))
		result = append(result, field...)
	}
	return result, nil
}

func newResponderHandshake(private, public, peerPublic []byte, binding noiseBinding, random io.Reader) (*noise.HandshakeState, error) {
	prologue, err := binding.prologue()
	if err != nil {
		return nil, err
	}
	return noise.NewHandshakeState(noise.Config{CipherSuite: noiseSuite, Random: random, Pattern: noise.HandshakeKK, Initiator: false, Prologue: prologue, StaticKeypair: noise.DHKey{Private: private, Public: public}, PeerStatic: peerPublic})
}

type secureSession struct {
	send, receive   *noise.CipherState
	sendSequence    uint64
	receiveSequence uint64
	binding         noiseBinding
	sendMu          sync.Mutex
	receiveMu       sync.Mutex
}

func newResponderSession(first, second *noise.CipherState, binding noiseBinding) *secureSession {
	return &secureSession{send: second, receive: first, binding: binding}
}

func (s *secureSession) encrypt(body []byte, expiresAt time.Time) ([]byte, uint64, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.sendSequence++
	payload := &relayv1.EncryptedPayload{Sequence: s.sendSequence, ContentType: "desktop-control-json-v1", CreatedAtMs: time.Now().UnixMilli(), ExpiresAtMs: expiresAt.UnixMilli(), Body: body, PairingId: s.binding.PairingID, SessionId: s.binding.SessionID, SenderDeviceId: s.binding.DaemonID, RecipientDeviceId: s.binding.MobileID}
	plain, err := proto.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	ciphertext, err := s.send.Encrypt(nil, nil, plain)
	return ciphertext, s.sendSequence, err
}

func (s *secureSession) decrypt(ciphertext []byte) (*relayv1.EncryptedPayload, error) {
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()
	plain, err := s.receive.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nil, err
	}
	var payload relayv1.EncryptedPayload
	if err = proto.Unmarshal(plain, &payload); err != nil {
		return nil, err
	}
	if payload.Sequence != s.receiveSequence+1 || payload.ExpiresAtMs < time.Now().UnixMilli() || payload.ContentType != "desktop-control-json-v1" || !same(payload.PairingId, s.binding.PairingID) || !same(payload.SessionId, s.binding.SessionID) || !same(payload.SenderDeviceId, s.binding.MobileID) || !same(payload.RecipientDeviceId, s.binding.DaemonID) {
		return nil, errors.New("remote payload binding, sequence, or expiry failed")
	}
	s.receiveSequence = payload.Sequence
	return &payload, nil
}

func same(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	var difference byte
	for index := range first {
		difference |= first[index] ^ second[index]
	}
	return difference == 0
}
