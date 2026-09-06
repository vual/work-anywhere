package routing

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

const registryShards = 64

var ErrCapacity = errors.New("relay connection capacity reached")

type registryShard struct {
	mu    sync.RWMutex
	items map[uuid.UUID]*Connection
}

type Registry struct {
	shards [registryShards]registryShard
	next   atomic.Uint64
	count  atomic.Int64
	max    int64
}

func NewRegistry(maxConnections int) *Registry {
	if maxConnections <= 0 {
		maxConnections = 10_000
	}
	r := &Registry{max: int64(maxConnections)}
	for index := range r.shards {
		r.shards[index].items = make(map[uuid.UUID]*Connection)
	}
	return r
}

func (r *Registry) Register(connection *Connection) (*Connection, error) {
	shard := r.shard(connection.DeviceID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	previous := shard.items[connection.DeviceID]
	if previous == nil && r.count.Load() >= r.max {
		return nil, ErrCapacity
	}
	connection.Epoch = r.next.Add(1)
	shard.items[connection.DeviceID] = connection
	if previous == nil {
		r.count.Add(1)
	}
	if previous != nil {
		previous.Close()
	}
	return previous, nil
}

func (r *Registry) Get(id uuid.UUID) (*Connection, bool) {
	shard := r.shard(id)
	shard.mu.RLock()
	value, ok := shard.items[id]
	shard.mu.RUnlock()
	return value, ok
}

func (r *Registry) Unregister(connection *Connection) bool {
	shard := r.shard(connection.DeviceID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.items[connection.DeviceID] != connection || shard.items[connection.DeviceID].Epoch != connection.Epoch {
		return false
	}
	delete(shard.items, connection.DeviceID)
	r.count.Add(-1)
	connection.Close()
	return true
}

func (r *Registry) Count() int64 { return r.count.Load() }

func (r *Registry) CloseAll() {
	for index := range r.shards {
		shard := &r.shards[index]
		shard.mu.Lock()
		for id, connection := range shard.items {
			connection.Close()
			delete(shard.items, id)
			r.count.Add(-1)
		}
		shard.mu.Unlock()
	}
}

func (r *Registry) shard(id uuid.UUID) *registryShard {
	return &r.shards[binary.BigEndian.Uint64(id[:8])%registryShards]
}
