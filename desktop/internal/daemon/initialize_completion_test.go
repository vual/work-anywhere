package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/jsonrpc"
)

type handshakeWriter struct {
	entered chan struct{}
	release chan struct{}
	err     error
	short   bool
}

func (w *handshakeWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	if w.err != nil {
		return 0, w.err
	}
	if w.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func (*handshakeWriter) Close() error { return nil }

func TestInitializeWaitsForUpstreamNotificationWrite(t *testing.T) {
	for _, failure := range []string{"", "write", "short"} {
		t.Run(failure, func(t *testing.T) {
			i, buffer, cleanup := testInstance(t)
			defer cleanup()
			a, b := compatDesktop(i, "a"), compatDesktop(i, "b")
			init := []byte(`{"id":1,"method":"initialize","trace":{"future":true},"params":{"capabilities":{"extensions":{"unknown":true}}}}`)
			if err := i.HandleDesktop(a, init); err != nil {
				t.Fatal(err)
			}
			req, _ := jsonrpc.Parse(buffer.Bytes())
			w := &handshakeWriter{entered: make(chan struct{}), release: make(chan struct{}), short: failure == "short"}
			if failure == "write" {
				w.err = errors.New("test write failed")
			}
			i.mu.Lock()
			i.stdin = w
			i.mu.Unlock()
			done := make(chan struct{})
			go func() {
				defer close(done)
				i.handleServerFrame(0, []byte(fmt.Sprintf(`{"id":%s,"result":{"future":true}}`, req.ID)))
			}()
			select {
			case <-w.entered:
			case <-time.After(time.Second):
				t.Fatal("no initialized write")
			}
			i.mu.Lock()
			published := len(i.cachedInitializeResponse) > 0 || !i.initializeInFlight
			i.mu.Unlock()
			if published || i.CanStartTurn("thread") || len(a.outbound) > 0 {
				t.Fatal("published success before handshake finished")
			}
			if err := i.HandleDesktop(b, bytes.Replace(init, []byte(`"id":1`), []byte(`"id":2`), 1)); err != nil {
				t.Fatal(err)
			}
			if len(b.outbound) > 0 {
				t.Fatal("concurrent frontend received premature success")
			}
			close(w.release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("completion blocked")
			}
			for _, c := range []*clientConn{a, b} {
				m := compatResponse(t, c)
				if (len(m.Error) > 0) != (failure != "") {
					t.Fatalf("response: %+v", m)
				}
			}
			if failure == "" {
				if !i.CanStartTurn("thread") {
					t.Fatal("successful handshake did not open writes")
				}
				c := compatDesktop(i, "reconnect")
				if err := i.HandleDesktop(c, init); err != nil {
					t.Fatal(err)
				}
				if len(compatResponse(t, c).Error) > 0 {
					t.Fatal("reconnect failed")
				}
			} else {
				if i.Snapshot().State != "error" || i.CanStartTurn("thread") || !strings.Contains(i.Snapshot().LastError, "initialized write failed") {
					t.Fatal("write failure treated as initialized")
				}
				i.mu.Lock()
				i.stdin = buffer
				i.mu.Unlock()
				buffer.Reset()
				if err := i.HandleDesktop(a, init); err != nil {
					t.Fatal(err)
				}
				if buffer.Len() == 0 {
					t.Fatal("retry was not forwarded")
				}
			}
		})
	}
}

func TestEarlyInitializedAndOldGeneration(t *testing.T) {
	i, w, cleanup := testInstance(t)
	defer cleanup()
	c := compatDesktop(i, "a")
	_ = i.HandleDesktop(c, []byte(`{"method":"initialized","params":{"future":true}}`))
	if w.Len() != 0 || i.CanStartTurn("thread") {
		t.Fatal("early notification made instance ready")
	}
	i.activeTurns["busy"] = "turn"
	i.completeInitialize(0, []byte(`{"id":1,"result":{}}`))
	if i.state != "active" || !strings.Contains(w.String(), `"future":true`) || strings.Count(w.String(), "initialized") != 1 {
		t.Fatal("early notification or activity lost")
	}
	i.generation++
	i.state = "waiting_initialize"
	i.initializedSent = false
	i.cachedInitializeResponse = nil
	w.Reset()
	if response := i.completeInitialize(0, []byte(`{"id":1,"result":{}}`)); response != nil || w.Len() != 0 || i.state != "waiting_initialize" {
		t.Fatal("old generation completed new handshake")
	}
}
