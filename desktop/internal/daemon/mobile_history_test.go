package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

type historyCaller func(string, json.RawMessage) (json.RawMessage, error)

func (f historyCaller) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return f(method, params)
}

func fakeHistoryServer(t *testing.T, i *Instance, handler historyCaller) {
	t.Helper()
	i.manager.ctx = context.Background()
	r, w := io.Pipe()
	i.stdin = w
	i.state = "ready"
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var req struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			result, err := handler(req.Method, req.Params)
			response := map[string]any{"id": req.ID, "result": result}
			if err != nil {
				delete(response, "result")
				var fault any
				if json.Unmarshal([]byte(strings.TrimPrefix(err.Error(), "App Server: ")), &fault) != nil {
					fault = map[string]any{"code": -32603, "message": err.Error()}
				}
				response["error"] = fault
			}
			raw, _ := json.Marshal(response)
			i.handleServerFrame(i.generation, raw)
		}
	}()
	t.Cleanup(func() { _ = w.Close(); _ = r.Close(); <-done })
}

func TestConversationBusinessVersionIsProcessLocal(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	for _, event := range []string{"provider.mcpServer/startupStatus/updated", "provider.item/agentMessage/delta", "provider.unknown/future"} {
		i.broadcastEvent(event, map[string]any{"threadId": "t"})
	}
	if revision, sequence := i.ConversationVersion("t"); revision != 0 || sequence != 3 {
		t.Fatalf("versions %d/%d", revision, sequence)
	}
	i.broadcastEvent("provider.turn/started", map[string]any{"params": map[string]any{"thread": map[string]any{"id": "t"}}})
	if revision, sequence := i.ConversationVersion("t"); revision != 1 || sequence != 4 {
		t.Fatalf("versions after turn start %d/%d", revision, sequence)
	}
	i.mu.Lock()
	i.conversations = make(map[string]*conversationProjection)
	i.mu.Unlock()
	if revision, sequence := i.ConversationVersion("t"); revision != 0 || sequence != 0 {
		t.Fatalf("process-local versions were restored: %d/%d", revision, sequence)
	}
}

func TestConversationEventWindowAndProjectionCountAreBounded(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	for sequence := 0; sequence < 300; sequence++ {
		i.broadcastEvent("provider.item/agentMessage/delta", map[string]any{"threadId": "active", "delta": "x"})
	}
	i.mu.Lock()
	if got := len(i.conversations["active"].Events); got != 256 {
		i.mu.Unlock()
		t.Fatalf("event window = %d", got)
	}
	replayFloor := i.conversations["active"].ReplayFloor
	i.mu.Unlock()
	if replayFloor == 0 {
		t.Fatal("truncated event window did not advance the replay floor")
	}
	subscription := i.ConversationSubscription(json.RawMessage(`{"threadId":"active","cursor":0}`)).(map[string]any)
	if snapshot, _ := subscription["snapshot"].(bool); !snapshot {
		t.Fatal("cursor older than the replay window did not require a snapshot")
	}
	for index := 0; index < 300; index++ {
		i.ConversationVersion(fmt.Sprintf("thread-%03d", index))
	}
	i.mu.Lock()
	count := len(i.conversations)
	i.mu.Unlock()
	if count > 256 {
		t.Fatalf("projection count = %d", count)
	}
}

