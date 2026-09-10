// Package mobilecore is the small gomobile-bindable facade used by Flutter.
// Complex protocol, crypto, and Relay behavior remain in the Go endpoint
// implementation; the UI exchanges versioned JSON DTOs through this boundary.
package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	endpoint "github.com/vual/work-anywhere/relay/pkg/endpoint/client"
)

const Contract = "mobile-control-v1"

type EventSink interface {
	OnEvent(eventJSON string)
}

type Core struct {
	mu     sync.Mutex
	client *endpoint.Client
	sink   EventSink
	closed bool
}

func NewCore() *Core {
	return &Core{client: endpoint.New()}
}

func (c *Core) SetEventSink(sink EventSink) {
	c.mu.Lock()
	c.sink = sink
	c.mu.Unlock()
}

// Pair consumes a waw://pair URI and keeps the resulting secure session alive
// until Close is called. Platform callers should invoke it off the UI thread.
func (c *Core) Pair(uri string) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	client := c.client
	c.mu.Unlock()
	if err := client.Pair(context.Background(), uri); err != nil {
		return failure("pair_failed", err.Error())
	}
	return success(client.Status())
}

func (c *Core) Restore(credentialsJSON string) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	client := c.client
	c.mu.Unlock()
	if err := client.Restore(context.Background(), []byte(credentialsJSON)); err != nil {
		return failure("restore_failed", err.Error())
	}
	return success(client.Status())
}

func (c *Core) ExportCredentials() string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	client := c.client
	c.mu.Unlock()
	credentials, err := client.ExportCredentials()
	if err != nil {
		return failure("credentials_unavailable", err.Error())
	}
	return success(string(credentials))
}

func (c *Core) Call(method, paramsJSON string, timeoutMilliseconds int64) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return failure("closed", "mobile core is closed")
	}
	client := c.client
	c.mu.Unlock()
	params := json.RawMessage(paramsJSON)
	if !json.Valid(params) {
		return failure("invalid_params", "paramsJSON must be valid JSON")
	}
	if timeoutMilliseconds <= 0 {
		timeoutMilliseconds = 30000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMilliseconds)*time.Millisecond)
	defer cancel()
	response, err := client.Call(ctx, method, params)
	if err != nil {
		var callError *endpoint.CallError
		if errors.As(err, &callError) {
			return failure(callError.Code, callError.Message)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return failure("request_timeout", err.Error())
		}
		return failure("call_failed", err.Error())
	}
	return string(response)
}

func (c *Core) Status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.client == nil {
		return success(map[string]any{"connected": false, "closed": true})
	}
	return success(c.client.Status())
}

func (c *Core) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	client := c.client
	c.sink = nil
	c.mu.Unlock()
	client.Close()
}

type resultEnvelope struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *fault `json:"error,omitempty"`
}

type fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func success(value any) string {
	raw, err := json.Marshal(resultEnvelope{OK: true, Result: value})
	if err != nil {
		return failure("encode_failed", err.Error())
	}
	return string(raw)
}

func failure(code, message string) string {
	if message == "" {
		message = errors.New(code).Error()
	}
	raw, _ := json.Marshal(resultEnvelope{OK: false, Error: &fault{Code: code, Message: message}})
	return string(raw)
}
