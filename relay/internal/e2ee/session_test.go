package e2ee

import (
	"bytes"
	"testing"
	"time"
)

type incrementingReader byte

func (r *incrementingReader) Read(target []byte) (int, error) {
	for index := range target {
		target[index] = byte(*r)
		*r++
	}
	return len(target), nil
}

func TestKKRoundTripAndTamper(t *testing.T) {
	initiatorRandom, responderRandom := incrementingReader(1), incrementingReader(101)
	initiatorKey, err := GenerateStaticKey(&initiatorRandom)
	if err != nil {
		t.Fatal(err)
	}
	responderKey, err := GenerateStaticKey(&responderRandom)
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	initiator, err := NewHandshake(true, initiatorKey, responderKey.Public, binding, &initiatorRandom)
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(false, responderKey, initiatorKey.Public, binding, &responderRandom)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _, err := initiator.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = responder.ReadMessage(nil, first); err != nil {
		t.Fatal(err)
	}
	second, responderFirst, responderSecond, err := responder.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, initiatorFirst, initiatorSecond, err := initiator.ReadMessage(nil, second)
	if err != nil {
		t.Fatal(err)
	}
	initiatorSession, _ := NewSession(true, initiatorFirst, initiatorSecond, binding)
	responderSession, _ := NewSession(false, responderFirst, responderSecond, binding)
	ciphertext, sequence, err := initiatorSession.Encrypt(ControlContentType, []byte(`{"type":"request"}`), time.Now().Add(time.Minute))
	if err != nil || sequence != 1 {
		t.Fatalf("encrypt: sequence=%d err=%v", sequence, err)
	}
	payload, err := responderSession.Decrypt(ciphertext)
	if err != nil || !bytes.Equal(payload.Body, []byte(`{"type":"request"}`)) {
		t.Fatalf("decrypt: payload=%v err=%v", payload, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = responderSession.Decrypt(ciphertext); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestPrologueBindsCapabilities(t *testing.T) {
	first := testBinding()
	second := testBinding()
	second.MobileCapability = []byte("other")
	one, _ := first.Prologue()
	two, _ := second.Prologue()
	if bytes.Equal(one, two) {
		t.Fatal("capability hash was not bound")
	}
}

func testBinding() Binding {
	return Binding{WireMajor: 1, PairingID: bytes.Repeat([]byte{1}, 16), DaemonDeviceID: bytes.Repeat([]byte{2}, 16), MobileDeviceID: bytes.Repeat([]byte{3}, 16), SessionID: bytes.Repeat([]byte{4}, 16), DaemonCapability: []byte("daemon-capabilities"), MobileCapability: []byte("mobile-capabilities")}
}
