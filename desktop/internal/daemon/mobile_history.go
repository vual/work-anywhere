package daemon

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const legacyCursorPrefix = "wa:history:"

type legacyHistoryCursor struct {
	Version  int    `json:"v"`
	Backend  string `json:"backend"`
	AgentID  string `json:"agentId"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
}

func providerFault(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	var fault struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(err.Error(), "App Server: ")), &fault) != nil {
		return 0, ""
	}
	return fault.Code, fault.Message
}

func unsupportedMethod(err error) bool {
	code, _ := providerFault(err)
	return code == -32601
}

func emptyHistoryUnsupported(err error) bool {
	code, message := providerFault(err)
	return code == -32601 && message == "list_turns is not supported yet"
}

// Disk evidence survives daemon restarts. Resolve symlinks before checking the
// sessions boundary, and require a matching session identity in every record.
func verifiedEmptyRollout(home, path, threadID string) bool {
	if home == "" || path == "" || threadID == "" {
		return false
	}
	root, err := filepath.EvalSymlinks(filepath.Join(home, "sessions"))
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	file, err := os.Open(resolved)
	if err != nil {
		return false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 || stat.Size() > 1<<20 {
		return false
	}
	scanner := bufio.NewScanner(io.LimitReader(file, (1<<20)+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	total, count := 0, 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if total > 1<<20 || json.Unmarshal(scanner.Bytes(), &record) != nil || record.Type != "session_meta" || record.Payload.ID != threadID {
			return false
		}
		count++
	}
	return count > 0 && scanner.Err() == nil
}

func (i *Instance) allowEmptyHistory(threadID string, path *string) bool {
	i.mu.Lock()
	home := i.descriptor.CodexHome
	created := i.createdThreads[threadID]
	busy := i.pendingTurnStarts[threadID] || i.activeTurns[threadID] != "" || i.pendingTurnSettings[threadID].Mode != ""
	i.mu.Unlock()
	if busy {
		return false
	}
	if path == nil || *path == "" {
		return created
	}
	if verifiedEmptyRollout(home, *path, threadID) {
		return true
	}
	// Missing rollouts are only expected immediately after a known creation.
	_, err := os.Stat(*path)
	return created && os.IsNotExist(err)
}

// Called only for an explicit user write, after reserving its turn. Reading a
// conversation never resumes it. No retry of the user message is involved.
func (m *Manager) resumeEmptyThreadForTurn(i *Instance, threadID string) error {
	i.mu.Lock()
	generation, needed := i.emptyThreadsToResume[threadID]
	needed = needed && generation == i.generation
	home := i.descriptor.CodexHome
	i.mu.Unlock()
	if !needed {
		return nil
	}
	raw, err := i.Call(m.ctx, "thread/read", mustJSON(map[string]any{"threadId": threadID, "includeTurns": false}))
	if err != nil {
		return err
	}
	var metadata struct {
		Thread struct {
			ID     string `json:"id"`
			Path   string `json:"path"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &metadata) != nil || metadata.Thread.ID != threadID {
		return &mobileContractError{code: "invalid_provider_response", message: "invalid empty thread metadata"}
	}
	if metadata.Thread.Status.Type == "notLoaded" {
		if !verifiedEmptyRollout(home, metadata.Thread.Path, threadID) {
			return &mobileContractError{code: "revision_conflict", message: "empty thread changed before resume"}
		}
		resumed, resumeErr := i.Call(m.ctx, "thread/resume", mustJSON(map[string]any{"threadId": threadID, "excludeTurns": true}))
		if resumeErr != nil {
			return resumeErr
		}
		var response struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(resumed, &response) != nil || response.Thread.ID != threadID {
			return &mobileContractError{code: "invalid_provider_response", message: "resume response does not match the empty thread"}
		}
	} else if metadata.Thread.Status.Type != "idle" {
		return &mobileContractError{code: "conversation_busy", message: "empty thread is no longer idle"}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if generation != i.generation {
		return &mobileContractError{code: "revision_conflict", message: "App Server restarted during resume"}
	}
	delete(i.emptyThreadsToResume, threadID)
	return nil
}

func noRollout(err error, threadID string) bool {
	code, message := providerFault(err)
	return code == -32603 && strings.Contains(message, "no rollout found for thread id "+threadID)
}

func legacyBackend(caller mobileProviderCaller, threadID string) bool {
	if i, ok := caller.(*Instance); ok {
		i.mu.Lock()
		defer i.mu.Unlock()
		generation, exists := i.legacyTimeline[threadID]
		return exists && generation == i.generation
	}
	return false
}

func rememberLegacy(caller mobileProviderCaller, threadID string) {
	if i, ok := caller.(*Instance); ok {
		i.mu.Lock()
		defer i.mu.Unlock()
		if i.legacyTimeline == nil {
			i.legacyTimeline = make(map[string]uint64)
		}
		i.legacyTimeline[threadID] = i.generation
	}
}

