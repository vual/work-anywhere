package daemon

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
	"github.com/hurf/work-anywhere/desktop/internal/provider/claudecode"
)

const claudeCodeSettingKey = "claudeCode"

// claudeCodeSettings is the persisted Claude Code backend configuration.
// A missing setting enables the backend when the transcript store exists.
type claudeCodeSettings struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	ConfigDir string `json:"configDir,omitempty"`
}

// backendVersion is the daemon-side revision/sequence projection for one
// backend conversation. Backends only report "changed"; the daemon owns the
// counters Mobile uses for subscription cursors.
type backendVersion struct {
	revision int64
	sequence int64
}

func (m *Manager) startBackends() {
	var settings claudeCodeSettings
	if m.settings != nil {
		if _, err := m.settings.GetSetting(claudeCodeSettingKey, &settings); err != nil && m.log != nil {
			m.log.Event("claude_code.settings_invalid", map[string]any{"error": err.Error()})
		}
	}
	if settings.Enabled != nil && !*settings.Enabled {
		return
	}
	configDir, err := claudecode.ResolveConfigDir(settings.ConfigDir)
	if err != nil || (settings.Enabled == nil && !claudecode.Available(configDir)) {
		return
	}
	backend, err := claudecode.New(claudecode.Options{ConfigDir: configDir})
	if err == nil {
		err = m.registerBackend(backend)
	}
	if err != nil && m.log != nil {
		m.log.Event("claude_code.start_failed", map[string]any{"error": err.Error()})
	}
}

func (m *Manager) registerBackend(backend provider.Backend) error {
	agentID := backend.Agent().ID
	m.backendMu.Lock()
	if m.backends == nil {
		m.backends = make(map[string]provider.Backend)
	}
	m.backends[agentID] = backend
	m.backendMu.Unlock()
	return backend.Start(m.ctx, func(conversationID string) { m.backendChanged(agentID, conversationID) })
}

func (m *Manager) closeBackends() {
	m.backendMu.Lock()
	backends := m.backends
	m.backends = nil
	m.backendMu.Unlock()
	for _, backend := range backends {
		_ = backend.Close()
	}
}

func (m *Manager) backendAgents() []mobileAgentSummary {
	m.backendMu.Lock()
	backends := make([]provider.Backend, 0, len(m.backends))
	for _, backend := range m.backends {
		backends = append(backends, backend)
	}
	m.backendMu.Unlock()
	result := make([]mobileAgentSummary, 0, len(backends))
	for _, backend := range backends {
		result = append(result, backend.Agent())
	}
	return result
}

// backendTarget resolves the Mobile agentId to a non-Codex backend.
func (m *Manager) backendTarget(raw json.RawMessage) (provider.Backend, bool) {
	var target struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(raw, &target) != nil || target.AgentID == "" {
		return nil, false
	}
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	backend, ok := m.backends[target.AgentID]
	return backend, ok
}

func (m *Manager) backendVersionOf(agentID, conversationID string) backendVersion {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	if version := m.backendVersions[agentID+"\x00"+conversationID]; version != nil {
		return *version
	}
	return backendVersion{}
}

// backendChanged turns a backend change report into a daemon event. Remote
// forwarding converts it into a payload-free Mobile invalidation.
func (m *Manager) backendChanged(agentID, conversationID string) {
	sequence := int64(0)
	if conversationID != "" {
		m.backendMu.Lock()
		if m.backendVersions == nil {
			m.backendVersions = make(map[string]*backendVersion)
		}
		key := agentID + "\x00" + conversationID
		version := m.backendVersions[key]
		if version == nil {
			version = &backendVersion{}
			m.backendVersions[key] = version
		}
		version.revision++
		version.sequence++
		sequence = version.sequence
		m.backendMu.Unlock()
	}
	event := "provider.conversation.updated"
	if conversationID == "" {
		event = "provider.conversations.changed"
	}
	m.publish(protocol.Event{Type: "event", Sequence: sequence, SyncEpoch: m.syncEpoch, Event: event, InstanceID: agentID, ConversationID: conversationID})
}

