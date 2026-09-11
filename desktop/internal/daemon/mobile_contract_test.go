package daemon

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/store"
	_ "modernc.org/sqlite"
)

type recordingMobileCaller struct {
	limits []int
	body   json.RawMessage
}

func (c *recordingMobileCaller) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != "thread/items/list" {
		return nil, errors.New("unexpected method")
	}
	var request struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(params, &request)
	c.limits = append(c.limits, request.Limit)
	return c.body, nil
}

func TestControlRejectsUnknownContract(t *testing.T) {
	manager := &Manager{}
	response := manager.control(protocol.ControlRequest{Type: "request", ID: "1", Contract: "mobile-control-v2", Method: "system.status"})
	if response.Contract != "mobile-control-v2" || response.Error == nil || response.Error.Code != "unsupported_contract" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestNormalizeConversationMetadataAndTimeline(t *testing.T) {
	metadata, err := normalizeConversationMetadata("agent-1", json.RawMessage(`{"thread":{"id":"t1","name":"Title","status":{"active":{}},"createdAt":1,"updatedAt":2,"turns":[{"items":[{"text":"must not pass through"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "t1" || metadata.AgentID != "agent-1" || metadata.Title != "Title" || !metadata.Running {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	page, _, err := normalizeTimelinePage(json.RawMessage(`{"data":[{"id":"i2","type":"agentMessage","text":"new"},{"id":"i1","turnId":"turn-1","type":"userMessage","content":[{"type":"text","text":"old"}]}],"nextCursor":"opaque"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "i2" || page.Items[1].TurnID != "turn-1" || page.NextCursor != "opaque" || page.Items[1].Blocks[0].Text != "old" {
		t.Fatalf("unexpected timeline: %#v", page)
	}
	nested, _, err := normalizeTimelinePage(json.RawMessage(`{"data":[{"turnId":"turn-2","item":{"id":"i3","type":"agentMessage","text":"nested"}}]}`))
	if err != nil || len(nested.Items) != 1 || nested.Items[0].ID != "i3" || nested.Items[0].TurnID != "turn-2" || nested.Items[0].Blocks[0].Text != "nested" {
		t.Fatalf("nested thread item was not normalized: page=%#v err=%v", nested, err)
	}
}

func TestMobileConversationSubscriptionDoesNotReplayLargePayloads(t *testing.T) {
	instance := &Instance{
		descriptor:    protocol.InstanceDescriptor{ID: "agent-1"},
		conversations: make(map[string]*conversationProjection),
	}
	projection := &conversationProjection{ID: "thread-1", Sequence: 1000, Revision: 1000}
	for sequence := int64(1); sequence <= 1000; sequence++ {
		projection.Events = append(projection.Events, protocol.Event{
			Type: "event", Sequence: sequence, Event: "provider.item",
			InstanceID: "agent-1", ConversationID: "thread-1",
			Payload: strings.Repeat("x", 2048),
		})
	}
	instance.conversations["thread-1"] = projection

	result, ok := instance.MobileConversationSubscription(json.RawMessage(`{"threadId":"thread-1","cursor":0}`)).(map[string]any)
	if !ok || result["snapshot"] != true {
		t.Fatalf("large replay should request a snapshot: %#v", result)
	}
	events, ok := result["events"].([]protocol.Event)
	if !ok || len(events) != 0 {
		t.Fatalf("large replay should not include events: %#v", result["events"])
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) >= mobileControlResponseBytes {
		t.Fatalf("mobile subscription response remains too large: %d bytes", len(encoded))
	}
}

func TestTimelinePageHalvesLimitAndReferencesOversizedSingleItem(t *testing.T) {
	content := make([]map[string]any, 10)
	for index := range content {
		content[index] = map[string]any{"type": "text", "text": strings.Repeat("界", 20000)}
	}
	body, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"id": "large", "type": "agentMessage", "content": content}}})
	caller := &recordingMobileCaller{body: body}
	manager := &Manager{ctx: context.Background(), contentRefs: make(map[string]mobileContentRef)}
	page, err := manager.readTimelinePage(caller, "agent-1", "thread-1", "opaque", 30)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{30, 15, 7, 3, 1}
	if fmt.Sprint(caller.limits) != fmt.Sprint(want) {
		t.Fatalf("limits = %v, want %v", caller.limits, want)
	}
	encoded, _ := json.Marshal(page)
	if len(encoded) > mobileControlResponseBytes || len(page.Items) != 1 || page.Items[0].Blocks[0].ContentRef == "" {
		t.Fatalf("oversized item was not safely referenced: bytes=%d page=%#v", len(encoded), page)
	}
}