func TestProjectionLimitCountsOnlyInactiveConversations(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.mu.Lock()
	for index := 0; index < 50; index++ {
		id := fmt.Sprintf("active-%03d", index)
		i.activeTurns[id] = "turn"
		i.projectionLocked(id)
	}
	i.mu.Unlock()
	for index := 0; index < 300; index++ {
		i.ConversationVersion(fmt.Sprintf("inactive-%03d", index))
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if got := len(i.conversations); got != 306 {
		t.Fatalf("projection count = %d, want 50 pinned + 256 inactive", got)
	}
	for index := 0; index < 50; index++ {
		if i.conversations[fmt.Sprintf("active-%03d", index)] == nil {
			t.Fatalf("active projection %d was evicted", index)
		}
	}
}

func TestGlobalEventPayloadBudgetEvictsOldestReplayPrefix(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.manager.instances = map[string]*Instance{i.descriptor.ID: i}
	payload := strings.Repeat("x", 2<<20)
	for index := 0; index < 17; index++ {
		i.broadcastEvent("provider.item/agentMessage/delta", map[string]any{
			"threadId": "large",
			"delta":    payload,
		})
	}
	if got := i.manager.eventPayloadBytes.Load(); got > maxEventPayloadMemory {
		t.Fatalf("event payload bytes = %d", got)
	}
	i.mu.Lock()
	floor := i.conversations["large"].ReplayFloor
	i.mu.Unlock()
	if floor == 0 {
		t.Fatal("global payload budget did not evict the oldest replay prefix")
	}
	result := i.ConversationSubscription(json.RawMessage(`{"threadId":"large","cursor":0}`)).(map[string]any)
	if snapshot, _ := result["snapshot"].(bool); !snapshot {
		t.Fatal("evicted global replay prefix did not force a snapshot")
	}
}

func TestConcurrentControlStartsDispatchOnlyOneTurn(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.manager.instances = map[string]*Instance{i.descriptor.ID: i}
	var starts atomic.Int32
	fakeHistoryServer(t, i, func(method string, _ json.RawMessage) (json.RawMessage, error) {
		if method != "turn/start" {
			return nil, fmt.Errorf("unexpected %s", method)
		}
		starts.Add(1)
		return json.RawMessage(`{"turn":{"id":"turn"}}`), nil
	})
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := i.manager.control(protocol.ControlRequest{Method: "turn.start", Contract: protocol.MobileControlContract, Params: json.RawMessage(`{"agentId":"test-instance","threadId":"t","text":"hello","expectedRevision":0}`)})
			if r.Error == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || starts.Load() != 1 {
		t.Fatalf("accepted %d, dispatched %d", accepted.Load(), starts.Load())
	}
}

type rejectedHistoryWriter struct{}

func (rejectedHistoryWriter) Write([]byte) (int, error) { return 0, errors.New("stdin rejected write") }
func (rejectedHistoryWriter) Close() error              { return nil }

func TestPendingTurnReservationClearsAfterFailedWrite(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.state = "ready"
	i.stdin = rejectedHistoryWriter{}
	params := json.RawMessage(`{"threadId":"t","expectedRevision":0}`)
	if err := i.reserveTurn(params); err != nil {
		t.Fatal(err)
	}
	if i.CanStartTurn("t") {
		t.Fatal("reservation did not block start")
	}
	if _, err := i.Call(context.Background(), "turn/start", params); err == nil {
		t.Fatal("stdin failure should fail request")
	}
	if !i.CanStartTurn("t") {
		t.Fatal("failed write left reservation busy")
	}
	if len(i.requests) != 0 {
		t.Fatal("failed write left pending request")
	}
}

func TestRejectedInitialTurnRetainsEmptyThreadProvenance(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.rememberCreatedThread("t")
	fakeHistoryServer(t, i, func(string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New(`App Server: {"code":-32602,"message":"invalid input"}`)
	})
	params := json.RawMessage(`{"threadId":"t","expectedRevision":0}`)
	if err := i.reserveTurn(params); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Call(context.Background(), "turn/start", params); err == nil {
		t.Fatal("provider rejection should fail")
	}
	if !i.emptyCreatedThread("t") {
		t.Fatal("explicit rejection lost new thread provenance")
	}
}

func TestLegacyHistoryPaginationAndContent(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	items := []map[string]any{{"id": "a", "type": "agentMessage", "text": "first"}, {"id": "b", "type": "agentMessage", "text": "second"}, {"id": "c", "type": "agentMessage", "text": strings.Repeat("界", 30000)}}
	caller := historyCaller(func(method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "thread/items/list" {
			return nil, errors.New(`App Server: {"code":-32601,"message":"thread/items/list is not supported yet"}`)
		}
		if method != "thread/read" {
			t.Fatalf("unexpected %s", method)
		}
		raw, _ := json.Marshal(map[string]any{"thread": map[string]any{"id": "t", "turns": []any{map[string]any{"id": "turn", "items": items}}}})
		return raw, nil
	})
	page, err := m.readTimelinePage(caller, "agent", "t", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "c" || !page.Items[0].Blocks[0].Truncated {
		t.Fatalf("first page %#v, %v", page, err)
	}
	content, err := m.findTimelineBlock(caller, "t", "turn", "c", 0)
	if err != nil || content != items[2]["text"] {
		t.Fatalf("content lookup failed: %v", err)
	}
	next, err := m.readTimelinePage(caller, "agent", "t", page.NextCursor, 1)
	if err != nil || next.Items[0].ID != "b" {
		t.Fatalf("next page %#v %v", next, err)
	}
	if _, err = m.readTimelinePage(caller, "other", "t", page.NextCursor, 1); !historyError(err, "invalid_params") {
		t.Fatalf("cursor binding: %v", err)
	}
	items = items[:2]
	if _, err = m.readTimelinePage(caller, "agent", "t", page.NextCursor, 1); !historyError(err, "cursor_expired") {
		t.Fatalf("deleted anchor: %v", err)
	}
}

func TestHistoryDoesNotFallbackOnOrdinaryProviderErrors(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	calls := 0
	caller := historyCaller(func(string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New(`App Server: {"code":-32603,"message":"permission denied"}`)
	})
	_, err := m.readTimelinePage(caller, "agent", "t", "", 20)
	if err == nil || calls != 1 {
		t.Fatalf("ordinary error triggered fallback: %v, calls=%d", err, calls)
	}
}

func TestEmptyCreatedConversationAndQueueCapability(t *testing.T) {
	for _, queueUnsupported := range []bool{false, true} {
		t.Run(fmt.Sprint(queueUnsupported), func(t *testing.T) {
			i, _, cleanup := testInstance(t)
			defer cleanup()
			i.rememberCreatedThread("t")
			fakeHistoryServer(t, i, func(method string, params json.RawMessage) (json.RawMessage, error) {
				switch method {
				case "thread/read":
					var p struct {
						IncludeTurns bool `json:"includeTurns"`
					}
					_ = json.Unmarshal(params, &p)
					if !p.IncludeTurns {
						return json.RawMessage(`{"thread":{"id":"t","path":null,"status":{"type":"idle"}}}`), nil
					}
				case "thread/items/list":
					return nil, errors.New(`App Server: {"code":-32601,"message":"thread/items/list is not supported yet"}`)
				case "thread/queue/list":
					if queueUnsupported {
						return nil, errors.New(`App Server: {"code":-32601,"message":"Method not found"}`)
					}
				}
				return nil, errors.New(`App Server: {"code":-32603,"message":"no rollout found for thread id t"}`)
			})
			snapshot, err := i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"t"}`))
			if err != nil || len(snapshot.Timeline.Items) != 0 || snapshot.Capabilities.Queue == queueUnsupported {
				t.Fatalf("empty snapshot %#v %v", snapshot, err)
			}
			i.mu.Lock()
			delete(i.createdThreads, "t")
			i.mu.Unlock()
			if _, err = i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"t"}`)); err == nil {
				t.Fatal("no-rollout error hidden for existing conversation")
			}
		})
	}
}