// backendControl serves Mobile contract methods for a provider.Backend. It
// returns false when the method is not a conversation method, leaving it to
// the shared daemon switch.
func (m *Manager) backendControl(backend provider.Backend, request protocol.ControlRequest, response *protocol.ControlResponse) bool {
	agentID := backend.Agent().ID
	var result any
	var err error
	switch request.Method {
	case "project.list":
		result, err = m.backendProjectList(backend, request.Params)
	case "conversation.list":
		result, err = m.backendConversationList(backend, request.Params)
	case "conversation.read":
		result, err = m.backendConversationRead(backend, agentID, request.Params)
	case "conversation.timeline.list":
		var params struct {
			ThreadID string `json:"threadId"`
			Cursor   string `json:"cursor"`
			Limit    int    `json:"limit"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" || params.Cursor == "" {
			err = &mobileContractError{code: "invalid_params", message: "threadId and cursor are required"}
			break
		}
		result, err = m.backendTimelinePage(backend, agentID, params.ThreadID, params.Cursor, params.Limit)
	case "conversation.content.read":
		var params struct {
			ThreadID   string `json:"threadId"`
			ContentRef string `json:"contentRef"`
			Offset     int    `json:"offset"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" || params.ContentRef == "" || params.Offset < 0 {
			err = &mobileContractError{code: "invalid_params", message: "threadId, contentRef and a non-negative offset are required"}
			break
		}
		var ref mobileContentRef
		if ref, err = m.resolveContentRef(params.ContentRef, agentID, params.ThreadID, time.Now()); err != nil {
			break
		}
		var text string
		if text, err = backend.TimelineBlock(m.ctx, ref.ThreadID, ref.TurnID, ref.ItemID, ref.BlockIndex); err != nil {
			break
		}
		chunk, next, hasMore, chunkErr := utf8ContentChunk(text, params.Offset)
		if chunkErr != nil {
			err = &mobileContractError{code: "invalid_params", message: chunkErr.Error()}
			break
		}
		result = map[string]any{"text": chunk, "nextOffset": next, "hasMore": hasMore, "totalBytes": len(text)}
	case "conversation.content.resolve":
		var params struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			ItemID     string `json:"itemId"`
			BlockIndex int    `json:"blockIndex"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" || params.ItemID == "" || params.BlockIndex < 0 {
			err = &mobileContractError{code: "invalid_params", message: "threadId, itemId and a non-negative blockIndex are required"}
			break
		}
		var text string
		if text, err = backend.TimelineBlock(m.ctx, params.ThreadID, params.TurnID, params.ItemID, params.BlockIndex); err != nil {
			break
		}
		result = map[string]any{"contentRef": m.registerContentRef(agentID, params.ThreadID, params.TurnID, params.ItemID, params.BlockIndex), "totalBytes": len(text)}
	case "conversation.subscribe":
		var params struct {
			ThreadID string `json:"threadId"`
			Cursor   int64  `json:"cursor"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := m.backendVersionOf(agentID, params.ThreadID)
		// Backend events carry no replayable payload, so any gap is resolved
		// by a fresh snapshot rather than an event replay.
		result = map[string]any{"threadId": params.ThreadID, "status": "idle", "revision": version.revision, "sequence": version.sequence, "syncEpoch": m.syncEpoch, "events": []protocol.Event{}, "snapshot": params.Cursor != version.sequence}
	case "action.list":
		result = []mobileAction{}
	case "catalog.models":
		result = mobileModelCatalog{Items: []mobileModelCatalogItem{}}
	case "catalog.permissionProfiles":
		result = mobilePermissionCatalog{Items: []mobilePermissionMode{}}
	case "conversation.create", "turn.start", "turn.interrupt", "action.resolve", "plan.respond", "conversation.settings.update", "attachment.begin":
		err = &mobileContractError{code: "unsupported_capability", message: request.Method + " is not supported for this agent"}
	default:
		if strings.HasPrefix(request.Method, "conversation.queue.") {
			err = &mobileContractError{code: "unsupported_capability", message: request.Method + " is not supported for this agent"}
			break
		}
		return false
	}
	if err != nil {
		response.Error = mobileControlError(err)
	} else {
		response.Result = result
	}
	return true
}

