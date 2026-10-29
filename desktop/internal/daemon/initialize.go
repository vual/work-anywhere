package daemon

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/jsonrpc"
)

func initializeConflict(conn *clientConn, id json.RawMessage) error {
	return conn.Enqueue(ipc.Frame{Kind: ipc.KindServerFrame, Payload: ensureNewline(mustJSON(map[string]any{
		"id": id, "error": map[string]any{"code": -32000, "message": "initialize capabilities differ; retry after the instance is idle"},
	}))})
}

func desktopUnavailable(id json.RawMessage) []byte {
	return mustJSON(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": "desktop_unavailable: no Desktop frontend can handle this infrastructure request"}})
}

// Success and the initialized notification are separate states. An error must
// neither poison the reconnect cache nor let the control plane start a turn.
func (i *Instance) completeInitialize(generation uint64, raw []byte, automatic bool) {
	message, err := jsonrpc.Parse(raw)
	success := err == nil && (len(message.Error) == 0 || bytes.Equal(message.Error, []byte("null"))) && len(message.Result) > 0
	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		return
	}
	i.initializeInFlight = false
	waiters := i.initializeWaiters
	i.initializeWaiters = nil
	if success {
		i.cachedInitializeResponse = append([]byte(nil), raw...)
		i.state, i.lastError = "waiting_initialize", ""
	} else {
		i.cachedInitializeResponse, i.cachedInitializeRequest, i.cachedInitialized = nil, nil, nil
		i.initializedSent = false
		i.state, i.lastError = "error", "App Server initialize failed"
	}
	initialized := append([]byte(nil), i.cachedInitialized...)
	i.mu.Unlock()
	if success && (automatic || len(initialized) > 0) {
		if len(initialized) == 0 {
			initialized = []byte(`{"method":"initialized","params":{}}`)
		}
		_ = i.sendInitializedForGeneration(initialized, &generation)
	}
	i.answerInitializeWaiters(raw, waiters)
}

func (i *Instance) sendInitialized(raw []byte) error {
	return i.sendInitializedForGeneration(raw, nil)
}

func (i *Instance) sendInitializedForGeneration(raw []byte, expected *uint64) error {
	i.serverWriteMu.Lock()
	defer i.serverWriteMu.Unlock()
	i.mu.Lock()
	if expected != nil && *expected != i.generation {
		i.mu.Unlock()
		return nil
	}
	if i.initializedSent {
		i.mu.Unlock()
		return nil
	}
	i.cachedInitialized = append([]byte(nil), raw...)
	if len(i.cachedInitializeResponse) == 0 {
		i.mu.Unlock()
		return nil
	}
	stdin, generation := i.stdin, i.generation
	i.mu.Unlock()
	if stdin == nil {
		return errors.New("App Server is not running")
	}
	_, err := stdin.Write(ensureNewline(raw))
	i.mu.Lock()
	defer i.mu.Unlock()
	if generation == i.generation {
		if err == nil {
			i.initializedSent = true
			i.state = "ready"
			i.updateActivityStateLocked()
		} else {
			i.state, i.lastError = "error", "initialized write failed"
		}
	}
	return err
}
