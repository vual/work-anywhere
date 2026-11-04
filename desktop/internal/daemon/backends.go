package daemon

import (
	"encoding/json"
	"os"
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
	Enabled    *bool  `json:"enabled,omitempty"`
	ConfigDir  string `json:"configDir,omitempty"`
	Executable string `json:"executable,omitempty"`
}

// backendVersion is the daemon-side projection for one backend conversation.
// sequence orders every event Mobile receives (including deltas); revision
// changes only on turn and action transitions, which is what writes guard.
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
	options := claudecode.Options{ConfigDir: configDir, Executable: settings.Executable}
	options.ProcessDiagnostic = func(d claudecode.ProcessDiagnostic) {
		if m.log != nil {
			m.log.Event("claude_code.process", map[string]any{"stage": d.Stage, "pid": d.PID, "runtimeVersion": d.RuntimeVersion, "exitCode": d.ExitCode, "win32Code": d.Win32Code})
		}
	}
	if m.job != nil {
		options.Attach = m.job.Add
	}
	backend, err := claudecode.New(options)
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
	return backend.Start(m.ctx, func(event provider.Event) { m.backendEvent(agentID, event) })
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

func (m *Manager) backendList() []provider.Backend {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	backends := make([]provider.Backend, 0, len(m.backends))
	for _, backend := range m.backends {
		backends = append(backends, backend)
	}
	return backends
}

func (m *Manager) backendActiveTurns() int {
	active := 0
	for _, backend := range m.backendList() {
		active += backend.ActiveTurns()
	}
	return active
}

func (m *Manager) backendAgents() []mobileAgentSummary {
	backends := m.backendList()
	result := make([]mobileAgentSummary, 0, len(backends))
	for _, backend := range backends {
		result = append(result, backend.Agent())
	}
	return result
}

// backendTarget resolves the Mobile agentId to a non-Codex backend. The
// registry key is the agent ID, so callers need not query the backend.
func (m *Manager) backendTarget(raw json.RawMessage) (provider.Backend, string, bool) {
	var target struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(raw, &target) != nil || target.AgentID == "" {
		return nil, "", false
	}
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	backend, ok := m.backends[target.AgentID]
	return backend, target.AgentID, ok
}

func (m *Manager) backendVersionOf(agentID, conversationID string) backendVersion {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	if version := m.backendVersions[agentID+"\x00"+conversationID]; version != nil {
		return *version
	}
	return backendVersion{}
}

func (m *Manager) advanceBackendVersion(agentID, conversationID string, revision bool) backendVersion {
	m.backendMu.Lock()
	defer m.backendMu.Unlock()
	if m.backendVersions == nil {
		m.backendVersions = make(map[string]*backendVersion)
	}
	key := agentID + "\x00" + conversationID
	version := m.backendVersions[key]
	if version == nil {
		version = &backendVersion{}
		m.backendVersions[key] = version
	}
	version.sequence++
	if revision {
		version.revision++
	}
	return *version
}

// backendEventNames maps backend events onto the event names the remote
// connector already classifies: delta names are batched as
// conversation.delta, action requests raise an action-required hint and
// terminal turn events a turn-completed hint.
var backendEventNames = map[provider.EventKind]string{
	provider.EventChanged:         "provider.conversation.updated",
	provider.EventActionRequested: "action.requested",
	provider.EventActionResolved:  "action.resolved",
	provider.EventTurnStarted:     "provider.turn/started",
	provider.EventTurnCompleted:   "provider.turn/completed",
	provider.EventTurnFailed:      "provider.turn/failed",
	provider.EventTurnInterrupted: "provider.turn/interrupted",
}

var backendDeltaNames = map[string]string{
	provider.DeltaAssistantText: "provider.item/agentMessage/delta",
	provider.DeltaReasoning:     "provider.item/reasoning/textDelta",
}

func (m *Manager) backendEvent(agentID string, event provider.Event) {
	if event.ConversationID == "" {
		m.publish(protocol.Event{Type: "event", SyncEpoch: m.syncEpoch, Event: "provider.conversations.changed", InstanceID: agentID})
		return
	}
	name := backendEventNames[event.Kind]
	payload := map[string]any{"threadId": event.ConversationID}
	if event.TurnID != "" {
		payload["turnId"] = event.TurnID
	}
	if event.Kind == provider.EventDelta {
		name = backendDeltaNames[event.DeltaKind]
		if name == "" || event.TurnID == "" || event.ItemID == "" || event.Delta == "" {
			return
		}
		payload = map[string]any{"params": map[string]any{"threadId": event.ConversationID, "turnId": event.TurnID, "itemId": event.ItemID, "delta": event.Delta}}
	}
	if name == "" {
		return
	}
	revision := event.Kind != provider.EventChanged && event.Kind != provider.EventDelta
	version := m.advanceBackendVersion(agentID, event.ConversationID, revision)
	m.publish(protocol.Event{Type: "event", Sequence: version.sequence, SyncEpoch: m.syncEpoch, Event: name, InstanceID: agentID, ConversationID: event.ConversationID, Payload: payload})
}

func unsupported(method string) error {
	return &mobileContractError{code: "unsupported_capability", message: method + " is not supported for this agent"}
}

