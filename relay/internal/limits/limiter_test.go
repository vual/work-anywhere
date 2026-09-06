package limits

import (
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	now := time.Unix(1, 0)
	limiter := New(2)
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("device", 1, 2) || !limiter.Allow("device", 1, 2) || limiter.Allow("device", 1, 2) {
		t.Fatal("unexpected initial burst behavior")
	}
	now = now.Add(time.Second)
	if !limiter.Allow("device", 1, 2) {
		t.Fatal("expected one refilled token")
	}
}

func TestLimiterEvictsOldestAtCapacity(t *testing.T) {
	now := time.Unix(1, 0)
	limiter := New(1)
	limiter.now = func() time.Time { return now }
	limiter.Allow("old", 1, 1)
	now = now.Add(time.Second)
	if !limiter.Allow("new", 1, 1) || len(limiter.entries) != 1 {
		t.Fatal("capacity was not enforced")
	}
}
