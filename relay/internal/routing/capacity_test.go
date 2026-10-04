package routing

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRegistryCapacityAcrossShards(t *testing.T) {
	registry := NewRegistry(5)
	var wait sync.WaitGroup
	accepted := make(chan *Connection, 100)
	start := make(chan struct{})
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			connection := NewConnection(context.Background(), uuid.New(), 1, DefaultQueueFrames, DefaultQueueBytes)
			if _, err := registry.Register(connection); err == nil {
				accepted <- connection
			} else {
				connection.Close()
			}
		}()
	}
	close(start)
	wait.Wait()
	close(accepted)
	if registry.Count() != 5 {
		t.Fatalf("capacity exceeded: %d", registry.Count())
	}
	for connection := range accepted {
		registry.Unregister(connection)
		connection.Close()
	}
	if registry.Count() != 0 {
		t.Fatal("capacity reservation leaked")
	}
}
