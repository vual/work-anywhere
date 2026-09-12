package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

const (
	mobileDefaultTimelineLimit     = 20
	mobileMaxTimelineLimit         = 50
	mobileControlResponseBytes     = 512 << 10
	mobileTimelinePageBytes        = 500 << 10
	mobileLargeBlockBytes          = 64 << 10
	mobileInlinePreviewBytes       = 16 << 10
	mobileContentChunkBytes        = 128 << 10
	mobileContentRefTTL            = 15 * time.Minute
	mobileMaxContentRefs           = 2048
	mobileMaxTimelineProviderPages = 8
	mobileSummaryValueBytes        = 512
)

type mobileAgentSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	ActiveTurn     bool   `json:"activeTurn"`
	PendingActions int    `json:"pendingActions"`
}

type mobileProjectSummary struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updatedAt"`
}

type mobileConversationSummary struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	ProjectID string `json:"projectId,omitempty"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Running   bool   `json:"running"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type mobilePage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (m *Manager) mobileAgents() []mobileAgentSummary {
	snapshots := m.instanceSnapshots()
	result := make([]mobileAgentSummary, 0, len(snapshots))
	for _, item := range snapshots {
		name := item.Name
		if name == "" {
			name = "Codex"
		}
		result = append(result, mobileAgentSummary{ID: item.ID, Name: name, Kind: "codex", Status: item.State, ActiveTurn: item.ActiveTurn, PendingActions: item.PendingActions})
	}
	return result
}

// mobileControlTarget accepts agentId at the public boundary while preserving
// instanceId for backwards-compatible Desktop callers. It also translates the
// Mobile search spelling to the official Codex app-server parameter.
func (m *Manager) mobileControlTarget(raw json.RawMessage) (*Instance, json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, nil, err
	}
	if _, exists := object["instanceId"]; !exists {
		if agentID, exists := object["agentId"]; exists {
			object["instanceId"] = agentID
		}
	}
	delete(object, "agentId")
	if query, exists := object["query"]; exists {
		object["searchTerm"] = query
		delete(object, "query")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, nil, err
	}
	return m.controlTarget(normalized)
}

