package limits

import (
	"sync"
	"time"
)

type entry struct {
	tokens float64
	seen   time.Time
}

// Limiter is a bounded in-memory token bucket registry. It deliberately uses
// the direct peer address; callers must not pass an untrusted forwarded header.
type Limiter struct {
	mu         sync.Mutex
	entries    map[string]entry
	maxEntries int
	now        func() time.Time
}

func New(maxEntries int) *Limiter {
	if maxEntries < 1 {
		maxEntries = 10_000
	}
	return &Limiter{entries: make(map[string]entry), maxEntries: maxEntries, now: time.Now}
}

func (l *Limiter) Allow(key string, tokensPerSecond float64, burst int) bool {
	if key == "" || tokensPerSecond <= 0 || burst <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	value, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.maxEntries {
			l.evictOldest()
		}
		value = entry{tokens: float64(burst), seen: now}
	} else {
		value.tokens += now.Sub(value.seen).Seconds() * tokensPerSecond
		if value.tokens > float64(burst) {
			value.tokens = float64(burst)
		}
		value.seen = now
	}
	if value.tokens < 1 {
		l.entries[key] = value
		return false
	}
	value.tokens--
	l.entries[key] = value
	return true
}

func (l *Limiter) Forget(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *Limiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, value := range l.entries {
		if oldestKey == "" || value.seen.Before(oldest) {
			oldestKey, oldest = key, value.seen
		}
	}
	delete(l.entries, oldestKey)
}
