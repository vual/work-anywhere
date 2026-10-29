package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

type fakeBackend struct {
	notify provider.Notifier
	long   string
}

func (*fakeBackend) Agent() provider.AgentSummary {
	return provider.AgentSummary{ID: "fake:agent", Name: "Fake", Kind: "fake", Status: "ready", Capabilities: &provider.Capabilities{History: true}}
}

func (f *fakeBackend) Start(_ context.Context, notify provider.Notifier) error {
	f.notify = notify
	return nil
}

func (*fakeBackend) ListProjects(context.Context) ([]provider.ProjectSummary, error) {
	return []provider.ProjectSummary{{ID: "p2", AgentID: "fake:agent", Name: "two", UpdatedAt: 20}, {ID: "p1", AgentID: "fake:agent", Name: "one", UpdatedAt: 10}}, nil
}

func (*fakeBackend) ListConversations(_ context.Context, query provider.ConversationQuery) (provider.Page[provider.ConversationSummary], error) {
	return provider.Page[provider.ConversationSummary]{Items: []provider.ConversationSummary{{ID: "c1", AgentID: "fake:agent", ProjectID: query.ProjectID, Title: query.Query}}}, nil
}

func (*fakeBackend) ReadConversation(_ context.Context, id string) (provider.ConversationMetadata, error) {
	if id != "c1" {
		return provider.ConversationMetadata{}, provider.NewError("not_found", "conversation not found")
	}
	return provider.ConversationMetadata{ID: "c1", AgentID: "fake:agent", Title: "Chat", Status: "idle", ExternallyActive: true}, nil
}

func (f *fakeBackend) Timeline(_ context.Context, id, cursor string, limit int) (provider.TimelinePage, error) {
	if cursor != "" {
		return provider.TimelinePage{Items: []provider.TimelineItem{}}, nil
	}
	return provider.TimelinePage{Items: []provider.TimelineItem{
		{ID: "i2", TurnID: "t1", Kind: "agentMessage", Blocks: []provider.TimelineBlock{{Kind: "text", Text: f.long}}},
		{ID: "i1", TurnID: "t1", Kind: "userMessage", Blocks: []provider.TimelineBlock{{Kind: "text", Text: "hi"}}},
	}[:min(limit, 2)], NextCursor: "older"}, nil
}

func (f *fakeBackend) TimelineBlock(_ context.Context, id, turnID, itemID string, blockIndex int) (string, error) {
	if id == "c1" && itemID == "i2" && blockIndex == 0 {
		return f.long, nil
	}
	return "", provider.NewError("content_not_found", "missing")
}

func (*fakeBackend) Close() error { return nil }

func newBackendTestManager(t *testing.T) (*Manager, *fakeBackend) {
	t.Helper()
	manager := &Manager{ctx: context.Background(), syncEpoch: "epoch", contentRefs: make(map[string]mobileContentRef), subscribers: make(map[uint64]chan protocol.Event)}
	backend := &fakeBackend{long: strings.Repeat("x", mobileLargeBlockBytes+10)}
	if err := manager.registerBackend(backend); err != nil {
		t.Fatal(err)
	}
	return manager, backend
}

func mobileCall(t *testing.T, manager *Manager, method string, params map[string]any) protocol.ControlResponse {
	t.Helper()
	raw, _ := json.Marshal(params)
	response := manager.control(protocol.ControlRequest{Type: "request", ID: "1", Method: method, Contract: protocol.MobileControlContract, Params: raw})
	encoded, _ := json.Marshal(response.Result)
	response.Result = json.RawMessage(encoded)
	return response
}

