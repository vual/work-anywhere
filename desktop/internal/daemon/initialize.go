package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

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

// The daemon owns the upstream handshake: some Desktop versions never send
// initialized. Publish success only after completing the upstream handshake.
func (i *Instance) completeInitialize(generation uint64, raw []byte) []byte {
	message, err := jsonrpc.Parse(raw)
	success := err == nil && (len(message.Error) == 0 || bytes.Equal(message.Error, []byte("null"))) && len(message.Result) > 0
	i.serverWriteMu.Lock()
	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		i.serverWriteMu.Unlock()
		return nil
	}
	stdin, sent := i.stdin, i.initializedSent
	initialized := append([]byte(nil), i.cachedInitialized...)
	i.mu.Unlock()
	if success && !sent {
		if len(initialized) == 0 {
			initialized = []byte(`{"method":"initialized"}`)
		}
		if stdin == nil {
			err = errors.New("App Server is not running")
		} else {
			line := ensureNewline(initialized)
			var written int
			written, err = stdin.Write(line)
			if err == nil && written != len(line) {
				err = io.ErrShortWrite
			}
		}
		if err != nil {
			success = false
			raw = mustJSON(map[string]any{"id": message.ID, "error": map[string]any{"code": -32000, "message": "initialized write failed: " + err.Error()}})
		}
	}
	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		i.serverWriteMu.Unlock()
		return nil
	}
	i.initializeInFlight = false
	waiters := i.initializeWaiters
	i.initializeWaiters = nil
	if success {
		i.cachedInitializeResponse = append([]byte(nil), raw...)
		i.initializedSent = true
		i.state, i.lastError = "ready", ""
		i.updateActivityStateLocked()
	} else {
		i.cachedInitializeResponse, i.cachedInitializeRequest, i.cachedInitialized = nil, nil, nil
		i.initializedSent = false
		i.state, i.lastError = "error", "App Server initialize failed"
		if failure, parseErr := jsonrpc.Parse(raw); parseErr == nil {
			var detail struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(failure.Error, &detail) == nil && detail.Message != "" {
				i.lastError = detail.Message
			}
		}
	}
	i.mu.Unlock()
	i.serverWriteMu.Unlock()
	i.answerInitializeWaiters(raw, waiters)
	return ensureNewline(raw)
}

// Frontend notifications are optional. Preserve an early notification, but
// only completeInitialize emits it and makes the upstream writable.
func (i *Instance) sendInitialized(raw []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.initializedSent && len(i.cachedInitialized) == 0 {
		i.cachedInitialized = append([]byte(nil), raw...)
	}
	return nil
}
