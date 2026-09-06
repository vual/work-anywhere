package secretbox

import (
	"bytes"
	"testing"
)

func TestAssociatedDataIsAuthenticated(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal([]byte("provider-token"), []byte("endpoint-a"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Open(ciphertext, []byte("endpoint-a"))
	if err != nil || string(plain) != "provider-token" {
		t.Fatalf("roundtrip failed: %q %v", plain, err)
	}
	if _, err = box.Open(ciphertext, []byte("endpoint-b")); err == nil {
		t.Fatal("changed associated data was accepted")
	}
}