func decodeResult[T any](t *testing.T, response protocol.ControlResponse) T {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("unexpected error %#v", response.Error)
	}
	var value T
	if err := json.Unmarshal(response.Result.(json.RawMessage), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBackendAgentIsListedAndRouted(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	agents := decodeResult[[]mobileAgentSummary](t, mobileCall(t, manager, "agent.list", nil))
	if len(agents) != 1 || agents[0].ID != "fake:agent" || agents[0].Capabilities == nil || !agents[0].Capabilities.History {
		t.Fatalf("agents = %#v", agents)
	}

	first := decodeResult[mobilePage[mobileProjectSummary]](t, mobileCall(t, manager, "project.list", map[string]any{"agentId": "fake:agent", "limit": 1}))
	if len(first.Items) != 1 || first.Items[0].ID != "p2" || first.NextCursor == "" {
		t.Fatalf("first project page = %#v", first)
	}
	second := decodeResult[mobilePage[mobileProjectSummary]](t, mobileCall(t, manager, "project.list", map[string]any{"agentId": "fake:agent", "limit": 1, "cursor": first.NextCursor}))
	if len(second.Items) != 1 || second.Items[0].ID != "p1" || second.NextCursor != "" {
		t.Fatalf("second project page = %#v", second)
	}

	conversations := decodeResult[mobilePage[mobileConversationSummary]](t, mobileCall(t, manager, "conversation.list", map[string]any{"agentId": "fake:agent", "projectId": "p1", "query": "needle"}))
	if len(conversations.Items) != 1 || conversations.Items[0].ProjectID != "p1" || conversations.Items[0].Title != "needle" {
		t.Fatalf("conversations = %#v", conversations)
	}

	// An agentId that belongs to no backend keeps using the Codex path.
	if response := mobileCall(t, manager, "project.list", map[string]any{"agentId": "codex-missing"}); response.Error == nil || response.Error.Code != "invalid_target" {
		t.Fatalf("unknown agent response = %#v", response.Error)
	}
}

func TestBackendConversationSnapshotAndContent(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	snapshot := decodeResult[mobileConversationSnapshot](t, mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "c1"}))
	if !snapshot.Conversation.ExternallyActive || snapshot.Capabilities.Queue || snapshot.SyncEpoch != "epoch" || snapshot.Queue.Items == nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.Timeline.Items) != 2 || snapshot.Timeline.NextCursor != "older" {
		t.Fatalf("timeline = %#v", snapshot.Timeline)
	}
	block := snapshot.Timeline.Items[0].Blocks[0]
	if !block.Truncated || block.ContentRef == "" || block.TotalBytes != mobileLargeBlockBytes+10 {
		t.Fatalf("large block was not referenced: %#v", block)
	}
	content := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.content.read", map[string]any{"agentId": "fake:agent", "threadId": "c1", "contentRef": block.ContentRef}))
	if content["totalBytes"].(float64) != float64(mobileLargeBlockBytes+10) || content["hasMore"] != false {
		t.Fatalf("content = %#v", content)
	}
	resolved := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.content.resolve", map[string]any{"agentId": "fake:agent", "threadId": "c1", "turnId": "t1", "itemId": "i2", "blockIndex": 0}))
	if resolved["contentRef"] == "" {
		t.Fatalf("resolve = %#v", resolved)
	}
	if response := mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "missing"}); response.Error == nil || response.Error.Code != "not_found" {
		t.Fatalf("missing conversation error = %#v", response.Error)
	}
	if response := mobileCall(t, manager, "conversation.timeline.list", map[string]any{"agentId": "fake:agent", "threadId": "c1"}); response.Error == nil || response.Error.Code != "invalid_params" {
		t.Fatalf("timeline without cursor = %#v", response.Error)
	}
}

func TestBackendChangesAdvanceSubscriptionAndPublishEvents(t *testing.T) {
	manager, backend := newBackendTestManager(t)
	events, cancel := manager.subscribe()
	defer cancel()
	backend.notify("c1")
	backend.notify("")
	first, second := <-events, <-events
	if first.Event != "provider.conversation.updated" || first.InstanceID != "fake:agent" || first.ConversationID != "c1" || first.Sequence != 1 {
		t.Fatalf("conversation event = %#v", first)
	}
	if second.Event != "provider.conversations.changed" || second.ConversationID != "" {
		t.Fatalf("list event = %#v", second)
	}
	stale := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.subscribe", map[string]any{"agentId": "fake:agent", "threadId": "c1", "cursor": 0}))
	if stale["snapshot"] != true || stale["sequence"].(float64) != 1 || stale["revision"].(float64) != 1 {
		t.Fatalf("stale subscription = %#v", stale)
	}
	current := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.subscribe", map[string]any{"agentId": "fake:agent", "threadId": "c1", "cursor": 1}))
	if current["snapshot"] != false {
		t.Fatalf("current subscription = %#v", current)
	}
	snapshot := decodeResult[mobileConversationSnapshot](t, mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "c1"}))
	if snapshot.Revision != 1 || snapshot.Sequence != 1 {
		t.Fatalf("snapshot version = %d/%d", snapshot.Revision, snapshot.Sequence)
	}
}

func TestBackendRejectsUnsupportedWrites(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	for _, method := range []string{"turn.start", "turn.interrupt", "conversation.create", "action.resolve", "plan.respond", "conversation.settings.update", "conversation.queue.add", "attachment.begin"} {
		response := mobileCall(t, manager, method, map[string]any{"agentId": "fake:agent", "threadId": "c1"})
		if response.Error == nil || response.Error.Code != "unsupported_capability" {
			t.Fatalf("%s error = %#v", method, response.Error)
		}
	}
	actions := decodeResult[[]mobileAction](t, mobileCall(t, manager, "action.list", map[string]any{"agentId": "fake:agent"}))
	if len(actions) != 0 {
		t.Fatalf("actions = %#v", actions)
	}
}
