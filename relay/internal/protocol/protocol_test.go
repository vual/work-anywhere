package protocol

import (
	"crypto/hmac"
	"encoding/hex"
	"testing"
)

func TestAuthSignatureInputIsStable(t *testing.T) {
	value := AuthSignatureInput([]byte{1, 2}, []byte{3, 4}, 5, []byte{6})
	got := hex.EncodeToString(value)
	const want = "7761772d72656c61792d6465766963652d617574682d7631000000000100000002010200000002030400000000000000050000000106"
	if got != want {
		t.Fatalf("signature input changed:\nwant %s\n got %s", want, got)
	}
}

func TestPairProofIsBoundToChallenge(t *testing.T) {
	input := PairProofInput(make([]byte, 16), []byte("challenge-a"), make([]byte, 16), make([]byte, 32), make([]byte, 32), make([]byte, 32), make([]byte, 32), []byte("daemon-cap"), []byte("mobile-cap"), 123)
	proof := PairProof([]byte("secret"), input)
	changed := PairProofInput(make([]byte, 16), []byte("challenge-b"), make([]byte, 16), make([]byte, 32), make([]byte, 32), make([]byte, 32), make([]byte, 32), []byte("daemon-cap"), []byte("mobile-cap"), 123)
	if hmac.Equal(proof, PairProof([]byte("secret"), changed)) {
		t.Fatal("pair proof was not bound to server challenge")
	}
}