func normalizeProjectList(agentID string, raw json.RawMessage) (mobilePage[mobileProjectSummary], error) {
	var source struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			UpdatedAt int64  `json:"updatedAt"`
			Roots     []struct {
				Path string `json:"path"`
			} `json:"roots"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobilePage[mobileProjectSummary]{}, fmt.Errorf("decode project/list: %w", err)
	}
	page := mobilePage[mobileProjectSummary]{Items: make([]mobileProjectSummary, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, item := range source.Data {
		if item.ID == "" {
			return mobilePage[mobileProjectSummary]{}, errors.New("project/list item has no id")
		}
		page.Items = append(page.Items, mobileProjectSummary{ID: item.ID, AgentID: agentID, Name: item.Name, UpdatedAt: item.UpdatedAt})
	}
	return page, nil
}

// mobileConversationList keeps project navigation working for threads created
// before the provider migrated legacy project assignments to project IDs.
// Those threads can still be addressed reliably by their session cwd.
func (m *Manager) mobileConversationList(instance *Instance, params json.RawMessage) (json.RawMessage, error) {
	var request struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(params, &request); err != nil || request.ProjectID == "" {
		return instance.Call(m.ctx, "thread/list", params)
	}

	result, err := instance.Call(m.ctx, "thread/list", params)
	if err != nil || !emptyThreadList(result) {
		return result, err
	}

	root, err := m.projectRoot(instance, request.ProjectID)
	if err != nil || root == "" {
		return result, nil
	}
	var object map[string]any
	if err := json.Unmarshal(params, &object); err != nil {
		return result, nil
	}
	delete(object, "projectId")
	object["cwd"] = root
	fallbackParams, err := json.Marshal(object)
	if err != nil {
		return result, nil
	}
	return instance.Call(m.ctx, "thread/list", fallbackParams)
}

func emptyThreadList(raw json.RawMessage) bool {
	var source struct {
		Data []json.RawMessage `json:"data"`
	}
	return json.Unmarshal(raw, &source) == nil && len(source.Data) == 0
}

func (m *Manager) projectRoot(instance *Instance, projectID string) (string, error) {
	const params = `{"limit":1000}`
	var source struct {
		Data []struct {
			ID    string `json:"id"`
			Roots []struct {
				Path string `json:"path"`
			} `json:"roots"`
		} `json:"data"`
	}
	raw, err := instance.Call(m.ctx, "project/list", json.RawMessage(params))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return "", err
	}
	for _, item := range source.Data {
		if item.ID == projectID && len(item.Roots) > 0 {
			return item.Roots[0].Path, nil
		}
	}
	return "", nil
}

func normalizeConversationList(agentID string, raw json.RawMessage) (mobilePage[mobileConversationSummary], error) {
	var source struct {
		Data []struct {
			ID        string          `json:"id"`
			ProjectID string          `json:"projectId"`
			Name      string          `json:"name"`
			Preview   string          `json:"preview"`
			Status    json.RawMessage `json:"status"`
			CreatedAt int64           `json:"createdAt"`
			UpdatedAt int64           `json:"updatedAt"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobilePage[mobileConversationSummary]{}, fmt.Errorf("decode thread/list: %w", err)
	}
	page := mobilePage[mobileConversationSummary]{Items: make([]mobileConversationSummary, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, item := range source.Data {
		if item.ID == "" {
			return mobilePage[mobileConversationSummary]{}, errors.New("thread/list item has no id")
		}
		title := strings.TrimSpace(item.Name)
		if title == "" {
			title = firstLine(item.Preview)
		}
		if title == "" {
			title = "Untitled conversation"
		}
		status, running := normalizeThreadStatus(item.Status)
		page.Items = append(page.Items, mobileConversationSummary{ID: item.ID, AgentID: agentID, ProjectID: item.ProjectID, Title: title, Status: status, Running: running, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	return page, nil
}

type mobileConversationMetadata struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	ProjectID string `json:"projectId,omitempty"`
	Title     string `json:"title"`
	Preview   string `json:"preview,omitempty"`
	Status    string `json:"status"`
	Running   bool   `json:"running"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func normalizeConversationCreate(raw json.RawMessage) (map[string]any, error) {
	var source struct {
		Thread struct {
			ID        string  `json:"id"`
			ProjectID *string `json:"projectId"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode thread/start: %w", err)
	}
	if source.Thread.ID == "" {
		return nil, errors.New("thread/start response has no thread id")
	}
	result := map[string]any{"id": source.Thread.ID}
	if source.Thread.ProjectID != nil && *source.Thread.ProjectID != "" {
		result["projectId"] = *source.Thread.ProjectID
	}
	return result, nil
}

type mobileTimelineBlock struct {
	Kind         string `json:"kind"`
	Text         string `json:"text"`
	SummaryKind  string `json:"summaryKind,omitempty"`
	SummaryValue string `json:"summaryValue,omitempty"`
	Truncated    bool   `json:"truncated"`
	ContentRef   string `json:"contentRef,omitempty"`
	TotalBytes   int    `json:"totalBytes,omitempty"`
}

type mobileTimelineItem struct {
	ID     string                `json:"id"`
	TurnID string                `json:"turnId,omitempty"`
	Kind   string                `json:"kind"`
	Status string                `json:"status,omitempty"`
	Blocks []mobileTimelineBlock `json:"blocks"`
}

type mobileTimelinePage struct {
	Items      []mobileTimelineItem `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

type mobileConversationSnapshot struct {
	Conversation mobileConversationMetadata `json:"conversation"`
	Revision     int64                      `json:"revision"`
	Sequence     int64                      `json:"sequence"`
	Timeline     mobileTimelinePage         `json:"timeline"`
}

type mobileContentRef struct {
	AgentID, ThreadID, TurnID, ItemID string
	BlockIndex                        int
	ExpiresAt                         time.Time
}

type mobileContractError struct {
	code    string
	message string
}

type mobileProviderCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func (e *mobileContractError) Error() string { return e.message }

func mobileControlError(err error) *protocol.ControlError {
	var contractErr *mobileContractError
	if errors.As(err, &contractErr) {
		return &protocol.ControlError{Code: contractErr.code, Message: contractErr.message}
	}
	return &protocol.ControlError{Code: "provider_error", Message: err.Error()}
}

func normalizeConversationMetadata(agentID string, raw json.RawMessage) (mobileConversationMetadata, error) {
	var source struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileConversationMetadata{}, fmt.Errorf("decode thread/read: %w", err)
	}
	if len(source.Thread) == 0 || string(source.Thread) == "null" {
		return mobileConversationMetadata{}, errors.New("thread/read response has no thread")
	}
	var thread struct {
		ID        string          `json:"id"`
		ProjectID string          `json:"projectId"`
		Name      string          `json:"name"`
		Preview   string          `json:"preview"`
		Status    json.RawMessage `json:"status"`
		CreatedAt int64           `json:"createdAt"`
		UpdatedAt int64           `json:"updatedAt"`
	}
	if err := json.Unmarshal(source.Thread, &thread); err != nil {
		return mobileConversationMetadata{}, fmt.Errorf("decode thread/read thread: %w", err)
	}
	if thread.ID == "" {
		return mobileConversationMetadata{}, errors.New("thread/read response has no thread id")
	}
	title := strings.TrimSpace(thread.Name)
	if title == "" {
		title = firstLine(thread.Preview)
	}
	if title == "" {
		title = "Untitled conversation"
	}
	status, running := normalizeThreadStatus(thread.Status)
	return mobileConversationMetadata{ID: thread.ID, AgentID: agentID, ProjectID: thread.ProjectID, Title: title, Preview: thread.Preview, Status: status, Running: running, CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt}, nil
}

func normalizeTimelinePage(raw json.RawMessage) (mobileTimelinePage, []map[string]any, error) {
	var source struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileTimelinePage{}, nil, fmt.Errorf("decode thread/items/list: %w", err)
	}
	page := mobileTimelinePage{Items: make([]mobileTimelineItem, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, rawEntry := range source.Data {
		// The current Codex app-server returns ThreadItemEntry values shaped as
		// {"item": <ThreadItem>, "turnId": "..."}. Keep accepting the old
		// flattened shape so older recorded fixtures remain useful.
		rawItem := rawEntry
		turnID, _ := rawEntry["turnId"].(string)
		if nested, ok := rawEntry["item"].(map[string]any); ok {
			rawItem = nested
		} else if value, exists := rawEntry["item"]; exists && value != nil {
			return mobileTimelinePage{}, nil, errors.New("thread/items/list item has invalid item")
		}
		id, _ := rawItem["id"].(string)
		if id == "" {
			return mobileTimelinePage{}, nil, errors.New("thread/items/list item has no id")
		}
		kind, _ := rawItem["type"].(string)
		if kind == "" {
			kind = "item"
		}
		if kind == "hookPrompt" {
			continue
		}
		if turnID == "" {
			turnID, _ = rawItem["turnId"].(string)
		}
		status := compactStatus(rawItem["status"])
		page.Items = append(page.Items, mobileTimelineItem{ID: id, TurnID: turnID, Kind: kind, Status: status, Blocks: timelineBlocks(rawItem)})
	}
	return page, source.Data, nil
}

func compactStatus(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	raw, _ := json.Marshal(value)
	var object map[string]any
	if json.Unmarshal(raw, &object) == nil {
		for key := range object {
			return key
		}
	}
	return "unknown"
}

func timelineBlocks(item map[string]any) []mobileTimelineBlock {
	if summary, ok := timelineSummary(item); ok {
		return []mobileTimelineBlock{summary}
	}
	if kind, _ := item["type"].(string); kind == "commandExecution" {
		if command, ok := item["command"].(string); ok && strings.TrimSpace(command) != "" {
			return []mobileTimelineBlock{{Kind: "command", Text: command}}
		}
		return nil
	}
	blocks := make([]mobileTimelineBlock, 0, 4)
	appendText := func(kind string, value any) {
		for _, text := range readableStrings(value) {
			if strings.TrimSpace(text) != "" {
				blocks = append(blocks, mobileTimelineBlock{Kind: kind, Text: text})
			}
		}
	}
	if kind, _ := item["type"].(string); kind == "reasoning" {
		blocks = blocks[:0]
		appendText("reasoning", item["content"])
		appendText("reasoningSummary", item["summary"])
		return blocks
	}
	// Plan text is Markdown and must remain a single block so the mobile
	// client can render the raw source instead of a lossy plaintext summary.
	if kind, _ := item["type"].(string); kind == "plan" {
		if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
			return []mobileTimelineBlock{{Kind: "plan", Text: text}}
		}
	}
	for _, field := range []struct{ key, kind string }{
		{"text", "text"}, {"message", "text"}, {"content", "content"}, {"summary", "reasoning"},
		{"command", "command"}, {"aggregatedOutput", "output"}, {"output", "output"},
		{"diff", "diff"}, {"changes", "diff"}, {"result", "result"}, {"error", "error"},
	} {
		if value, ok := item[field.key]; ok {
			appendText(field.kind, value)
		}
	}
	if len(blocks) == 0 {
		if name, ok := item["name"].(string); ok && name != "" {
			blocks = append(blocks, mobileTimelineBlock{Kind: "summary", Text: name})
		}
	}
	return blocks
}

func timelineSummary(item map[string]any) (mobileTimelineBlock, bool) {
	kind, _ := item["type"].(string)
	text := func(rawValue any) string {
		value, _ := rawValue.(string)
		return summaryValue(oneLine(value))
	}
	qualified := func(namespace, name string) string {
		if namespace == "" {
			return name
		}
		if name == "" {
			return namespace
		}
		return namespace + "." + name
	}
	result := func(value string) (mobileTimelineBlock, bool) {
		value = summaryValue(value)
		fallback := value
		if fallback == "" {
			fallback = kind
		}
		return mobileTimelineBlock{Kind: "summary", Text: fallback, SummaryKind: kind, SummaryValue: value}, true
	}
	switch kind {
	case "functionCallOutput":
		name, _ := item["name"].(string)
		namespace, _ := item["namespace"].(string)
		return result(qualified(oneLine(namespace), oneLine(name)))
	case "mcpToolCall":
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		server, tool = oneLine(server), oneLine(tool)
		return result(strings.Trim(server+"/"+tool, "/"))
	case "dynamicToolCall":
		namespace, _ := item["namespace"].(string)
		tool, _ := item["tool"].(string)
		return result(qualified(oneLine(namespace), oneLine(tool)))
	case "collabAgentToolCall":
		tool, _ := item["tool"].(string)
		return result(oneLine(tool))
	case "subAgentActivity":
		activity, _ := item["kind"].(string)
		return result(oneLine(activity))
	case "webSearch":
		query, _ := item["query"].(string)
		return result(text(query))
	case "imageView":
		path, _ := item["path"].(string)
		return result(baseName(path))
	case "sleep":
		return result(durationText(item["durationMs"]) + "ms")
	case "imageGeneration":
		return result("")
	case "enteredReviewMode":
		review, _ := item["review"].(string)
		return result(text(review))
	case "exitedReviewMode":
		review, _ := item["review"].(string)
		return result(text(review))
	case "contextCompaction":
		return result("")
	default:
		return mobileTimelineBlock{}, false
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func summaryValue(value string) string {
	if len(value) <= mobileSummaryValueBytes {
		return value
	}
	return utf8Prefix(value, mobileSummaryValueBytes)
}

func durationText(value any) string {
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%g", typed)
	case float32:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case json.Number:
		return typed.String()
	default:
		return "0"
	}
}

func baseName(path string) string {
	path = strings.TrimSpace(path)
	if index := strings.LastIndexAny(path, `/\\`); index >= 0 {
		return path[index+1:]
	}
	return path
}

// firstRawLines returns the first maxLines source lines without converting or
// reformatting Markdown. SplitAfter preserves the original line endings,
// including CRLF and blank lines, which keeps the preview faithful to the
// provider's source document.
func firstRawLines(value string, maxLines int) (string, bool) {
	if maxLines <= 0 || value == "" {
		return value, false
	}
	lines := strings.SplitAfter(value, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines {
		return value, false
	}
	return strings.Join(lines[:maxLines], ""), true
}

func readableStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, readableStrings(item)...)
		}
		return result
	case map[string]any:
		result := make([]string, 0, 2)
		for _, key := range []string{"text", "message", "path", "diff", "output", "content", "title", "name", "url"} {
			if child, ok := typed[key]; ok {
				result = append(result, readableStrings(child)...)
			}
		}
		return result
	default:
		return nil
	}
}

func utf8Prefix(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func utf8ContentChunk(value string, offset int) (string, int, bool, error) {
	if offset < 0 || offset > len(value) || (offset < len(value) && !utf8.RuneStart(value[offset])) {
		return "", offset, false, errors.New("offset is not a valid UTF-8 boundary")
	}
	end := offset + mobileContentChunkBytes
	if end > len(value) {
		end = len(value)
	}
	for end > offset && end < len(value) && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[offset:end], end, end < len(value), nil
}

func (m *Manager) registerContentRef(agentID, threadID, turnID, itemID string, blockIndex int) string {
	now := time.Now()
	m.contentRefMu.Lock()
	defer m.contentRefMu.Unlock()
	if m.contentRefs == nil {
		m.contentRefs = make(map[string]mobileContentRef)
	}
	for token, ref := range m.contentRefs {
		if now.After(ref.ExpiresAt) || len(m.contentRefs) >= mobileMaxContentRefs {
			delete(m.contentRefs, token)
		}
	}
	token := randomToken(24)
	m.contentRefs[token] = mobileContentRef{AgentID: agentID, ThreadID: threadID, TurnID: turnID, ItemID: itemID, BlockIndex: blockIndex, ExpiresAt: now.Add(mobileContentRefTTL)}
	return token
}

func (m *Manager) resolveContentRef(token, agentID, threadID string, now time.Time) (mobileContentRef, error) {
	m.contentRefMu.Lock()
	defer m.contentRefMu.Unlock()
	ref, ok := m.contentRefs[token]
	if !ok || now.After(ref.ExpiresAt) || ref.AgentID != agentID || ref.ThreadID != threadID {
		if ok && now.After(ref.ExpiresAt) {
			delete(m.contentRefs, token)
		}
		return mobileContentRef{}, &mobileContractError{code: "content_ref_expired", message: "content reference expired or does not belong to this conversation"}
	}
	ref.ExpiresAt = now.Add(mobileContentRefTTL)
	m.contentRefs[token] = ref
	return ref, nil
}

func (m *Manager) applyContentRefs(page *mobileTimelinePage, agentID, threadID string, force bool) {
	for itemIndex := range page.Items {
		for blockIndex := range page.Items[itemIndex].Blocks {
			block := &page.Items[itemIndex].Blocks[blockIndex]
			if block.Kind == "plan" {
				preview, truncated := firstRawLines(block.Text, 5)
				if !truncated {
					continue
				}
				block.TotalBytes = len(block.Text)
				block.Text = preview
				block.Truncated = true
				block.ContentRef = m.registerContentRef(agentID, threadID, page.Items[itemIndex].TurnID, page.Items[itemIndex].ID, blockIndex)
				continue
			}
			if block.Kind == "summary" {
				continue
			}
			threshold, preview := mobileLargeBlockBytes, mobileInlinePreviewBytes
			if force {
				threshold, preview = 4<<10, 1<<10
			}
			if len(block.Text) <= threshold {
				continue
			}
			block.TotalBytes = len(block.Text)
			block.Text = utf8Prefix(block.Text, preview)
			block.Truncated = true
			block.ContentRef = m.registerContentRef(agentID, threadID, page.Items[itemIndex].TurnID, page.Items[itemIndex].ID, blockIndex)
		}
	}
}

func timelineLimit(value int) (int, error) {
	if value == 0 {
		return mobileDefaultTimelineLimit, nil
	}
	if value < 1 || value > mobileMaxTimelineLimit {
		return 0, &mobileContractError{code: "invalid_params", message: "limit must be between 1 and 50"}
	}
	return value, nil
}

type mobileTimelineSegment struct {
	Page      mobileTimelinePage
	Cursor    string
	PageLimit int
}

func (m *Manager) fetchTimelineSegments(instance mobileProviderCaller, threadID, cursor string, limit int) ([]mobileTimelineSegment, error) {
	segments := make([]mobileTimelineSegment, 0, 1)
	remaining := limit
	providerCursor := cursor
	for pageCount := 0; pageCount < mobileMaxTimelineProviderPages && remaining > 0; pageCount++ {
		providerParams := map[string]any{"threadId": threadID, "limit": remaining, "sortDirection": "desc"}
		if providerCursor != "" {
			providerParams["cursor"] = providerCursor
		}
		rawParams, _ := json.Marshal(providerParams)
		raw, callErr := instance.Call(m.ctx, "thread/items/list", rawParams)
		if callErr != nil {
			return nil, &mobileContractError{code: "provider_error", message: callErr.Error()}
		}
		page, _, decodeErr := normalizeTimelinePage(raw)
		if decodeErr != nil {
			return nil, &mobileContractError{code: "invalid_provider_response", message: decodeErr.Error()}
		}
		if len(page.Items) > remaining {
			page.Items = page.Items[:remaining]
		}
		segments = append(segments, mobileTimelineSegment{Page: page, Cursor: providerCursor, PageLimit: remaining})
		remaining -= len(page.Items)
		if remaining == 0 || page.NextCursor == "" {
			break
		}
		providerCursor = page.NextCursor
	}
	return segments, nil
}

func (m *Manager) composeTimelineSegments(segments []mobileTimelineSegment, agentID, threadID string, force bool) mobileTimelinePage {
	page := mobileTimelinePage{Items: make([]mobileTimelineItem, 0)}
	for index := range segments {
		segment := &segments[index]
		m.applyContentRefs(&segment.Page, agentID, threadID, force)
		page.Items = append(page.Items, segment.Page.Items...)
		page.NextCursor = segment.Page.NextCursor
	}
	return page
}

func (m *Manager) readTimelinePageAttempt(instance mobileProviderCaller, agentID, threadID, cursor string, limit int, force bool) (mobileTimelinePage, error) {
	segments, err := m.fetchTimelineSegments(instance, threadID, cursor, limit)
	if err != nil {
		return mobileTimelinePage{}, err
	}
	return m.composeTimelineSegments(segments, agentID, threadID, force), nil
}

func (m *Manager) readTimelinePage(instance mobileProviderCaller, agentID, threadID, cursor string, requestedLimit int) (mobileTimelinePage, error) {
	limit, err := timelineLimit(requestedLimit)
	if err != nil {
		return mobileTimelinePage{}, err
	}
	for {
		page, err := m.readTimelinePageAttempt(instance, agentID, threadID, cursor, limit, false)
		if err != nil {
			return mobileTimelinePage{}, err
		}
		encoded, _ := json.Marshal(page)
		if len(encoded) <= mobileTimelinePageBytes || limit == 1 {
			if len(encoded) > mobileTimelinePageBytes {
				page, err = m.readTimelinePageAttempt(instance, agentID, threadID, cursor, limit, true)
				if err != nil {
					return mobileTimelinePage{}, err
				}
			}
			return page, nil
		}
		limit /= 2
		if limit < 1 {
			limit = 1
		}
	}
}

func (m *Manager) conversationSnapshot(instance *Instance, agentID string, params json.RawMessage) (mobileConversationSnapshot, error) {
	var request struct {
		ThreadID string `json:"threadId"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	metadataParams, _ := json.Marshal(map[string]any{"threadId": request.ThreadID, "includeTurns": false})
	raw, err := instance.Call(m.ctx, "thread/read", metadataParams)
	if err != nil {
		return mobileConversationSnapshot{}, &mobileContractError{code: "provider_error", message: err.Error()}
	}
	metadata, err := normalizeConversationMetadata(agentID, raw)
	if err != nil {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	page, err := m.readTimelinePage(instance, agentID, request.ThreadID, "", request.Limit)
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	revision, sequence := instance.ConversationVersion(request.ThreadID)
	return mobileConversationSnapshot{Conversation: metadata, Revision: revision, Sequence: sequence, Timeline: page}, nil
}

func (m *Manager) mobileConversationRead(instance *Instance, params json.RawMessage) (mobileConversationSnapshot, error) {
	return m.conversationSnapshot(instance, instance.descriptor.ID, params)
}

func (m *Manager) conversationTimeline(instance *Instance, agentID string, params json.RawMessage) (mobileTimelinePage, error) {
	var request struct {
		ThreadID string `json:"threadId"`
		Cursor   string `json:"cursor"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.Cursor == "" {
		return mobileTimelinePage{}, &mobileContractError{code: "invalid_params", message: "threadId and cursor are required"}
	}
	return m.readTimelinePage(instance, agentID, request.ThreadID, request.Cursor, request.Limit)
}

func (m *Manager) mobileTimelineList(instance *Instance, params json.RawMessage) (mobileTimelinePage, error) {
	return m.conversationTimeline(instance, instance.descriptor.ID, params)
}

func (m *Manager) findTimelineBlock(instance mobileProviderCaller, threadID, turnID, itemID string, blockIndex int) (string, error) {
	if threadID == "" || itemID == "" || blockIndex < 0 {
		return "", &mobileContractError{code: "invalid_params", message: "threadId, itemId and a non-negative blockIndex are required"}
	}
	cursor := ""
	seen := make(map[string]struct{})
	for {
		if _, duplicate := seen[cursor]; duplicate {
			return "", &mobileContractError{code: "invalid_provider_response", message: "thread/items/list returned a cursor loop"}
		}
		seen[cursor] = struct{}{}
		providerParams := map[string]any{"threadId": threadID, "limit": mobileMaxTimelineLimit, "sortDirection": "asc"}
		if turnID != "" {
			providerParams["turnId"] = turnID
		}
		if cursor != "" {
			providerParams["cursor"] = cursor
		}
		rawParams, _ := json.Marshal(providerParams)
		raw, err := instance.Call(m.ctx, "thread/items/list", rawParams)
		if err != nil {
			return "", &mobileContractError{code: "provider_error", message: err.Error()}
		}
		page, _, err := normalizeTimelinePage(raw)
		if err != nil {
			return "", &mobileContractError{code: "invalid_provider_response", message: err.Error()}
		}
		for _, item := range page.Items {
			if item.ID != itemID {
				continue
			}
			if turnID != "" && item.TurnID != "" && item.TurnID != turnID {
				continue
			}
			if blockIndex >= len(item.Blocks) || item.Blocks[blockIndex].Kind == "summary" || item.Blocks[blockIndex].Text == "" {
				return "", &mobileContractError{code: "content_not_found", message: "referenced content is not available"}
			}
			return item.Blocks[blockIndex].Text, nil
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return "", &mobileContractError{code: "content_not_found", message: "referenced content is no longer available"}
}

func (m *Manager) conversationContent(instance *Instance, agentID string, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID   string `json:"threadId"`
		ContentRef string `json:"contentRef"`
		Offset     int    `json:"offset"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.ContentRef == "" || request.Offset < 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, contentRef and a non-negative offset are required"}
	}
	ref, err := m.resolveContentRef(request.ContentRef, agentID, request.ThreadID, time.Now())
	if err != nil {
		return nil, err
	}
	text, err := m.findTimelineBlock(instance, ref.ThreadID, ref.TurnID, ref.ItemID, ref.BlockIndex)
	if err != nil {
		return nil, err
	}
	chunk, next, hasMore, chunkErr := utf8ContentChunk(text, request.Offset)
	if chunkErr != nil {
		return nil, &mobileContractError{code: "invalid_params", message: chunkErr.Error()}
	}
	return map[string]any{"text": chunk, "nextOffset": next, "hasMore": hasMore, "totalBytes": len(text)}, nil
}

func (m *Manager) mobileContentRead(instance *Instance, params json.RawMessage) (map[string]any, error) {
	return m.conversationContent(instance, instance.descriptor.ID, params)
}

func (m *Manager) conversationContentResolve(instance *Instance, agentID string, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ItemID     string `json:"itemId"`
		BlockIndex int    `json:"blockIndex"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.TurnID == "" || request.ItemID == "" || request.BlockIndex < 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, turnId, itemId and a non-negative blockIndex are required"}
	}
	text, err := m.findTimelineBlock(instance, request.ThreadID, request.TurnID, request.ItemID, request.BlockIndex)
	if err != nil {
		return nil, err
	}
	contentRef := m.registerContentRef(agentID, request.ThreadID, request.TurnID, request.ItemID, request.BlockIndex)
	return map[string]any{"contentRef": contentRef, "totalBytes": len(text)}, nil
}

func (m *Manager) mobileContentResolve(instance *Instance, params json.RawMessage) (map[string]any, error) {
	return m.conversationContentResolve(instance, instance.descriptor.ID, params)
}

func normalizeThreadStatus(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "unknown", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		lower := strings.ToLower(text)
		return text, strings.Contains(lower, "active") || strings.Contains(lower, "running") || strings.Contains(lower, "waiting")
	}
	compact := strings.ToLower(string(raw))
	switch {
	case strings.Contains(compact, "waitingonapproval"):
		return "waitingOnApproval", true
	case strings.Contains(compact, "waitingonuserinput"):
		return "waitingOnUserInput", true
	case strings.Contains(compact, "active") || strings.Contains(compact, "running"):
		return "running", true
	default:
		return "unknown", false
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	const maxTitle = 80
	if len([]rune(value)) > maxTitle {
		return string([]rune(value)[:maxTitle])
	}
	return value
}