func TestContentReferencesExpireAndAreBoundToConversation(t *testing.T) {
	now := time.Now()
	manager := &Manager{contentRefs: map[string]mobileContentRef{
		"valid":   {AgentID: "a1", ThreadID: "t1", ExpiresAt: now.Add(time.Minute)},
		"expired": {AgentID: "a1", ThreadID: "t1", ExpiresAt: now.Add(-time.Second)},
	}}
	if _, err := manager.resolveContentRef("valid", "a1", "t1", now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ token, agent, thread string }{
		{"valid", "a1", "other"}, {"expired", "a1", "t1"}, {"forged", "a1", "t1"},
	} {
		_, err := manager.resolveContentRef(test.token, test.agent, test.thread, now)
		var contractErr *mobileContractError
		if !errors.As(err, &contractErr) || contractErr.code != "content_ref_expired" {
			t.Fatalf("%+v returned %v", test, err)
		}
	}
}

func TestUTF8ContentChunksRestoreCompleteText(t *testing.T) {
	want := strings.Repeat("A界🙂", 60000)
	var restored strings.Builder
	offset := 0
	for {
		chunk, next, more, err := utf8ContentChunk(want, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk) > mobileContentChunkBytes || next <= offset {
			t.Fatalf("invalid chunk boundaries: offset=%d next=%d bytes=%d", offset, next, len(chunk))
		}
		restored.WriteString(chunk)
		offset = next
		if !more {
			break
		}
	}
	if restored.String() != want {
		t.Fatal("UTF-8 chunks did not restore the original content")
	}
}

func TestLargeTimelineBlockUsesBoundContentReference(t *testing.T) {
	text := strings.Repeat("界", mobileLargeBlockBytes)
	page := mobileTimelinePage{Items: []mobileTimelineItem{{ID: "item-1", Kind: "commandExecution", Blocks: []mobileTimelineBlock{{Kind: "output", Text: text}}}}}
	manager := &Manager{contentRefs: make(map[string]mobileContentRef)}
	manager.applyContentRefs(&page, "agent-1", "thread-1", "opaque", 30, false)
	block := page.Items[0].Blocks[0]
	if !block.Truncated || block.ContentRef == "" || block.TotalBytes != len(text) || len(block.Text) > mobileInlinePreviewBytes || !json.Valid([]byte(`"`+block.Text+`"`)) {
		t.Fatalf("unexpected block: %#v", block)
	}
	manager.contentRefMu.Lock()
	ref := manager.contentRefs[block.ContentRef]
	manager.contentRefMu.Unlock()
	if ref.AgentID != "agent-1" || ref.ThreadID != "thread-1" || ref.ItemID != "item-1" || ref.BlockIndex != 0 {
		t.Fatalf("unexpected reference binding: %#v", ref)
	}
}

func TestTimelineLimit(t *testing.T) {
	if got, _ := timelineLimit(0); got != 30 {
		t.Fatalf("default limit = %d", got)
	}
	if _, err := timelineLimit(51); err == nil {
		t.Fatal("expected max-limit validation error")
	}
}

