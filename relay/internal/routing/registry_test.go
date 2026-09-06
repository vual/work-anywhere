package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRegistryNewConnectionWins(t *testing.T) {
	registry := NewRegistry(1)
	id := uuid.New()
	first := NewConnection(context.Background(), id, 1, 1, 10)
	second := NewConnection(context.Background(), id, 1, 1, 10)
	if _, err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	previous, err := registry.Register(second)
	if err != nil || previous != first {
		t.Fatalf("replacement failed: previous=%p err=%v", previous, err)
	}
	if first.Context().Err() == nil {
		t.Fatal("old connection was not cancelled")
	}
	if registry.Unregister(first) {
		t.Fatal("old connection removed the replacement")
	}
	current, ok := registry.Get(id)
	if !ok || current != second {
		t.Fatal("replacement is not registered")
	}
}

func TestConnectionQueueLimitsBytesAndFrames(t *testing.T) {
	connection := NewConnection(context.Background(), uuid.New(), 1, 1, 4)
	if err := connection.Enqueue([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := connection.Enqueue([]byte{5}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
	frame := <-connection.Outbound
	connection.Dequeued(frame)
	if connection.QueueBytes() != 0 {
		t.Fatalf("queue byte count leaked: %d", connection.QueueBytes())
	}
}