func TestSnapshotRejectsRepeatedBusinessChanges(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	var reads atomic.Int32
	fakeHistoryServer(t, i, func(method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "thread/read" {
			reads.Add(1)
			i.broadcastEvent("provider.thread/settings/updated", map[string]any{"threadId": "t"})
			return json.RawMessage(`{"thread":{"id":"t","status":{"type":"idle"}}}`), nil
		}
		return json.RawMessage(`{"data":[]}`), nil
	})
	_, err := i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"t"}`))
	if !historyError(err, "snapshot_unstable") || reads.Load() != 3 {
		t.Fatalf("snapshot retry bound: reads=%d err=%v", reads.Load(), err)
	}
}

func TestMetadataOnlyRolloutNeverHidesConversationContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if !metadataOnlyRollout(path) {
		t.Fatal("missing rollout should qualify")
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\",\"payload\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !metadataOnlyRollout(path) {
		t.Fatal("metadata-only rollout should qualify")
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\"}\n{\"type\":\"response_item\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if metadataOnlyRollout(path) {
		t.Fatal("content rollout must not qualify")
	}
}

func TestHistoryBackendCacheIsPerThreadAndGeneration(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	var nativeCalls atomic.Int32
	fakeHistoryServer(t, i, func(method string, params json.RawMessage) (json.RawMessage, error) {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &p)
		if method == "thread/items/list" {
			nativeCalls.Add(1)
			if p.ThreadID == "legacy" {
				return nil, errors.New(`App Server: {"code":-32601,"message":"unsupported"}`)
			}
			return json.RawMessage(`{"data":[]}`), nil
		}
		return json.RawMessage(`{"thread":{"id":"legacy","turns":[]}}`), nil
	})
	for _, threadID := range []string{"legacy", "legacy", "native"} {
		if _, err := i.manager.readTimelinePage(i, i.descriptor.ID, threadID, "", 20); err != nil {
			t.Fatal(err)
		}
	}
	if nativeCalls.Load() != 2 {
		t.Fatal("backend was not cached per thread")
	}
	i.mu.Lock()
	i.generation++
	i.mu.Unlock()
	if _, err := i.manager.readTimelinePage(i, i.descriptor.ID, "legacy", "", 20); err != nil {
		t.Fatal(err)
	}
	if nativeCalls.Load() != 3 {
		t.Fatal("backend was not reprobed after generation change")
	}
}