func (m *Manager) backendProjectList(backend provider.Backend, raw json.RawMessage) (mobilePage[mobileProjectSummary], error) {
	var request struct {
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return mobilePage[mobileProjectSummary]{}, &mobileContractError{code: "invalid_params", message: "invalid project list params"}
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, mobileProjectProviderPageLimit)
	items, err := backend.ListProjects(m.ctx)
	if err != nil {
		return mobilePage[mobileProjectSummary]{}, err
	}
	start := 0
	if request.Cursor != "" {
		cursor, err := decodeMobileProjectCursor(request.Cursor)
		if err != nil {
			return mobilePage[mobileProjectSummary]{}, &mobileContractError{code: "invalid_params", message: err.Error()}
		}
		for start < len(items) && !mobileProjectAfterCursor(items[start], cursor) {
			start++
		}
	}
	end := min(start+limit, len(items))
	page := mobilePage[mobileProjectSummary]{Items: append([]mobileProjectSummary{}, items[start:end]...)}
	if end < len(items) && end > start {
		page.NextCursor = encodeMobileProjectCursor(mobileProjectCursor{UpdatedAt: items[end-1].UpdatedAt, ProjectID: items[end-1].ID})
	}
	return page, nil
}

func (m *Manager) backendConversationList(backend provider.Backend, raw json.RawMessage) (mobilePage[mobileConversationSummary], error) {
	var request struct {
		ProjectID  string `json:"projectId"`
		Query      string `json:"query"`
		SearchTerm string `json:"searchTerm"`
		Cursor     string `json:"cursor"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return mobilePage[mobileConversationSummary]{}, &mobileContractError{code: "invalid_params", message: "invalid conversation list params"}
	}
	query := request.Query
	if query == "" {
		query = request.SearchTerm
	}
	return backend.ListConversations(m.ctx, provider.ConversationQuery{ProjectID: request.ProjectID, Query: query, Cursor: request.Cursor, Limit: request.Limit})
}

func (m *Manager) backendConversationRead(backend provider.Backend, agentID string, raw json.RawMessage) (mobileConversationSnapshot, error) {
	var request struct {
		ThreadID string `json:"threadId"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(raw, &request) != nil || request.ThreadID == "" {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	version := m.backendVersionOf(agentID, request.ThreadID)
	metadata, err := backend.ReadConversation(m.ctx, request.ThreadID)
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	page, err := m.backendTimelinePage(backend, agentID, request.ThreadID, "", request.Limit)
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	snapshot := mobileConversationSnapshot{
		Conversation: metadata,
		Settings:     mobileConversationSettings{Mode: "default"},
		Revision:     version.revision,
		Sequence:     version.sequence,
		SyncEpoch:    m.syncEpoch,
		Timeline:     page,
		Queue:        mobileQueuePage{Items: []mobileQueuedSubmission{}},
	}
	return snapshot, nil
}

// backendTimelinePage applies the same response budget as the Codex path:
// large blocks become content references and the page shrinks until it fits.
func (m *Manager) backendTimelinePage(backend provider.Backend, agentID, threadID, cursor string, requestedLimit int) (mobileTimelinePage, error) {
	limit, err := timelineLimit(requestedLimit)
	if err != nil {
		return mobileTimelinePage{}, err
	}
	for {
		page, err := backend.Timeline(m.ctx, threadID, cursor, limit)
		if err != nil {
			return mobileTimelinePage{}, err
		}
		m.applyContentRefs(&page, agentID, threadID, false)
		encoded, _ := json.Marshal(page)
		if len(encoded) <= mobileTimelinePageBytes {
			return page, nil
		}
		if limit == 1 {
			page, err = backend.Timeline(m.ctx, threadID, cursor, limit)
			if err != nil {
				return mobileTimelinePage{}, err
			}
			m.applyContentRefs(&page, agentID, threadID, true)
			return page, nil
		}
		limit = max(limit/2, 1)
	}
}

// backendErrorCode exposes the stable code of a typed backend error.
func backendErrorCode(err error) (string, bool) {
	return provider.ErrorCode(err)
}