// backendControl serves Mobile contract methods for a provider.Backend. It
// returns false when the method is not an agent method, leaving it to the
// shared daemon switch.
func (m *Manager) backendControl(backend provider.Backend, agentID string, request protocol.ControlRequest, response *protocol.ControlResponse) bool {
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
		result, err = m.backendContentRead(backend, agentID, request.Params)
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
		status := "idle"
		if metadata, readErr := backend.ReadConversation(m.ctx, params.ThreadID); readErr == nil {
			status = metadata.Status
		}
		// Backend events carry no replayable payload, so any gap is resolved
		// by a fresh snapshot rather than an event replay.
		result = map[string]any{"threadId": params.ThreadID, "status": status, "revision": version.revision, "sequence": version.sequence, "syncEpoch": m.syncEpoch, "events": []protocol.Event{}, "snapshot": params.Cursor != version.sequence}
	case "conversation.create":
		var params struct {
			ProjectID string `json:"projectId"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ProjectID == "" {
			err = &mobileContractError{code: "invalid_params", message: "projectId is required"}
			break
		}
		var metadata mobileConversationMetadata
		if metadata, err = backend.CreateConversation(m.ctx, params.ProjectID); err == nil {
			result = map[string]any{"id": metadata.ID, "projectId": metadata.ProjectID}
		}
	case "turn.start":
		result, err = m.backendTurnStart(backend, agentID, request.Params)
	case "turn.interrupt":
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" {
			err = &mobileContractError{code: "invalid_params", message: "threadId is required"}
			break
		}
		if err = backend.InterruptTurn(m.ctx, params.ThreadID); err == nil {
			result = map[string]any{}
		}
	case "conversation.rename":
		var threadID, name string
		if threadID, name, err = conversationRenameParams(request.Params); err != nil {
			break
		}
		if err = backend.RenameConversation(m.ctx, threadID, name); err == nil {
			result = map[string]any{"threadId": threadID, "name": name}
		}
	case "action.list":
		result = backend.Actions()
	case "action.resolve":
		var params struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		var id string
		if json.Unmarshal(request.Params, &params) != nil || json.Unmarshal(params.ID, &id) != nil || id == "" {
			err = &mobileContractError{code: "invalid_params", message: "id and result are required"}
			break
		}
		if err = backend.ResolveAction(m.ctx, id, params.Result); err == nil {
			result = map[string]any{"resolved": true}
		}
	case "plan.respond":
		var params struct {
			ThreadID         string `json:"threadId"`
			PlanItemID       string `json:"planItemId"`
			ExpectedRevision *int64 `json:"expectedRevision"`
			Decision         string `json:"decision"`
			Text             string `json:"text"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" || params.PlanItemID == "" {
			err = &mobileContractError{code: "invalid_params", message: "threadId and planItemId are required"}
			break
		}
		if err = m.checkBackendRevision(agentID, params.ThreadID, params.ExpectedRevision); err != nil {
			break
		}
		if err = backend.RespondPlan(m.ctx, params.ThreadID, params.PlanItemID, params.Decision, strings.TrimSpace(params.Text)); err == nil {
			result = map[string]any{"started": true}
		}
	case "conversation.settings.update":
		var params struct {
			ThreadID         string                     `json:"threadId"`
			ExpectedRevision *int64                     `json:"expectedRevision"`
			Settings         mobileConversationSettings `json:"settings"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID == "" {
			err = &mobileContractError{code: "invalid_params", message: "threadId and settings are required"}
			break
		}
		if err = m.checkBackendRevision(agentID, params.ThreadID, params.ExpectedRevision); err != nil {
			break
		}
		result, err = backend.UpdateSettings(m.ctx, params.ThreadID, params.Settings)
	case "catalog.models":
		result, err = backend.Models(m.ctx)
	case "catalog.permissionProfiles":
		var modes []provider.PermissionMode
		if modes, err = backend.PermissionModes(m.ctx); err == nil {
			catalog := mobilePermissionCatalog{Items: make([]mobilePermissionMode, 0, len(modes))}
			for _, mode := range modes {
				catalog.Items = append(catalog.Items, mobilePermissionMode{ID: mode.ID, Available: mode.Available, Reason: mode.Reason, Dangerous: mode.Dangerous})
			}
			result = catalog
		}
	case "attachment.begin":
		var params attachmentBeginParams
		if json.Unmarshal(request.Params, &params) != nil {
			err = &mobileContractError{code: "invalid_params", message: "invalid attachment params"}
			break
		}
		root, ok := backend.ProjectRoot(params.ProjectID, params.ConversationID)
		if !ok {
			err = &mobileContractError{code: "project_not_resolved", message: "conversation has no project directory"}
			break
		}
		if root, err = existingDirectory(root); err != nil {
			err = &mobileContractError{code: "project_not_resolved", message: err.Error()}
			break
		}
		params.InstanceID = agentID
		var controlErr *protocol.ControlError
		if result, controlErr = m.beginAttachment(params, root); controlErr != nil {
			response.Error = controlErr
			return true
		}
	default:
		if strings.HasPrefix(request.Method, "conversation.queue.") {
			err = unsupported(request.Method)
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

func (m *Manager) checkBackendRevision(agentID, conversationID string, expected *int64) error {
	if expected == nil {
		return nil
	}
	if current := m.backendVersionOf(agentID, conversationID).revision; current != *expected {
		return &mobileContractError{code: "revision_conflict", message: "conversation changed; reload before retrying"}
	}
	return nil
}

func (m *Manager) backendTurnStart(backend provider.Backend, agentID string, raw json.RawMessage) (any, error) {
	var params struct {
		ThreadID         string                      `json:"threadId"`
		Text             string                      `json:"text"`
		AttachmentIDs    []string                    `json:"attachmentIds"`
		ClientMessageID  string                      `json:"clientMessageId"`
		ExpectedRevision *int64                      `json:"expectedRevision"`
		Settings         *mobileConversationSettings `json:"settings"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID == "" {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	if strings.TrimSpace(params.Text) == "" && len(params.AttachmentIDs) == 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "text or attachments are required"}
	}
	if err := m.checkBackendRevision(agentID, params.ThreadID, params.ExpectedRevision); err != nil {
		return nil, err
	}
	images, err := m.backendImages(agentID, params.ThreadID, params.AttachmentIDs)
	if err != nil {
		return nil, err
	}
	turnID, err := backend.StartTurn(m.ctx, provider.TurnRequest{ConversationID: params.ThreadID, ClientMessageID: params.ClientMessageID, Text: params.Text, Images: images, Settings: params.Settings})
	if err != nil {
		return nil, err
	}
	m.releaseAttachments(params.AttachmentIDs)
	return map[string]any{"turn": map[string]any{"id": turnID}}, nil
}

