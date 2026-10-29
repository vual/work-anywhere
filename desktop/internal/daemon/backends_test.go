package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

type fakeBackend struct {
	notify   provider.Notifier
	long     string
	root     string
	turns    []provider.TurnRequest
	resolved map[string]json.RawMessage
	plans    []string
	settings provider.ConversationSettings
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
	return provider.ConversationMetadata{ID: "c1", AgentID: "fake:agent", Title: "Chat", Status: "running", ExternallyActive: true}, nil
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

func (f *fakeBackend) ProjectRoot(projectID, conversationID string) (string, bool) {
	return f.root, f.root != "" && (projectID == "p1" || conversationID == "c1")
}

func (*fakeBackend) CreateConversation(_ context.Context, projectID string) (provider.ConversationMetadata, error) {
	if projectID != "p1" {
		return provider.ConversationMetadata{}, provider.NewError("project_not_resolved", "project not found")
	}
	return provider.ConversationMetadata{ID: "new", ProjectID: "p1"}, nil
}

func (f *fakeBackend) StartTurn(_ context.Context, request provider.TurnRequest) (string, error) {
	f.turns = append(f.turns, request)
	return "turn-1", nil
}

func (*fakeBackend) InterruptTurn(context.Context, string) error { return nil }

func (*fakeBackend) Actions() []provider.Action {
	return []provider.Action{{ID: json.RawMessage(`"cc-1"`), Kind: "approval", Method: "claude/canUseTool/Bash", ConversationID: "c1", Detail: &provider.ActionDetail{Command: "go test"}}}
}

func (f *fakeBackend) ResolveAction(_ context.Context, id string, result json.RawMessage) error {
	if f.resolved == nil {
		f.resolved = map[string]json.RawMessage{}
	}
	if _, done := f.resolved[id]; done {
		return provider.NewError("already_resolved", "done")
	}
	f.resolved[id] = result
	return nil
}

func (*fakeBackend) PlanConfirmation(string) *provider.PlanConfirmation {
	return &provider.PlanConfirmation{PlanItemID: "toolu_plan", TurnID: "t1"}
}

func (f *fakeBackend) RespondPlan(_ context.Context, _, planItemID, decision, text string) error {
	f.plans = append(f.plans, planItemID+":"+decision+":"+text)
	return nil
}

func (f *fakeBackend) Settings(string) provider.ConversationSettings { return f.settings }

func (f *fakeBackend) UpdateSettings(_ context.Context, _ string, settings provider.ConversationSettings) (provider.ConversationSettings, error) {
	f.settings = settings
	return settings, nil
}

func (*fakeBackend) Models(context.Context) (provider.ModelCatalog, error) {
	return provider.ModelCatalog{Items: []provider.ModelCatalogItem{{ID: "default", Name: "Default", Default: true, Selectable: true}}}, nil
}

func (*fakeBackend) PermissionModes(context.Context) ([]provider.PermissionMode, error) {
	return []provider.PermissionMode{{ID: "default", Available: true}, {ID: "readOnly", Reason: "no"}, {ID: "fullAccess", Available: true, Dangerous: true}}, nil
}

func (*fakeBackend) Close() error { return nil }

func newBackendTestManager(t *testing.T) (*Manager, *fakeBackend) {
	t.Helper()
	manager := &Manager{ctx: context.Background(), syncEpoch: "epoch", contentRefs: make(map[string]mobileContentRef), subscribers: make(map[uint64]chan protocol.Event), uploads: make(map[string]*attachmentUpload), attachments: make(map[string]*attachmentUpload), imageEncode: make(chan struct{}, 1)}
	backend := &fakeBackend{long: strings.Repeat("x", mobileLargeBlockBytes+10), root: t.TempDir(), settings: provider.ConversationSettings{Mode: "default", ModelID: "default", PermissionModeID: "default"}}
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

func expectError(t *testing.T, response protocol.ControlResponse, code string) {
	t.Helper()
	if response.Error == nil || response.Error.Code != code {
		t.Fatalf("error = %#v, want %s", response.Error, code)
	}
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
	expectError(t, mobileCall(t, manager, "project.list", map[string]any{"agentId": "codex-missing"}), "invalid_target")
}

func TestBackendConversationSnapshotAndContent(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	snapshot := decodeResult[mobileConversationSnapshot](t, mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "c1"}))
	if !snapshot.Conversation.ExternallyActive || snapshot.Capabilities.Queue || snapshot.SyncEpoch != "epoch" || snapshot.Queue.Items == nil || snapshot.Settings.ModelID != "default" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.PlanConfirmation == nil || snapshot.PlanConfirmation.PlanItemID != "toolu_plan" {
		t.Fatalf("plan confirmation = %#v", snapshot.PlanConfirmation)
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
	expectError(t, mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "missing"}), "not_found")
	expectError(t, mobileCall(t, manager, "conversation.timeline.list", map[string]any{"agentId": "fake:agent", "threadId": "c1"}), "invalid_params")
}

