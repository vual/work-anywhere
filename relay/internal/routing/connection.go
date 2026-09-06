package routing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

var ErrQueueFull = errors.New("connection outbound queue is full")

const (
	DefaultQueueFrames = 256
	DefaultQueueBytes  = 4 << 20
)

type Connection struct {
	DeviceID uuid.UUID
	Role     int16
	Epoch    uint64
	Outbound chan []byte

	ctx       context.Context
	cancel    context.CancelFunc
	maxBytes  int64
	bytes     atomic.Int64
	closeOnce sync.Once
}

func NewConnection(parent context.Context, deviceID uuid.UUID, role int16, frames int, maxBytes int64) *Connection {
	if frames <= 0 {
		frames = DefaultQueueFrames
	}
	if maxBytes <= 0 {
		maxBytes = DefaultQueueBytes
	}
	ctx, cancel := context.WithCancel(parent)
	return &Connection{DeviceID: deviceID, Role: role, Outbound: make(chan []byte, frames), ctx: ctx, cancel: cancel, maxBytes: maxBytes}
}

func (c *Connection) Context() context.Context { return c.ctx }
func (c *Connection) QueueBytes() int64        { return c.bytes.Load() }

func (c *Connection) Enqueue(frame []byte) error {
	copyOfFrame := append([]byte(nil), frame...)
	size := int64(len(copyOfFrame))
	for {
		current := c.bytes.Load()
		if size > c.maxBytes || current+size > c.maxBytes {
			return ErrQueueFull
		}
		if c.bytes.CompareAndSwap(current, current+size) {
			break
		}
	}
	select {
	case <-c.ctx.Done():
		c.bytes.Add(-size)
		return c.ctx.Err()
	case c.Outbound <- copyOfFrame:
		return nil
	default:
		c.bytes.Add(-size)
		return ErrQueueFull
	}
}

func (c *Connection) Dequeued(frame []byte) { c.bytes.Add(-int64(len(frame))) }

func (c *Connection) Close() {
	c.closeOnce.Do(func() { c.cancel() })
}