func TestNormalizeProjectList(t *testing.T) {
	page, err := normalizeProjectList("agent-1", json.RawMessage(`{"data":[{"id":"p1","name":"work-anywhere","roots":[{"path":"D:/CodeSpace/work-anywhere"}],"updatedAt":42}],"nextCursor":"more"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AgentID != "agent-1" || page.Items[0].Root != "D:/CodeSpace/work-anywhere" || page.NextCursor != "more" {
		t.Fatalf("unexpected page: %#v", page)
	}
}

func TestMobileControlTargetTranslatesPublicParameters(t *testing.T) {
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}
	manager := &Manager{instances: map[string]*Instance{"agent-1": instance}}

	target, params, err := manager.mobileControlTarget(json.RawMessage(`{"agentId":"agent-1","query":"redis","limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if target != instance {
		t.Fatal("resolved the wrong instance")
	}
	var values map[string]any
	if err := json.Unmarshal(params, &values); err != nil {
		t.Fatal(err)
	}
	if values["searchTerm"] != "redis" || values["limit"] != float64(10) {
		t.Fatalf("unexpected translated params: %#v", values)
	}
	if _, exists := values["agentId"]; exists {
		t.Fatal("agentId leaked through the provider boundary")
	}
	if _, exists := values["instanceId"]; exists {
		t.Fatal("instanceId leaked through the provider boundary")
	}
}

func TestNormalizeConversationList(t *testing.T) {
	page, err := normalizeConversationList("agent-1", json.RawMessage(`{"data":[{"id":"t1","projectId":"p1","name":null,"preview":"First line\nSecond line","status":{"active":{"activeFlags":["waitingOnApproval"]}},"createdAt":1,"updatedAt":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "First line" || !page.Items[0].Running || page.Items[0].Status != "waitingOnApproval" {
		t.Fatalf("unexpected page: %#v", page)
	}
}

func TestNormalizeConversationCreate(t *testing.T) {
	result, err := normalizeConversationCreate(json.RawMessage(`{"thread":{"id":"t-new","projectId":"p1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result["id"] != "t-new" || result["projectId"] != "p1" {
		t.Fatalf("unexpected create result: %#v", result)
	}
	if _, err := normalizeConversationCreate(json.RawMessage(`{"thread":{}}`)); err == nil {
		t.Fatal("expected missing thread id to be rejected")
	}
}

func TestMobileAttachmentBeginKeepsResolvedOwner(t *testing.T) {
	root := t.TempDir()
	state, err := store.Open(filepath.Join(root, "state.db"), filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}
	instance.cwd = root
	manager := &Manager{
		ctx:       context.Background(),
		store:     state,
		instances: map[string]*Instance{"agent-1": instance},
		uploads:   make(map[string]*attachmentUpload),
	}
	response := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "1",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.begin",
		Params:   json.RawMessage(`{"agentId":"agent-1","conversationId":"thread-1","clientMessageId":"message-1","mimeType":"image/jpeg","fileName":"photo.jpg","size":10}`),
	})
	if response.Error != nil {
		t.Fatalf("attachment begin failed: %+v", response.Error)
	}
	result, ok := response.Result.(map[string]any)
	if !ok || result["attachmentId"] == "" {
		t.Fatalf("unexpected attachment result: %#v", response.Result)
	}
	attachmentID := result["attachmentId"].(string)
	chunkResponse := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "2",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.chunk",
		Params:   json.RawMessage(fmt.Sprintf(`{"attachmentId":%q,"data":%q}`, attachmentID, base64.StdEncoding.EncodeToString([]byte("image-data")))),
	})
	if chunkResponse.Error != nil {
		t.Fatalf("attachment chunk failed: %+v", chunkResponse.Error)
	}
	commitResponse := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "3",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.commit",
		Params:   json.RawMessage(fmt.Sprintf(`{"attachmentId":%q}`, attachmentID)),
	})
	if commitResponse.Error != nil {
		t.Fatalf("attachment commit failed: %+v", commitResponse.Error)
	}
	attachment, err := state.ReadAttachment(attachmentID)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.InstanceID != "agent-1" || attachment.ConversationID != "thread-1" || attachment.MessageID != "message-1" || attachment.MimeType != "image/jpeg" {
		t.Fatalf("unexpected attachment metadata: %#v", attachment)
	}
	wantPath := filepath.Join(root, remoteAttachmentDirectory, "thread-1", "message-1")
	if filepath.Dir(attachment.Path) != wantPath || filepath.Ext(attachment.Path) != ".jpg" {
		t.Fatalf("attachment was stored at the wrong path: %s", attachment.Path)
	}
	content, err := os.ReadFile(attachment.Path)
	if err != nil || string(content) != "image-data" {
		t.Fatalf("stored attachment content mismatch: %q, %v", content, err)
	}
	database, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var encrypted []byte
	if err = database.QueryRow(`SELECT content_encrypted FROM attachments WHERE id=?`, attachmentID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if encrypted != nil {
		t.Fatal("attachment content was stored in the database")
	}
}