func TestBackendEventsMapToMobileEvents(t *testing.T) {
	manager, backend := newBackendTestManager(t)
	events, cancel := manager.subscribe()
	defer cancel()
	backend.notify(provider.Event{Kind: provider.EventTurnStarted, ConversationID: "c1", TurnID: "t1"})
	backend.notify(provider.Event{Kind: provider.EventDelta, ConversationID: "c1", TurnID: "t1", ItemID: "msg:0", DeltaKind: provider.DeltaAssistantText, Delta: "Hi"})
	backend.notify(provider.Event{Kind: provider.EventDelta, ConversationID: "c1", TurnID: "t1", ItemID: "msg:1", DeltaKind: provider.DeltaReasoning, Delta: "think"})
	backend.notify(provider.Event{Kind: provider.EventActionRequested, ConversationID: "c1", TurnID: "t1"})
	backend.notify(provider.Event{Kind: provider.EventChanged, ConversationID: "c1"})
	backend.notify(provider.Event{Kind: provider.EventTurnCompleted, ConversationID: "c1", TurnID: "t1"})
	backend.notify(provider.Event{Kind: provider.EventChanged})
	want := []struct {
		name     string
		sequence int64
	}{{"provider.turn/started", 1}, {"provider.item/agentMessage/delta", 2}, {"provider.item/reasoning/textDelta", 3}, {"action.requested", 4}, {"provider.conversation.updated", 5}, {"provider.turn/completed", 6}, {"provider.conversations.changed", 0}}
	for _, expected := range want {
		event := <-events
		if event.Event != expected.name || event.Sequence != expected.sequence || event.InstanceID != "fake:agent" {
			t.Fatalf("event = %#v, want %#v", event, expected)
		}
		if expected.name == "provider.item/agentMessage/delta" {
			params := event.Payload.(map[string]any)["params"].(map[string]any)
			if params["threadId"] != "c1" || params["turnId"] != "t1" || params["itemId"] != "msg:0" || params["delta"] != "Hi" {
				t.Fatalf("delta payload = %#v", event.Payload)
			}
		}
	}
	// Deltas and invalidations advance only the sequence; turn and action
	// transitions advance the write revision.
	if version := manager.backendVersionOf("fake:agent", "c1"); version.sequence != 6 || version.revision != 3 {
		t.Fatalf("version = %#v", version)
	}
	stale := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.subscribe", map[string]any{"agentId": "fake:agent", "threadId": "c1", "cursor": 0}))
	if stale["snapshot"] != true || stale["sequence"].(float64) != 6 || stale["status"] != "running" {
		t.Fatalf("stale subscription = %#v", stale)
	}
	current := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.subscribe", map[string]any{"agentId": "fake:agent", "threadId": "c1", "cursor": 6}))
	if current["snapshot"] != false {
		t.Fatalf("current subscription = %#v", current)
	}
	snapshot := decodeResult[mobileConversationSnapshot](t, mobileCall(t, manager, "conversation.read", map[string]any{"agentId": "fake:agent", "threadId": "c1"}))
	if snapshot.Revision != 3 || snapshot.Sequence != 6 || snapshot.PlanConfirmation.Revision != 3 {
		t.Fatalf("snapshot version = %d/%d", snapshot.Revision, snapshot.Sequence)
	}
}

