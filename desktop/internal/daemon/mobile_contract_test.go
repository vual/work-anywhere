package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/store"
	_ "modernc.org/sqlite"
)

type recordingMobileCaller struct {
	limits []int
	body   json.RawMessage
}

type pagedMobileCaller struct {
	limits  []int
	cursors []string
	turnIDs []string
	pages   map[string]json.RawMessage
}

type projectRecencyCaller struct {
	projects      json.RawMessage
	conversations json.RawMessage
}

func (c *projectRecencyCaller) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "project/list":
		return c.projects, nil
	case "thread/list":
		return c.conversations, nil
	default:
		return nil, fmt.Errorf("unexpected method %q", method)
	}
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

func (c *pagedMobileCaller) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != "thread/items/list" {
		return nil, errors.New("unexpected method")
	}
	var request struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
		TurnID string `json:"turnId"`
	}
	_ = json.Unmarshal(params, &request)
	c.limits = append(c.limits, request.Limit)
	c.cursors = append(c.cursors, request.Cursor)
	c.turnIDs = append(c.turnIDs, request.TurnID)
	body, ok := c.pages[request.Cursor]
	if !ok {
		return nil, fmt.Errorf("unexpected cursor %q", request.Cursor)
	}
	return body, nil
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

func TestMobileConversationAttentionComesFromPendingActions(t *testing.T) {
	page, err := normalizeConversationList("agent-1", json.RawMessage(`{"data":[{"id":"thread-1","name":"Needs help"},{"id":"thread-2","name":"Running"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	applyConversationAttention(&page, []pendingAction{
		{ConversationID: "thread-1"},
		{ConversationID: "thread-2", Resolved: true},
	})
	if !page.Items[0].NeedsAttention || page.Items[1].NeedsAttention {
		t.Fatalf("unexpected attention projection: %#v", page.Items)
	}
}

func TestNormalizeMobileRequestUserInputAction(t *testing.T) {
	actions := normalizeMobileActions([]pendingAction{{
		ID:             json.RawMessage(`"request-1"`),
		Method:         "item/tool/requestUserInput",
		ConversationID: "thread-1",
		TurnID:         "turn-1",
		Params:         json.RawMessage(`{"threadId":"thread-1","questions":[{"header":"Data","id":"empty_state","question":"How should it render?","options":[{"label":"Keep metrics","description":"Show placeholders."}],"isOther":true}]}`),
	}})
	if len(actions) != 1 || actions[0].Kind != "userInput" || len(actions[0].Questions) != 1 {
		t.Fatalf("unexpected action projection: %#v", actions)
	}
	question := actions[0].Questions[0]
	if question.ID != "empty_state" || question.Options[0].Label != "Keep metrics" || !question.IsOther {
		t.Fatalf("unexpected question projection: %#v", question)
	}
	encoded, err := json.Marshal(actions[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "threadId") {
		t.Fatalf("raw provider params escaped the stable Mobile DTO: %s", encoded)
	}
}

func TestMalformedMobileRequestUserInputDoesNotBecomeApproval(t *testing.T) {
	actions := normalizeMobileActions([]pendingAction{{
		ID:     json.RawMessage(`"request-1"`),
		Method: "item/tool/requestUserInput",
		Params: json.RawMessage(`{"questions":"invalid"}`),
	}})
	if len(actions) != 1 || actions[0].Kind != "userInput" || len(actions[0].Questions) != 0 {
		t.Fatalf("malformed question action must stay non-approvable: %#v", actions)
	}
}

func TestNormalizeMobileQueueRedactsProviderInputs(t *testing.T) {
	page, err := normalizeMobileQueuePage(json.RawMessage(`{"data":[{"id":"queue-1","clientUserMessageId":"client-1","input":[{"type":"text","text":"first"},{"type":"text","text":"second"},{"type":"localImage","path":"C:\\secret\\photo.jpg"},{"type":"mention","name":"private","path":"C:\\secret"}]}],"nextCursor":"next"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "queue-1" || page.Items[0].ClientMessageID != "client-1" || page.Items[0].Text != "first\nsecond" || page.Items[0].AttachmentCount != 1 || page.NextCursor != "next" {
		t.Fatalf("unexpected queue projection: %#v", page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "photo.jpg") {
		t.Fatalf("provider-only paths escaped queue projection: %s", encoded)
	}
}

func TestQueuedInputsWithTextPreservesNonTextInputs(t *testing.T) {
	inputs, err := queuedInputsWithText([]json.RawMessage{
		json.RawMessage(`{"type":"text","text":"old"}`),
		json.RawMessage(`{"type":"localImage","path":"C:\\attachments\\photo.jpg"}`),
	}, "  revised  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || string(inputs[0]) != `{"text":"revised","type":"text"}` || !strings.Contains(string(inputs[1]), "localImage") {
		t.Fatalf("unexpected updated inputs: %s", inputs)
	}
	if _, err = queuedInputsWithText([]json.RawMessage{json.RawMessage(`{"type":"text","text":"old"}`)}, "  "); err == nil {
		t.Fatal("expected an empty queued input to be rejected")
	}
}

func TestPlanTimelineBlockPreservesRawMarkdown(t *testing.T) {
	page, _, err := normalizeTimelinePage(json.RawMessage(`{"data":[{"id":"plan-1","type":"plan","text":"# 实施计划\n\n- 第一步\n- 第二步"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].Blocks) != 1 {
		t.Fatalf("unexpected plan page: %#v", page)
	}
	block := page.Items[0].Blocks[0]
	if block.Kind != "plan" || block.Text != "# 实施计划\n\n- 第一步\n- 第二步" {
		t.Fatalf("plan markdown was not preserved: %#v", block)
	}
}

func TestReasoningTimelineKeepsCompleteContentAndSummary(t *testing.T) {
	page, _, err := normalizeTimelinePage(json.RawMessage(`{"data":[{"id":"reasoning-1","type":"reasoning","content":["full reasoning line 1","full reasoning line 2"],"summary":["summary"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].Blocks) != 3 {
		t.Fatalf("unexpected reasoning blocks: %#v", page.Items)
	}
	want := []string{"full reasoning line 1", "full reasoning line 2", "summary"}
	for index, text := range want {
		if page.Items[0].Blocks[index].Text != text {
			t.Fatalf("reasoning block %d = %q, want %q", index, page.Items[0].Blocks[index].Text, text)
		}
	}
}

func TestPlanTimelineUsesRawFiveLinePreviewAndContentReference(t *testing.T) {
	full := "# 实施计划\r\n\r\n- 第一步\r\n- 第二步\r\n- 第三步\r\n- 第四步"
	page := mobileTimelinePage{Items: []mobileTimelineItem{{
		ID: "plan-1", TurnID: "turn-1", Kind: "plan", Blocks: []mobileTimelineBlock{{Kind: "plan", Text: full}},
	}}}
	manager := &Manager{contentRefs: make(map[string]mobileContentRef)}
	manager.applyContentRefs(&page, "agent-1", "thread-1", false)
	block := page.Items[0].Blocks[0]
	wantPreview := "# 实施计划\r\n\r\n- 第一步\r\n- 第二步\r\n- 第三步\r\n"
	if !block.Truncated || block.Text != wantPreview || block.TotalBytes != len(full) || block.ContentRef == "" {
		t.Fatalf("unexpected plan preview: %#v", block)
	}
	manager.contentRefMu.Lock()
	ref := manager.contentRefs[block.ContentRef]
	manager.contentRefMu.Unlock()
	if ref.TurnID != "turn-1" || ref.ItemID != "plan-1" || ref.BlockIndex != 0 {
		t.Fatalf("unexpected plan reference binding: %#v", ref)
	}
}

func TestPlanWithAtMostFiveRawLinesStaysInline(t *testing.T) {
	page := mobileTimelinePage{Items: []mobileTimelineItem{{
		ID: "plan-1", Kind: "plan", Blocks: []mobileTimelineBlock{{Kind: "plan", Text: "# Plan\n\n- one\n- two"}},
	}}}
	manager := &Manager{contentRefs: make(map[string]mobileContentRef)}
	manager.applyContentRefs(&page, "agent-1", "thread-1", false)
	block := page.Items[0].Blocks[0]
	if block.Truncated || block.ContentRef != "" || block.Text != "# Plan\n\n- one\n- two" {
		t.Fatalf("short plan should remain inline: %#v", block)
	}
}

func TestPlanWithExactlyFiveLinesAndTrailingNewlineStaysInline(t *testing.T) {
	value := "# Plan\n\n- one\n- two\n- three\n"
	preview, truncated := firstRawLines(value, 5)
	if truncated || preview != value {
		t.Fatalf("five source lines plus a trailing newline must stay inline: preview=%q truncated=%v", preview, truncated)
	}
	value += "- four"
	preview, truncated = firstRawLines(value, 5)
	if !truncated || preview != "# Plan\n\n- one\n- two\n- three\n" {
		t.Fatalf("a real sixth line must be truncated: preview=%q truncated=%v", preview, truncated)
	}
}

func TestPlanConfirmationRequiresLatestSuccessfulPlanTurn(t *testing.T) {
	instance := &Instance{
		stdin:          &bufferWriteCloser{},
		state:          "ready",
		actions:        make(map[string]*pendingAction),
		activeTurns:    make(map[string]string),
		threadSettings: map[string]threadCollaborationSettings{"thread-1": {TurnID: "turn-2", Mode: "plan", Model: "gpt-test", ReasoningEffort: "high", Succeeded: true}},
	}
	page := mobileTimelinePage{Items: []mobileTimelineItem{
		{ID: "plan-2", TurnID: "turn-2", Kind: "plan"},
		{ID: "message-1", TurnID: "turn-1", Kind: "agentMessage"},
	}}
	confirmation := planConfirmationFor(instance, "thread-1", mobileConversationMetadata{}, page, 9)
	if confirmation == nil || confirmation.PlanItemID != "plan-2" || confirmation.TurnID != "turn-2" || confirmation.Revision != 9 {
		t.Fatalf("unexpected plan confirmation: %#v", confirmation)
	}

	newerTurn := page
	newerTurn.Items = append([]mobileTimelineItem{{ID: "message-3", TurnID: "turn-3", Kind: "agentMessage"}}, page.Items...)
	if got := planConfirmationFor(instance, "thread-1", mobileConversationMetadata{}, newerTurn, 10); got != nil {
		t.Fatalf("historical plan must not be inferred after a newer turn: %#v", got)
	}

	settings := instance.threadSettings["thread-1"]
	settings.Succeeded = false
	instance.threadSettings["thread-1"] = settings
	if got := planConfirmationFor(instance, "thread-1", mobileConversationMetadata{}, page, 9); got != nil {
		t.Fatalf("failed or interrupted plan turn must not be confirmed: %#v", got)
	}
	settings.Succeeded = true
	instance.threadSettings["thread-1"] = settings
	instance.activeTurns["thread-1"] = "turn-3"
	if got := planConfirmationFor(instance, "thread-1", mobileConversationMetadata{}, page, 9); got != nil {
		t.Fatalf("active conversation must not expose a plan confirmation: %#v", got)
	}
	delete(instance.activeTurns, "thread-1")
	instance.actions["1"] = &pendingAction{ConversationID: "thread-1"}
	if got := planConfirmationFor(instance, "thread-1", mobileConversationMetadata{}, page, 9); got != nil {
		t.Fatalf("pending action must take precedence over plan confirmation: %#v", got)
	}
}

func TestPlanTurnStartParamsSwitchModeAndDiscardDeveloperInstructions(t *testing.T) {
	settings := threadCollaborationSettings{
		Model: "gpt-test", ReasoningEffort: "high", PermissionModeID: "autoReview",
		PermissionProfile: ":workspace", ApprovalPolicy: "on-request", ApprovalsReviewer: "auto_review",
	}
	for _, test := range []struct {
		decision string
		wantMode string
	}{
		{decision: "implement", wantMode: "default"},
		{decision: "revise", wantMode: "plan"},
	} {
		params := planTurnStartParams("thread-1", test.decision, "next", settings)
		mode := params["collaborationMode"].(map[string]any)
		if mode["mode"] != test.wantMode {
			t.Fatalf("%s mode = %v, want %s", test.decision, mode["mode"], test.wantMode)
		}
		modeSettings := mode["settings"].(map[string]any)
		if modeSettings["model"] != "gpt-test" || modeSettings["reasoning_effort"] != "high" {
			t.Fatalf("settings were not carried forward: %#v", modeSettings)
		}
		if value, exists := modeSettings["developer_instructions"]; !exists || value != nil {
			t.Fatalf("developer instructions must be explicitly cleared: %#v", modeSettings)
		}
		if params["permissions"] != ":workspace" || params["approvalPolicy"] != "on-request" || params["approvalsReviewer"] != "auto_review" {
			t.Fatalf("permission settings were not carried forward: %#v", params)
		}
	}
}

func TestNormalizeMobileModelCatalog(t *testing.T) {
	catalog, err := normalizeMobileModelCatalog(json.RawMessage(`{"data":[{"id":"gpt-a","model":"gpt-a","displayName":"GPT A","description":"Primary","hidden":false,"isDefault":true,"defaultReasoningEffort":"high","supportedReasoningEfforts":[{"reasoningEffort":"low","description":"Fast"},{"reasoningEffort":"high","description":"Deep"}]},{"id":"legacy","displayName":"Legacy","hidden":true,"isDefault":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Items) != 2 || catalog.Items[0].ID != "gpt-a" || !catalog.Items[0].Default || !catalog.Items[0].Selectable || catalog.Items[1].Selectable {
		t.Fatalf("unexpected normalized catalog: %#v", catalog)
	}
	if len(catalog.Items[0].ReasoningEfforts) != 2 || catalog.Items[0].DefaultReasoningEffort != "high" {
		t.Fatalf("reasoning efforts were not normalized: %#v", catalog.Items[0])
	}
}

func TestNormalizeMobilePermissionCatalogAppliesRequirements(t *testing.T) {
	profiles := json.RawMessage(`{"data":[{"id":":workspace","allowed":true},{"id":":read-only","allowed":true},{"id":":danger-full-access","allowed":true}]}`)
	requirements := json.RawMessage(`{"requirements":{"allowedApprovalPolicies":["on-request"],"allowedApprovalsReviewers":["user"],"allowedPermissionProfiles":{":workspace":true,":read-only":true,":danger-full-access":true},"allowedSandboxModes":["workspace-write","read-only"]}}`)
	catalog, err := normalizeMobilePermissionCatalog(profiles, requirements)
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, item := range catalog.Items {
		available[item.ID] = item.Available
	}
	if !available["default"] || !available["readOnly"] || available["autoReview"] || available["fullAccess"] {
		t.Fatalf("requirements were not applied: %#v", catalog.Items)
	}
}

func TestNormalizeMobilePermissionCatalogRequiresAutoReviewForConfiguredModel(t *testing.T) {
	profiles := json.RawMessage(`{"data":[{"id":":workspace","allowed":true},{"id":":read-only","allowed":true},{"id":":danger-full-access","allowed":true}]}`)
	requirements := json.RawMessage(`{"requirements":{"allowedApprovalsReviewers":["user","auto_review"],"autoReview":{"requiredOnModels":["model-guarded"]}}}`)
	catalog, err := normalizeMobilePermissionCatalog(profiles, requirements, "model-guarded")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Items {
		if item.Available != (item.ID == "autoReview") {
			t.Fatalf("auto review requirement was not enforced: %#v", catalog.Items)
		}
	}
}

func TestMobileConversationSettingsUpdateCommitsOnlyAfterProviderSuccess(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.manager.ctx = context.Background()
	instance.state = "ready"
	instance.conversations["thread-1"] = &conversationProjection{ID: "thread-1", Revision: 3}
	instance.threadSettings["thread-1"] = threadCollaborationSettings{
		Mode: "default", Model: "model-a", ReasoningEffort: "high", PermissionModeID: "default",
		PermissionProfile: ":workspace", ApprovalPolicy: "on-request", ApprovalsReviewer: "user",
	}

	serverReader, serverWriter := io.Pipe()
	defer serverReader.Close()
	defer serverWriter.Close()
	instance.stdin = serverWriter
	var failUpdate atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(serverReader)
		for scanner.Scan() {
			var request struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &request)
			var response string
			switch request.Method {
			case "model/list":
				response = `{"jsonrpc":"2.0","id":"` + request.ID + `","result":{"data":[{"id":"model-a","displayName":"A","hidden":false,"isDefault":true,"defaultReasoningEffort":"high","supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}}`
			case "permissionProfile/list":
				response = `{"jsonrpc":"2.0","id":"` + request.ID + `","result":{"data":[{"id":":workspace","allowed":true},{"id":":read-only","allowed":true},{"id":":danger-full-access","allowed":true}]}}`
			case "configRequirements/read":
				response = `{"jsonrpc":"2.0","id":"` + request.ID + `","result":{"requirements":null}}`
			case "thread/settings/update":
				if failUpdate.Load() {
					response = `{"jsonrpc":"2.0","id":"` + request.ID + `","error":{"code":-32602,"message":"rejected"}}`
				} else {
					response = `{"jsonrpc":"2.0","id":"` + request.ID + `","result":{}}`
				}
			}
			instance.handleServerFrame(instance.generation, []byte(response))
		}
	}()

	request := json.RawMessage(`{"threadId":"thread-1","expectedRevision":3,"settings":{"mode":"plan","modelId":"model-a","reasoningEffort":"high","permissionModeId":"autoReview"}}`)
	updated, err := instance.manager.mobileConversationSettingsUpdate(instance, request)
	if err != nil || updated.Mode != "plan" || updated.PermissionModeID != "autoReview" {
		t.Fatalf("successful settings update = %#v, err=%v", updated, err)
	}
	committed, _ := instance.collaborationSettings("thread-1")
	if committed.Mode != "plan" || committed.PermissionModeID != "autoReview" {
		t.Fatalf("successful provider response was not committed: %#v", committed)
	}

	instance.activeTurns["thread-1"] = "turn-running"
	runningRequest := json.RawMessage(`{"threadId":"thread-1","expectedRevision":3,"settings":{"mode":"plan","modelId":"model-a","reasoningEffort":"high","permissionModeId":"autoReview"}}`)
	if _, err = instance.manager.mobileConversationSettingsUpdate(instance, runningRequest); err != nil {
		t.Fatalf("settings update should be accepted for a running turn: %v", err)
	}
	delete(instance.activeTurns, "thread-1")

	failUpdate.Store(true)
	request = json.RawMessage(`{"threadId":"thread-1","expectedRevision":3,"settings":{"mode":"default","modelId":"model-a","reasoningEffort":"high","permissionModeId":"readOnly"}}`)
	if _, err = instance.manager.mobileConversationSettingsUpdate(instance, request); err == nil {
		t.Fatal("provider rejection must fail the settings update")
	}
	afterFailure, _ := instance.collaborationSettings("thread-1")
	if afterFailure.Mode != "plan" || afterFailure.PermissionModeID != "autoReview" {
		t.Fatalf("provider rejection changed the committed projection: %#v", afterFailure)
	}
	_ = serverWriter.Close()
	<-done
}

func TestMobileQueueMethodsMapToProviderQueue(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.manager.ctx = context.Background()
	instance.manager.instances = map[string]*Instance{"test-instance": instance}
	instance.state = "ready"
	instance.activeTurns["thread-1"] = "turn-running"

	serverReader, serverWriter := io.Pipe()
	defer serverReader.Close()
	defer serverWriter.Close()
	instance.stdin = serverWriter
	methods := make([]string, 0, 5)
	var updateParams json.RawMessage
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(serverReader)
		for scanner.Scan() {
			var request struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &request)
			methods = append(methods, request.Method)
			var result string
			switch request.Method {
			case "thread/queue/add":
				result = `{"queuedSubmission":{"id":"queue-1","clientUserMessageId":"client-1","input":[{"type":"text","text":"hello"}]}}`
			case "thread/queue/list":
				result = `{"data":[{"id":"queue-1","clientUserMessageId":"client-1","input":[{"type":"text","text":"hello"},{"type":"localImage","path":"C:\\private\\photo.jpg"}]}]}`
			case "thread/queue/update":
				updateParams = append(json.RawMessage(nil), request.Params...)
				result = `{"queuedSubmission":{"id":"queue-1","clientUserMessageId":"client-1","input":[{"type":"text","text":"revised"},{"type":"localImage","path":"C:\\private\\photo.jpg"}]}}`
			case "thread/queue/delete":
				result = `{}`
			case "thread/queue/start":
				result = `{"turn":{"id":"turn-guided"}}`
			}
			instance.handleServerFrame(instance.generation, []byte(`{"jsonrpc":"2.0","id":"`+request.ID+`","result":`+result+`}`))
		}
	}()

	added, err := instance.manager.mobileQueueAdd(instance, json.RawMessage(`{"threadId":"thread-1","clientMessageId":"client-1","text":"hello"}`))
	if err != nil || added.ID != "queue-1" || added.Text != "hello" {
		t.Fatalf("queue add = %#v, err=%v", added, err)
	}
	updated, err := instance.manager.mobileQueueUpdate(instance, json.RawMessage(`{"threadId":"thread-1","queuedSubmissionId":"queue-1","text":"revised"}`))
	if err != nil || updated.Text != "revised" || updated.AttachmentCount != 1 {
		t.Fatalf("queue update = %#v, err=%v", updated, err)
	}
	if !bytes.Contains(updateParams, []byte("localImage")) || bytes.Contains(updateParams, []byte(`"text":"hello"`)) {
		t.Fatalf("queue update did not preserve only non-text input: %s", updateParams)
	}
	deleted, err := instance.manager.mobileQueueDelete(instance, json.RawMessage(`{"threadId":"thread-1","queuedSubmissionId":"queue-1"}`))
	if err != nil || deleted["deleted"] != true {
		t.Fatalf("queue delete = %#v, err=%v", deleted, err)
	}
	started, err := instance.manager.mobileQueueStart(instance, json.RawMessage(`{"threadId":"thread-1","queuedSubmissionId":"queue-1"}`))
	if err != nil || started["turnId"] != "turn-guided" {
		t.Fatalf("queue start = %#v, err=%v", started, err)
	}

	_ = serverWriter.Close()
	<-done
	wantMethods := []string{"thread/queue/add", "thread/queue/list", "thread/queue/update", "thread/queue/delete", "thread/queue/start"}
	if fmt.Sprint(methods) != fmt.Sprint(wantMethods) {
		t.Fatalf("provider methods = %v, want %v", methods, wantMethods)
	}
}

func TestPlanRespondReturnsStablePreflightErrors(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.state = "ready"
	manager := instance.manager

	_, err := manager.mobilePlanRespond(instance, "test-instance", json.RawMessage(`{}`))
	if contractErr, ok := err.(*mobileContractError); !ok || contractErr.code != "invalid_params" {
		t.Fatalf("invalid request error = %#v", err)
	}

	request := json.RawMessage(`{"threadId":"thread-1","planItemId":"plan-1","expectedRevision":0,"decision":"implement","text":"implement"}`)
	_, err = manager.mobilePlanRespond(instance, "test-instance", request)
	if contractErr, ok := err.(*mobileContractError); !ok || contractErr.code != "plan_mode_unavailable" {
		t.Fatalf("missing settings error = %#v", err)
	}

	instance.mu.Lock()
	instance.conversations["thread-1"] = &conversationProjection{Revision: 2}
	instance.mu.Unlock()
	_, err = manager.mobilePlanRespond(instance, "test-instance", request)
	if contractErr, ok := err.(*mobileContractError); !ok || contractErr.code != "revision_conflict" {
		t.Fatalf("revision conflict error = %#v", err)
	}

	instance.mu.Lock()
	instance.conversations["thread-1"].Revision = 0
	instance.activeTurns["thread-1"] = "turn-2"
	instance.mu.Unlock()
	_, err = manager.mobilePlanRespond(instance, "test-instance", request)
	if contractErr, ok := err.(*mobileContractError); !ok || contractErr.code != "conversation_busy" {
		t.Fatalf("busy conversation error = %#v", err)
	}
}

func TestNormalizeTimelineSummariesAndHiddenItems(t *testing.T) {
	rawItems := []map[string]any{
		{"id": "hook", "type": "hookPrompt", "fragments": []any{"secret"}},
		{"id": "function", "type": "functionCallOutput", "namespace": "fs", "name": "read", "output": "secret output"},
		{"id": "mcp", "type": "mcpToolCall", "server": "github", "tool": "list_issues", "arguments": map[string]any{"private": true}, "result": map[string]any{"content": []any{"secret"}}},
		{"id": "dynamic", "type": "dynamicToolCall", "namespace": "browser", "tool": "open", "arguments": map[string]any{"url": "https://secret.invalid"}},
		{"id": "collab", "type": "collabAgentToolCall", "tool": "spawnAgent", "prompt": "secret prompt"},
		{"id": "subagent", "type": "subAgentActivity", "kind": "completed", "agentPath": "C:/secret/agent"},
		{"id": "search", "type": "webSearch", "query": "  hello\n world ", "results": []any{"secret"}},
		{"id": "image", "type": "imageView", "path": `C:\secret\image.png`},
		{"id": "sleep", "type": "sleep", "durationMs": 1500},
		{"id": "generate", "type": "imageGeneration", "result": "secret result"},
		{"id": "enter", "type": "enteredReviewMode", "review": "  review\nmode "},
		{"id": "exit", "type": "exitedReviewMode", "review": "review"},
		{"id": "compact", "type": "contextCompaction"},
		{"id": "command", "type": "commandExecution", "command": "git status", "aggregatedOutput": "secret output"},
	}
	raw, _ := json.Marshal(map[string]any{"data": rawItems})
	page, _, err := normalizeTimelinePage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(rawItems)-1 {
		t.Fatalf("hidden hook item was not removed: %#v", page.Items)
	}
	want := map[string]string{
		"function": "fs.read",
		"mcp":      "github/list_issues",
		"dynamic":  "browser.open",
		"collab":   "spawnAgent",
		"subagent": "completed",
		"search":   "hello world",
		"image":    "image.png",
		"sleep":    "1500ms",
		"generate": "imageGeneration",
		"enter":    "review mode",
		"exit":     "review",
		"compact":  "contextCompaction",
		"command":  "git status",
	}
	for _, item := range page.Items {
		if expected, ok := want[item.ID]; ok {
			wantKind := "summary"
			if item.ID == "command" {
				wantKind = "command"
			}
			if len(item.Blocks) != 1 || item.Blocks[0].Kind != wantKind || item.Blocks[0].Text != expected {
				t.Fatalf("unexpected summary for %s: %#v", item.ID, item)
			}
			wantValue := expected
			if item.ID == "generate" || item.ID == "compact" {
				wantValue = ""
			}
			if item.ID != "command" && (item.Blocks[0].SummaryKind != item.Kind || item.Blocks[0].SummaryValue != wantValue) {
				t.Fatalf("summary metadata for %s is incomplete: %#v", item.ID, item.Blocks[0])
			}
			encoded, _ := json.Marshal(item)
			for _, forbidden := range []string{"secret", "arguments", "result", "prompt", "C:\\secret"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("summary %s leaked %q: %s", item.ID, forbidden, encoded)
				}
			}
		}
	}
	encoded, _ := json.Marshal(page)
	for _, localized := range []string{"调用", "搜索", "图片", "上下文"} {
		if strings.Contains(string(encoded), localized) {
			t.Fatalf("daemon summary must not contain localized prose %q: %s", localized, encoded)
		}
	}
}

func TestTimelineSummaryValueIsSingleLineAndUTF8Bounded(t *testing.T) {
	block, ok := timelineSummary(map[string]any{"type": "webSearch", "query": strings.Repeat("界", 300) + "\nsecret"})
	if !ok || block.Kind != "summary" || block.SummaryKind != "webSearch" || block.Text != block.SummaryValue {
		t.Fatalf("unexpected summary block: %#v", block)
	}
	if len(block.SummaryValue) > mobileSummaryValueBytes || !utf8.ValidString(block.SummaryValue) || strings.Contains(block.SummaryValue, "\n") {
		t.Fatalf("summary value is not safely bounded: bytes=%d value=%q", len(block.SummaryValue), block.SummaryValue)
	}
}

func TestFindTimelineBlockUsesTurnScopedPaginationAndRejectsSummary(t *testing.T) {
	first, _ := json.Marshal(map[string]any{
		"data":       []any{map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "summary", "type": "mcpToolCall", "server": "github", "tool": "list"}}},
		"nextCursor": "next",
	})
	second, _ := json.Marshal(map[string]any{
		"data": []any{map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "plan", "type": "plan", "text": "# Full plan\n\n- item"}}},
	})
	caller := &pagedMobileCaller{pages: map[string]json.RawMessage{"": first, "next": second}}
	manager := &Manager{ctx: context.Background()}
	text, err := manager.findTimelineBlock(caller, "thread-1", "turn-1", "plan", 0)
	if err != nil || text != "# Full plan\n\n- item" {
		t.Fatalf("turn-scoped content lookup failed: text=%q err=%v", text, err)
	}
	if fmt.Sprint(caller.cursors) != "[ next]" {
		t.Fatalf("content lookup did not paginate: %v", caller.cursors)
	}
	if fmt.Sprint(caller.turnIDs) != "[turn-1 turn-1]" {
		t.Fatalf("content lookup was not scoped to the turn: %v", caller.turnIDs)
	}
	caller.cursors = nil
	_, err = manager.findTimelineBlock(caller, "thread-1", "turn-1", "summary", 0)
	var contractErr *mobileContractError
	if !errors.As(err, &contractErr) || contractErr.code != "content_not_found" {
		t.Fatalf("summary content must not be resolvable: %v", err)
	}
}

func TestTimelineFilteringFollowsPagesAndBindsReferencesToSourcePage(t *testing.T) {
	first, _ := json.Marshal(map[string]any{
		"data": []any{
			map[string]any{"id": "hidden", "type": "hookPrompt", "text": "not visible"},
			map[string]any{"id": "summary", "type": "mcpToolCall", "server": "github", "tool": "list"},
		},
		"nextCursor": "page-2",
	})
	second, _ := json.Marshal(map[string]any{
		"data": []any{
			map[string]any{"turnId": "turn-2", "item": map[string]any{"id": "plan", "type": "plan", "text": "# Plan\n\n- one\n- two\n- three\n- four"}},
		},
	})
	caller := &pagedMobileCaller{pages: map[string]json.RawMessage{"": first, "page-2": second}}
	manager := &Manager{ctx: context.Background(), contentRefs: make(map[string]mobileContentRef)}
	page, err := manager.readTimelinePage(caller, "agent-1", "thread-1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "summary" || page.Items[1].ID != "plan" || page.NextCursor != "" {
		t.Fatalf("unexpected filtered page: %#v", page)
	}
	if fmt.Sprint(caller.limits) != "[2 1]" || fmt.Sprint(caller.cursors) != "[ page-2]" {
		t.Fatalf("provider pagination = limits %v cursors %v", caller.limits, caller.cursors)
	}
	block := page.Items[1].Blocks[0]
	if block.ContentRef == "" || !block.Truncated || block.Text != "# Plan\n\n- one\n- two\n- three\n" {
		t.Fatalf("plan was not previewed with a content reference: %#v", block)
	}
	manager.contentRefMu.Lock()
	ref := manager.contentRefs[block.ContentRef]
	manager.contentRefMu.Unlock()
	if ref.TurnID != "turn-2" || ref.ItemID != "plan" {
		t.Fatalf("content reference was not bound to source page: %#v", ref)
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
	want := []int{30, 15, 7, 3, 1, 1}
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
	manager.applyContentRefs(&page, "agent-1", "thread-1", false)
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
	if got, _ := timelineLimit(0); got != 20 {
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
	if len(page.Items) != 1 || page.Items[0].AgentID != "agent-1" || page.Items[0].Name != "work-anywhere" || page.NextCursor != "more" {
		t.Fatalf("unexpected page: %#v", page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "D:/CodeSpace/work-anywhere") || strings.Contains(string(encoded), "root") {
		t.Fatalf("project root leaked into mobile DTO: %s", encoded)
	}
}

func TestMobileProjectListUsesLatestConversationTime(t *testing.T) {
	caller := &projectRecencyCaller{
		projects:      json.RawMessage(`{"data":[{"id":"p-old","name":"Old","updatedAt":900},{"id":"p-new","name":"New","updatedAt":1},{"id":"p-empty","name":"Empty","updatedAt":999}]}`),
		conversations: json.RawMessage(`{"data":[{"id":"t-old","projectId":"p-old","updatedAt":100},{"id":"t-new","projectId":"p-new","updatedAt":200},{"id":"t-newer","projectId":"p-new","updatedAt":300}]}`),
	}
	manager := &Manager{ctx: context.Background()}
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}
	page, err := manager.mobileProjectListWithCaller(instance, caller, json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "p-new" || page.Items[1].ID != "p-old" {
		t.Fatalf("projects were not ordered by latest conversation: %#v", page.Items)
	}
	if page.Items[0].UpdatedAt != 300 || page.Items[1].UpdatedAt != 100 {
		t.Fatalf("project timestamps were not derived from conversations: %#v", page.Items)
	}
	first, err := manager.mobileProjectListWithCaller(instance, caller, json.RawMessage(`{"limit":1}`))
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first project page was not paginated: %#v, %v", first, err)
	}
	secondParams, _ := json.Marshal(map[string]any{"limit": 1, "cursor": first.NextCursor})
	second, err := manager.mobileProjectListWithCaller(instance, caller, secondParams)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "p-old" {
		t.Fatalf("project cursor did not continue sorted results: %#v, %v", second, err)
	}
}

func TestMobileProjectListFallsBackToConversationCWD(t *testing.T) {
	caller := &projectRecencyCaller{
		projects:      json.RawMessage(`{"data":[{"id":"p-main","name":"Main","roots":[{"path":"D:/CodeSpace/work-anywhere"}]},{"id":"p-mobile","name":"Mobile","roots":[{"path":"D:/CodeSpace/work-anywhere/mobile"}]},{"id":"p-other","name":"Other","roots":[{"path":"D:/CodeSpace/other"}]}]}`),
		conversations: json.RawMessage(`{"data":[{"id":"t-main","projectId":null,"cwd":"D:/CodeSpace/work-anywhere","updatedAt":100},{"id":"t-mobile","projectId":null,"cwd":"D:/CodeSpace/work-anywhere/mobile/app","updatedAt":300},{"id":"t-other","projectId":null,"environments":[{"environmentId":"local","cwd":"D:/CodeSpace/other"}],"updatedAt":200},{"id":"t-unmatched","projectId":null,"cwd":"D:/Temp/unmatched","updatedAt":999}]}`),
	}
	manager := &Manager{ctx: context.Background()}
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}
	page, err := manager.mobileProjectListWithCaller(instance, caller, json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].ID != "p-mobile" || page.Items[1].ID != "p-other" || page.Items[2].ID != "p-main" {
		t.Fatalf("projects were not matched and ordered by conversation cwd: %#v", page.Items)
	}
	if page.Items[0].UpdatedAt != 300 || page.Items[1].UpdatedAt != 200 || page.Items[2].UpdatedAt != 100 {
		t.Fatalf("cwd-matched project timestamps were incorrect: %#v", page.Items)
	}
}

func TestMobileProjectListUsesOnlyProjectMainRoot(t *testing.T) {
	caller := &projectRecencyCaller{
		projects:      json.RawMessage(`{"data":[{"id":"p-skills","name":"lasen-skills","roots":[{"path":"D:/CodeSpace/lasen-skills"},{"path":"D:/CodeSpace/lasen/lasen-ui"}]},{"id":"p-ui","name":"lasen-ui","roots":[{"path":"D:/CodeSpace/lasen/lasen-ui"}]}]}`),
		conversations: json.RawMessage(`{"data":[{"id":"t-ui","projectId":null,"cwd":"D:/CodeSpace/lasen/lasen-ui","updatedAt":300}]}`),
	}
	manager := &Manager{ctx: context.Background()}
	instance := &Instance{descriptor: protocol.InstanceDescriptor{ID: "agent-1"}}
	page, err := manager.mobileProjectListWithCaller(instance, caller, json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "p-ui" {
		t.Fatalf("conversation was claimed by a secondary project root: %#v", page.Items)
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
	projects := []mobileProviderProject{{ID: "p1", Name: "Project One"}}
	page, err := normalizeConversationList("agent-1", json.RawMessage(`{"data":[{"id":"t1","projectId":"p1","name":null,"preview":"First line\nSecond line","status":{"active":{"activeFlags":["waitingOnApproval"]}},"createdAt":1,"updatedAt":2},{"id":"t1","projectId":"p1","name":"Duplicate","preview":"ignored","status":"idle","createdAt":1,"updatedAt":2}]}`), projects)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "First line" || page.Items[0].ProjectName != "Project One" || !page.Items[0].Running || page.Items[0].Status != "waitingOnApproval" {
		t.Fatalf("unexpected page: %#v", page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "preview") || strings.Contains(string(encoded), "Second line") {
		t.Fatalf("conversation preview leaked into mobile DTO: %s", encoded)
	}
}

func TestNormalizeConversationListRecoversLegacyProjectNameFromCWD(t *testing.T) {
	projects := []mobileProviderProject{{ID: "p1", Name: "Project One", Roots: []string{`C:\code\one`}}}
	page, err := normalizeConversationList("agent-1", json.RawMessage(`{"data":[{"id":"t1","projectId":"","cwd":"C:\\code\\one\\src","name":"Legacy thread","status":"idle"}]}`), projects)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ProjectID != "p1" || page.Items[0].ProjectName != "Project One" {
		t.Fatalf("legacy project was not recovered: %#v", page.Items)
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