func TestMobileAttachmentRejectsUnsafePathAndCleansPartialUpload(t *testing.T) {
	root := t.TempDir()
	state, err := store.Open(filepath.Join(root, "state.db"), filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}, cwd: root}
	manager := &Manager{
		ctx:       context.Background(),
		store:     state,
		instances: map[string]*Instance{"agent-1": instance},
		uploads:   make(map[string]*attachmentUpload),
	}
	unsafe := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "unsafe",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.begin",
		Params:   json.RawMessage(`{"agentId":"agent-1","conversationId":"../outside","clientMessageId":"message-1","mimeType":"image/jpeg","fileName":"photo.jpg","size":3}`),
	})
	if unsafe.Error == nil || unsafe.Error.Code != "invalid_params" {
		t.Fatalf("expected unsafe path rejection, got %+v", unsafe.Error)
	}

	begin := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "begin",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.begin",
		Params:   json.RawMessage(`{"agentId":"agent-1","conversationId":"thread-1","clientMessageId":"message-1","mimeType":"image/jpeg","fileName":"photo.jpg","size":4}`),
	})
	if begin.Error != nil {
		t.Fatalf("attachment begin failed: %+v", begin.Error)
	}
	attachmentID := begin.Result.(map[string]any)["attachmentId"].(string)
	chunk := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "chunk",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.chunk",
		Params:   json.RawMessage(fmt.Sprintf(`{"attachmentId":%q,"data":%q}`, attachmentID, base64.StdEncoding.EncodeToString([]byte("abc")))),
	})
	if chunk.Error != nil {
		t.Fatalf("attachment chunk failed: %+v", chunk.Error)
	}
	commit := manager.control(protocol.ControlRequest{
		Type:     "request",
		ID:       "commit",
		Contract: protocol.MobileControlContract,
		Method:   "attachment.commit",
		Params:   json.RawMessage(fmt.Sprintf(`{"attachmentId":%q}`, attachmentID)),
	})
	if commit.Error == nil {
		t.Fatal("expected size mismatch")
	}
	if _, err = os.Stat(filepath.Join(root, remoteAttachmentDirectory, "thread-1", "message-1")); err == nil {
		entries, readErr := os.ReadDir(filepath.Join(root, remoteAttachmentDirectory, "thread-1", "message-1"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("partial upload was not removed: %v", entries)
		}
	}
}

func TestNormalizeTurnStartRejectsNonImageAttachment(t *testing.T) {
	root := t.TempDir()
	state, err := store.Open(filepath.Join(root, "state.db"), filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	path := filepath.Join(root, "document.pdf")
	if err = os.WriteFile(path, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = state.SaveAttachmentMetadata(store.Attachment{ID: "pdf-1", InstanceID: "agent-1", ConversationID: "thread-1", MessageID: "message-1", MimeType: "application/pdf", FileName: "document.pdf", Path: path, Size: 3}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: state, instances: map[string]*Instance{"agent-1": {descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}}}
	_, err = manager.normalizeTurnStart("agent-1", json.RawMessage(`{"threadId":"thread-1","attachmentIds":["pdf-1"]}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported_attachment_type") {
		t.Fatalf("expected unsupported attachment error, got %v", err)
	}
}