func (m *Manager) legacyHistoryPage(caller mobileProviderCaller, agentID, threadID, cursor string, limit int) (mobileTimelinePage, error) {
	var anchor legacyHistoryCursor
	if cursor != "" {
		if len(cursor) > 4096 || !strings.HasPrefix(cursor, legacyCursorPrefix) {
			return mobileTimelinePage{}, &mobileContractError{code: "invalid_params", message: "invalid history cursor"}
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, legacyCursorPrefix))
		if err != nil || json.Unmarshal(data, &anchor) != nil || anchor.Version != 1 || anchor.Backend != "legacy" || anchor.AgentID != agentID || anchor.ThreadID != threadID || anchor.ItemID == "" {
			return mobileTimelinePage{}, &mobileContractError{code: "invalid_params", message: "history cursor does not belong to this conversation"}
		}
	}
	params, _ := json.Marshal(map[string]any{"threadId": threadID, "includeTurns": true})
	raw, err := caller.Call(m.ctx, "thread/read", params)
	if err != nil {
		return mobileTimelinePage{}, &mobileContractError{code: "provider_error", message: err.Error()}
	}
	var source struct {
		Thread struct {
			ID    string `json:"id"`
			Turns []struct {
				ID    string           `json:"id"`
				Items []map[string]any `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &source) != nil || source.Thread.ID != threadID {
		return mobileTimelinePage{}, &mobileContractError{code: "invalid_provider_response", message: "invalid thread/read history"}
	}
	entries := make([]map[string]any, 0)
	for _, turn := range source.Thread.Turns {
		for _, item := range turn.Items {
			entries = append(entries, map[string]any{"turnId": turn.ID, "item": item})
		}
	}
	// Provider history is oldest first; the Mobile timeline is newest first.
	for a, b := 0, len(entries)-1; a < b; a, b = a+1, b-1 {
		entries[a], entries[b] = entries[b], entries[a]
	}
	encoded, _ := json.Marshal(map[string]any{"data": entries})
	page, _, err := normalizeTimelinePage(encoded)
	if err != nil {
		return mobileTimelinePage{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	start := 0
	if cursor != "" {
		found := false
		for index, item := range page.Items {
			if item.ID == anchor.ItemID && item.TurnID == anchor.TurnID {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return mobileTimelinePage{}, &mobileContractError{code: "cursor_expired", message: "history cursor anchor no longer exists"}
		}
	}
	end := start + limit
	if end > len(page.Items) {
		end = len(page.Items)
	}
	result := mobileTimelinePage{Items: append([]mobileTimelineItem{}, page.Items[start:end]...)}
	if end < len(page.Items) && end > start {
		last := result.Items[len(result.Items)-1]
		encoded, _ := json.Marshal(legacyHistoryCursor{Version: 1, Backend: "legacy", AgentID: agentID, ThreadID: threadID, TurnID: last.TurnID, ItemID: last.ID})
		result.NextCursor = legacyCursorPrefix + base64.RawURLEncoding.EncodeToString(encoded)
	}
	return result, nil
}

func (m *Manager) fetchCompatibleTimeline(caller mobileProviderCaller, agentID, threadID, cursor string, limit int) ([]mobileTimelineSegment, error) {
	return m.fetchCompatibleTimelineScoped(caller, agentID, threadID, "", cursor, limit)
}

func (m *Manager) fetchCompatibleTimelineScoped(caller mobileProviderCaller, agentID, threadID, turnID, cursor string, limit int) ([]mobileTimelineSegment, error) {
	if len(cursor) > 4096 {
		return nil, &mobileContractError{code: "invalid_params", message: "timeline cursor is too long"}
	}
	legacy := legacyBackend(caller, threadID) || strings.HasPrefix(cursor, legacyCursorPrefix)
	if !legacy {
		segments, err := m.fetchTimelineSegmentsScoped(caller, threadID, turnID, cursor, limit)
		if err == nil || !unsupportedMethod(err) {
			return segments, err
		}
		if cursor != "" {
			return nil, &mobileContractError{code: "cursor_expired", message: "timeline backend changed; refresh conversation"}
		}
	}
	page, err := m.legacyHistoryPage(caller, agentID, threadID, cursor, limit)
	if err != nil {
		return nil, err
	}
	rememberLegacy(caller, threadID)
	return []mobileTimelineSegment{{Page: page, Cursor: cursor, PageLimit: limit}}, nil
}

func (m *Manager) compatibleQueue(instance *Instance, threadID string, allowEmpty bool) (mobileQueuePage, bool, error) {
	queue, err := m.mobileQueueList(instance, threadID)
	if unsupportedMethod(err) {
		return mobileQueuePage{Items: []mobileQueuedSubmission{}}, false, nil
	}
	if allowEmpty && noRollout(err, threadID) {
		return mobileQueuePage{Items: []mobileQueuedSubmission{}}, true, nil
	}
	return queue, true, err
}

func (m *Manager) stableConversationSnapshot(instance *Instance, agentID string, params json.RawMessage) (mobileConversationSnapshot, error) {
	var target struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &target)
	if target.ThreadID == "" {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	for attempt := 0; attempt < 3; attempt++ {
		revision, _ := instance.ConversationVersion(target.ThreadID)
		snapshot, err := m.conversationSnapshotOnce(instance, agentID, params)
		if err != nil {
			return mobileConversationSnapshot{}, err
		}
		current, _ := instance.ConversationVersion(target.ThreadID)
		if current == revision && snapshot.Revision == revision {
			return snapshot, nil
		}
	}
	return mobileConversationSnapshot{}, &mobileContractError{code: "snapshot_unstable", message: "conversation changed during synchronization; retry"}
}

func historyError(err error, code string) bool {
	var fault *mobileContractError
	return errors.As(err, &fault) && fault.code == code
}