func TestBackendWritesAreRouted(t *testing.T) {
	manager, backend := newBackendTestManager(t)
	created := decodeResult[map[string]any](t, mobileCall(t, manager, "conversation.create", map[string]any{"agentId": "fake:agent", "projectId": "p1"}))
	if created["id"] != "new" || created["projectId"] != "p1" {
		t.Fatalf("created = %#v", created)
	}
	expectError(t, mobileCall(t, manager, "conversation.create", map[string]any{"agentId": "fake:agent", "projectId": "nope"}), "project_not_resolved")

	backend.notify(provider.Event{Kind: provider.EventTurnCompleted, ConversationID: "c1"})
	expectError(t, mobileCall(t, manager, "turn.start", map[string]any{"agentId": "fake:agent", "threadId": "c1", "text": "hi", "expectedRevision": 0}), "revision_conflict")
	expectError(t, mobileCall(t, manager, "turn.start", map[string]any{"agentId": "fake:agent", "threadId": "c1"}), "invalid_params")
	started := decodeResult[map[string]any](t, mobileCall(t, manager, "turn.start", map[string]any{"agentId": "fake:agent", "threadId": "c1", "text": "hi", "clientMessageId": "m1", "expectedRevision": 1, "settings": map[string]any{"mode": "plan", "modelId": "opus"}}))
	if started["turn"].(map[string]any)["id"] != "turn-1" || len(backend.turns) != 1 || backend.turns[0].ClientMessageID != "m1" || backend.turns[0].Settings.ModelID != "opus" {
		t.Fatalf("turn = %#v, requests %#v", started, backend.turns)
	}
	decodeResult[map[string]any](t, mobileCall(t, manager, "turn.interrupt", map[string]any{"agentId": "fake:agent", "threadId": "c1"}))

	actions := decodeResult[[]mobileAction](t, mobileCall(t, manager, "action.list", map[string]any{"agentId": "fake:agent"}))
	if len(actions) != 1 || actions[0].Detail == nil || actions[0].Detail.Command != "go test" {
		t.Fatalf("actions = %#v", actions)
	}
	decodeResult[map[string]any](t, mobileCall(t, manager, "action.resolve", map[string]any{"agentId": "fake:agent", "id": "cc-1", "result": map[string]any{"decision": "accept"}}))
	expectError(t, mobileCall(t, manager, "action.resolve", map[string]any{"agentId": "fake:agent", "id": "cc-1", "result": map[string]any{"decision": "accept"}}), "already_resolved")
	if string(backend.resolved["cc-1"]) != `{"decision":"accept"}` {
		t.Fatalf("resolved = %s", backend.resolved["cc-1"])
	}

	expectError(t, mobileCall(t, manager, "plan.respond", map[string]any{"agentId": "fake:agent", "threadId": "c1", "planItemId": "toolu_plan", "expectedRevision": 0, "decision": "revise", "text": "x"}), "revision_conflict")
	decodeResult[map[string]any](t, mobileCall(t, manager, "plan.respond", map[string]any{"agentId": "fake:agent", "threadId": "c1", "planItemId": "toolu_plan", "expectedRevision": 1, "decision": "revise", "text": " shorter "}))
	if len(backend.plans) != 1 || backend.plans[0] != "toolu_plan:revise:shorter" {
		t.Fatalf("plans = %#v", backend.plans)
	}

	settings := decodeResult[mobileConversationSettings](t, mobileCall(t, manager, "conversation.settings.update", map[string]any{"agentId": "fake:agent", "threadId": "c1", "settings": map[string]any{"mode": "default", "modelId": "sonnet", "permissionModeId": "autoReview"}}))
	if settings.ModelID != "sonnet" || backend.settings.PermissionModeID != "autoReview" {
		t.Fatalf("settings = %#v", settings)
	}
	models := decodeResult[mobileModelCatalog](t, mobileCall(t, manager, "catalog.models", map[string]any{"agentId": "fake:agent"}))
	if len(models.Items) != 1 || models.Items[0].ID != "default" {
		t.Fatalf("models = %#v", models)
	}
	permissions := decodeResult[map[string][]map[string]any](t, mobileCall(t, manager, "catalog.permissionProfiles", map[string]any{"agentId": "fake:agent"}))
	if len(permissions["items"]) != 3 || permissions["items"][1]["available"] != false || permissions["items"][2]["dangerous"] != true {
		t.Fatalf("permissions = %#v", permissions)
	}
	for _, method := range []string{"conversation.queue.add", "conversation.queue.start"} {
		expectError(t, mobileCall(t, manager, method, map[string]any{"agentId": "fake:agent", "threadId": "c1"}), "unsupported_capability")
	}
}

func TestBackendImageAttachmentsReachTheTurn(t *testing.T) {
	manager, backend := newBackendTestManager(t)
	begin := decodeResult[map[string]any](t, mobileCall(t, manager, "attachment.begin", map[string]any{"agentId": "fake:agent", "conversationId": "c1", "clientMessageId": "m1", "mimeType": "image/png", "fileName": "a.png", "size": 3}))
	id := begin["attachmentId"].(string)
	for _, step := range []struct {
		method string
		params map[string]any
	}{{"attachment.chunk", map[string]any{"attachmentId": id, "data": base64.StdEncoding.EncodeToString([]byte("png"))}}, {"attachment.commit", map[string]any{"attachmentId": id}}} {
		raw, _ := json.Marshal(step.params)
		if response := manager.control(protocol.ControlRequest{Type: "request", ID: "1", Method: step.method, Contract: protocol.MobileControlContract, Params: raw}); response.Error != nil {
			t.Fatalf("%s: %#v", step.method, response.Error)
		}
	}
	upload := manager.attachments[id]
	if upload == nil || upload.InstanceID != "fake:agent" || !strings.HasPrefix(upload.Path, backend.root) {
		t.Fatalf("upload = %#v", upload)
	}
	expectError(t, mobileCall(t, manager, "turn.start", map[string]any{"agentId": "fake:agent", "threadId": "other", "attachmentIds": []string{id}}), "invalid_params")
	decodeResult[map[string]any](t, mobileCall(t, manager, "turn.start", map[string]any{"agentId": "fake:agent", "threadId": "c1", "attachmentIds": []string{id}}))
	images := backend.turns[0].Images
	if len(images) != 1 || images[0].MimeType != "image/png" || string(images[0].Data) != "png" {
		t.Fatalf("images = %#v", images)
	}
	if _, err := os.Stat(upload.Path); !os.IsNotExist(err) {
		t.Fatalf("accepted attachment was not released: %v", err)
	}
	expectError(t, mobileCall(t, manager, "attachment.begin", map[string]any{"agentId": "fake:agent", "conversationId": "unknown", "clientMessageId": "m2", "mimeType": "image/png", "fileName": "b.png", "size": 1}), "project_not_resolved")
}
