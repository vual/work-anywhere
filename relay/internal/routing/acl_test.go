package routing

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestACLCacheIsSymmetricAndRevocable(t *testing.T) {
	cache := NewACLCache(time.Minute)
	first, second, pairing := uuid.New(), uuid.New(), uuid.New()
	cache.Put(first, second, pairing, []string{"status.read"})
	if value, ok := cache.Get(second, first); !ok || value.PairingID != pairing {
		t.Fatal("symmetric lookup failed")
	}
	cache.InvalidatePairing(pairing)
	if _, ok := cache.Get(first, second); ok {
		t.Fatal("revoked pairing remained cached")
	}
}
