package routing

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type aclKey struct {
	first  uuid.UUID
	second uuid.UUID
}

type ACLValue struct {
	PairingID uuid.UUID
	Scopes    []string
	ExpiresAt time.Time
}

type ACLCache struct {
	mu      sync.RWMutex
	entries map[aclKey]ACLValue
	ttl     time.Duration
}

func NewACLCache(ttl time.Duration) *ACLCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &ACLCache{entries: make(map[aclKey]ACLValue), ttl: ttl}
}

func (c *ACLCache) Get(first, second uuid.UUID) (ACLValue, bool) {
	key := normalizedACLKey(first, second)
	c.mu.RLock()
	value, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(value.ExpiresAt) {
		if ok {
			c.mu.Lock()
			delete(c.entries, key)
			c.mu.Unlock()
		}
		return ACLValue{}, false
	}
	value.Scopes = append([]string(nil), value.Scopes...)
	return value, true
}

func (c *ACLCache) Put(first, second, pairingID uuid.UUID, scopes []string) {
	c.mu.Lock()
	c.entries[normalizedACLKey(first, second)] = ACLValue{PairingID: pairingID, Scopes: append([]string(nil), scopes...), ExpiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *ACLCache) InvalidatePairing(pairingID uuid.UUID) {
	c.mu.Lock()
	for key, value := range c.entries {
		if value.PairingID == pairingID {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

func normalizedACLKey(first, second uuid.UUID) aclKey {
	if first.String() > second.String() {
		first, second = second, first
	}
	return aclKey{first: first, second: second}
}