// backendImages loads committed image uploads that belong to this agent and
// conversation. Only images are accepted, matching the Codex path.
func (m *Manager) backendImages(agentID, conversationID string, ids []string) ([]provider.Image, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	m.imageEncode <- struct{}{}
	defer func() { <-m.imageEncode }()
	images := make([]provider.Image, 0, len(ids))
	for _, id := range ids {
		m.uploadMu.Lock()
		attachment := m.attachments[id]
		m.uploadMu.Unlock()
		if attachment == nil || attachment.InstanceID != agentID || attachment.ConversationID != conversationID {
			return nil, &mobileContractError{code: "invalid_params", message: "attachment " + id + " is not available for this conversation"}
		}
		if !strings.HasPrefix(attachment.MimeType, "image/") {
			return nil, &mobileContractError{code: "unsupported_attachment_type", message: "attachment " + id + " is not an image"}
		}
		data, err := os.ReadFile(attachment.Path)
		if err != nil {
			return nil, &mobileContractError{code: "attachment_store_error", message: err.Error()}
		}
		images = append(images, provider.Image{MimeType: attachment.MimeType, Data: data})
	}
	return images, nil
}

func (m *Manager) backendContentRead(backend provider.Backend, agentID string, raw json.RawMessage) (any, error) {
	var params struct {
		ThreadID   string `json:"threadId"`
		ContentRef string `json:"contentRef"`
		Offset     int    `json:"offset"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID == "" || params.ContentRef == "" || params.Offset < 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, contentRef and a non-negative offset are required"}
	}
	ref, err := m.resolveContentRef(params.ContentRef, agentID, params.ThreadID, time.Now())
	if err != nil {
		return nil, err
	}
	text, err := backend.TimelineBlock(m.ctx, ref.ThreadID, ref.TurnID, ref.ItemID, ref.BlockIndex)
	if err != nil {
		return nil, err
	}
	chunk, next, hasMore, chunkErr := utf8ContentChunk(text, params.Offset)
	if chunkErr != nil {
		return nil, &mobileContractError{code: "invalid_params", message: chunkErr.Error()}
	}
	return map[string]any{"text": chunk, "nextOffset": next, "hasMore": hasMore, "totalBytes": len(text)}, nil
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
	settings := backend.Settings(request.ThreadID)
	snapshot := mobileConversationSnapshot{
		Conversation: metadata,
		Settings:     &settings,
		Revision:     version.revision,
		Sequence:     version.sequence,
		SyncEpoch:    m.syncEpoch,
		Timeline:     page,
		Queue:        mobileQueuePage{Items: []mobileQueuedSubmission{}},
	}
	if confirmation := backend.PlanConfirmation(request.ThreadID); confirmation != nil {
		confirmation.Revision = version.revision
		snapshot.PlanConfirmation = confirmation
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

// conversationRenameParams decodes and validates conversation.rename for
// every agent kind so Codex and provider backends reject the same names.
func conversationRenameParams(raw json.RawMessage) (string, string, error) {
	var params struct {
		ThreadID string `json:"threadId"`
		Name     string `json:"name"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID == "" {
		return "", "", &mobileContractError{code: "invalid_params", message: "threadId and name are required"}
	}
	name, ok := provider.ValidConversationName(params.Name)
	if !ok {
		return "", "", &mobileContractError{code: "invalid_params", message: "name must be 1-200 characters on a single line"}
	}
	return params.ThreadID, name, nil
}
