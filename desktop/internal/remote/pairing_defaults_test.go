package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestApprovalWithoutScopesSignsDesktopDefaults(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	daemonID, mobileID, pairingID := uuid.New(), uuid.New(), uuid.New()
	attempt := &PairAttempt{ID: pairingID.String(), MobileDeviceID: mobileID.String(), MobileEd25519: make([]byte, 32), MobileX25519: make([]byte, 32), proofValid: true, ExpiresAt: time.Now().Add(time.Minute)}
	s := &Service{state: persistedState{DeviceID: daemonID.String(), Ed25519Private: private}, attempts: map[string]*PairAttempt{attempt.ID: attempt}}
	// The same nil-scopes path is used by CLI and the management page. With no
	// socket it prepares the signed decision and fails only at transport send.
	if err = s.ResolvePairing(attempt.ID, true, nil); err == nil || err.Error() != "relay is not connected" {
		t.Fatalf("unexpected approval error: %v", err)
	}
	if !reflect.DeepEqual(attempt.grantedScopes, defaultScopes) {
		t.Fatalf("defaults drifted: %v", attempt.grantedScopes)
	}
	input := pairGrantSignatureInput(pairingID[:], daemonID[:], mobileID[:], attempt.MobileEd25519, attempt.MobileX25519, defaultScopes, attempt.grantExpiresAtMS)
	if !ed25519.Verify(public, input, attempt.grantSignature) {
		t.Fatal("default scope signature invalid")
	}
}
