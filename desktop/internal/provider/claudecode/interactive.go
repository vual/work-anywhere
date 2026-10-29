package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

// ensureRuntime waits for executable discovery when a write arrives before
// the background resolution finished.
func (b *Backend) ensureRuntime(ctx context.Context) {
	b.mu.Lock()
	ready := b.runtimeReady
	b.mu.Unlock()
	if !ready {
		b.resolveRuntime(ctx)
	}
}

func (b *Backend) CreateConversation(_ context.Context, projectID string) (provider.ConversationMetadata, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cwd, ok := b.projectRootLocked(projectID, "")
	if !ok {
		return provider.ConversationMetadata{}, provider.NewError("project_not_resolved", "project not found")
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return provider.ConversationMetadata{}, provider.NewError("project_not_resolved", "project directory is not accessible")
	}
	id := newUUID()
	now := time.Now().Unix()
	b.created[id] = &createdConversation{projectID: projectID, cwd: cwd, createdAt: now}
	return provider.ConversationMetadata{ID: id, AgentID: b.agentID, ProjectID: projectID, Title: "New conversation", Status: "idle", CreatedAt: now, UpdatedAt: now}, nil
}

// StartTurn sends one user message. The conversation's process is started
// (resuming the transcript when one exists) if it is not already running.
// Conversations open in another Claude Code process are never written.
func (b *Backend) StartTurn(ctx context.Context, request provider.TurnRequest) (string, error) {
	if request.Settings != nil {
		if err := validSettings(*request.Settings); err != nil {
			return "", err
		}
	}
	b.ensureRuntime(ctx)
	b.mu.Lock()
	id := request.ConversationID
	entry, created := b.files[id], b.created[id]
	if entry == nil && created == nil {
		b.mu.Unlock()
		return "", provider.NewError("not_found", "conversation not found")
	}
	if b.live[id] {
		b.mu.Unlock()
		return "", provider.NewError("conversation_busy_external", "the conversation is open in another Claude Code process on the computer")
	}
	process := b.processes[id]
	if process != nil && (process.turnActive || len(process.requests) > 0) {
		b.mu.Unlock()
		return "", provider.NewError("conversation_busy", "a turn is already active or waiting for an action")
	}
	settings := b.settingsLocked(id)
	if request.Settings != nil {
		settings = normalizedSettings(*request.Settings)
		b.settings[id] = settings
	}
	if process != nil && process.settings != settings {
		// Launch flags carry the settings; restart before the next turn.
		delete(b.processes, id)
		process.stop()
		process = nil
	}
	if process == nil {
		cwd, ok := b.projectRootLocked("", id)
		if !ok {
			b.mu.Unlock()
			return "", provider.NewError("project_not_resolved", "conversation has no project directory")
		}
		resume := entry != nil && entry.summary.HasPrompt
		var err error
		if process, err = b.startProcessLocked(id, cwd, resume, settings); err != nil {
			b.mu.Unlock()
			return "", err
		}
	}
	turnID := turnIDForClientMessage(request.ClientMessageID)
	if request.ClientMessageID != "" {
		if len(b.clientMessages) >= maxClientMessages {
			clear(b.clientMessages)
		}
		b.clientMessages[turnID] = request.ClientMessageID
	}
	process.turnID, process.turnActive, process.interrupting = turnID, true, false
	process.lastUsed = time.Now()
	message := process.userMessage(turnID, request.Text, request.Images)
	b.mu.Unlock()

	if err := process.write(message); err != nil {
		b.mu.Lock()
		process.turnActive = false
		b.mu.Unlock()
		process.kill()
		return "", provider.NewError("provider_error", "send to Claude Code: "+err.Error())
	}
	b.emit(provider.Event{Kind: provider.EventTurnStarted, ConversationID: id, TurnID: turnID})
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: id})
	b.emit(provider.Event{Kind: provider.EventChanged})
	return turnID, nil
}

// InterruptTurn asks the CLI to stop the active turn and kills the process
// if it has not finished within the grace period.
func (b *Backend) InterruptTurn(_ context.Context, conversationID string) error {
	b.mu.Lock()
	process := b.processes[conversationID]
	if process == nil || !process.turnActive {
		b.mu.Unlock()
		return nil
	}
	process.interrupting = true
	turnID := process.turnID
	b.mu.Unlock()
	if err := process.write(map[string]any{"type": "control_request", "request_id": "wa-interrupt-" + randomHex(6), "request": map[string]any{"subtype": "interrupt"}}); err != nil {
		process.kill()
		return nil
	}
	go func() {
		timer := time.NewTimer(interruptGrace)
		defer timer.Stop()
		select {
		case <-process.done:
		case <-timer.C:
			b.mu.Lock()
			stillRunning := process.turnActive && process.turnID == turnID
			b.mu.Unlock()
			if stillRunning {
				process.kill()
			}
		}
	}()
	return nil
}

// Actions lists pending approvals and questions, oldest first. Plan approvals
// are exposed through PlanConfirmation instead.
func (b *Backend) Actions() []provider.Action {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]provider.Action, 0)
	for _, process := range b.processes {
		for _, request := range process.requests {
			if request.kind == actionKindPlan {
				continue
			}
			id, _ := json.Marshal(request.actionID)
			action := provider.Action{ID: id, Kind: request.kind, Method: "claude/canUseTool/" + request.toolName, ConversationID: process.id, TurnID: request.turnID, CreatedAt: request.createdAt, Questions: request.questions, Detail: request.detail}
			result = append(result, action)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
		return string(result[i].ID) < string(result[j].ID)
	})
	return result
}

// takeRequestLocked removes a pending request so the first valid answer wins.
func (b *Backend) takeRequestLocked(match func(*pendingRequest) bool) (*sessionProcess, *pendingRequest) {
	for _, process := range b.processes {
		for key, request := range process.requests {
			if match(request) {
				delete(process.requests, key)
				return process, request
			}
		}
	}
	return nil, nil
}

func (b *Backend) ResolveAction(_ context.Context, id string, result json.RawMessage) error {
	b.mu.Lock()
	var target *pendingRequest
	for _, process := range b.processes {
		if request := process.requests[id]; request != nil && request.kind != actionKindPlan {
			target = request
		}
	}
	if target == nil {
		b.mu.Unlock()
		return provider.NewError("already_resolved", "action is no longer pending")
	}
	allow, updated, message := false, json.RawMessage(nil), declinedMessage
	if target.kind == actionKindUserInput {
		answered, err := answeredInput(target.input, target.questions, result)
		if err != nil {
			b.mu.Unlock()
			return err
		}
		allow, updated = true, answered
	} else {
		var decision struct {
			Decision string `json:"decision"`
		}
		_ = json.Unmarshal(result, &decision)
		switch decision.Decision {
		case "accept", "approve", "allow":
			allow, updated = true, target.input
		case "decline", "deny", "reject":
		default:
			b.mu.Unlock()
			return provider.NewError("invalid_params", "decision must be accept or decline")
		}
	}
	process, request := b.takeRequestLocked(func(candidate *pendingRequest) bool { return candidate == target })
	b.mu.Unlock()
	if request == nil {
		return provider.NewError("already_resolved", "action is no longer pending")
	}
	if err := process.respond(request.requestID, allow, updated, message); err != nil {
		return provider.NewError("provider_error", "answer Claude Code: "+err.Error())
	}
	b.emit(provider.Event{Kind: provider.EventActionResolved, ConversationID: process.id, TurnID: request.turnID})
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: process.id})
	return nil
}

func (b *Backend) PlanConfirmation(conversationID string) *provider.PlanConfirmation {
	b.mu.Lock()
	defer b.mu.Unlock()
	process := b.processes[conversationID]
	if process == nil {
		return nil
	}
	for _, request := range process.requests {
		if request.kind == actionKindPlan {
			return &provider.PlanConfirmation{PlanItemID: request.toolUseID, TurnID: request.turnID}
		}
	}
	return nil
}

// RespondPlan answers ExitPlanMode inside the running turn: implement lets
// Claude Code leave plan mode and continue, revise returns the text as
// feedback so the plan is revised in plan mode.
func (b *Backend) RespondPlan(_ context.Context, conversationID, planItemID, decision, text string) error {
	if decision != "implement" && decision != "revise" {
		return provider.NewError("invalid_params", "decision must be implement or revise")
	}
	b.mu.Lock()
	process, request := b.takeRequestLocked(func(candidate *pendingRequest) bool {
		return candidate.kind == actionKindPlan && candidate.toolUseID == planItemID
	})
	if request == nil || process.id != conversationID {
		if request != nil {
			process.requests[request.actionID] = request
		}
		b.mu.Unlock()
		return provider.NewError("stale_plan", "the plan is no longer waiting for a decision")
	}
	if decision == "implement" {
		settings := b.settingsLocked(conversationID)
		settings.Mode = "default"
		b.settings[conversationID] = settings
		// The running process already left plan mode; keep it reusable.
		process.settings = settings
	}
	b.mu.Unlock()
	message := text
	if message == "" {
		message = defaultReviseText
	}
	if err := process.respond(request.requestID, decision == "implement", request.input, message); err != nil {
		return provider.NewError("provider_error", "answer Claude Code: "+err.Error())
	}
	b.emit(provider.Event{Kind: provider.EventActionResolved, ConversationID: conversationID, TurnID: request.turnID})
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: conversationID})
	return nil
}

func (b *Backend) Settings(conversationID string) provider.ConversationSettings {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.settingsLocked(conversationID)
}

func (b *Backend) settingsLocked(conversationID string) provider.ConversationSettings {
	if settings, ok := b.settings[conversationID]; ok {
		return settings
	}
	return defaultSettings()
}

func normalizedSettings(settings provider.ConversationSettings) provider.ConversationSettings {
	if settings.Mode == "" {
		settings.Mode = "default"
	}
	if settings.ModelID == "" {
		settings.ModelID = defaultModelID
	}
	if settings.PermissionModeID == "" {
		settings.PermissionModeID = "default"
	}
	return settings
}

// UpdateSettings stores settings for the next turn. An idle process is
// stopped so the next turn relaunches with the new flags; a running turn
// keeps its launch settings.
func (b *Backend) UpdateSettings(_ context.Context, conversationID string, settings provider.ConversationSettings) (provider.ConversationSettings, error) {
	if err := validSettings(settings); err != nil {
		return provider.ConversationSettings{}, err
	}
	settings = normalizedSettings(settings)
	b.mu.Lock()
	if b.files[conversationID] == nil && b.created[conversationID] == nil {
		b.mu.Unlock()
		return provider.ConversationSettings{}, provider.NewError("not_found", "conversation not found")
	}
	b.settings[conversationID] = settings
	process := b.processes[conversationID]
	var stale *sessionProcess
	if process != nil && !process.turnActive && len(process.requests) == 0 && process.settings != settings {
		delete(b.processes, conversationID)
		stale = process
	}
	b.mu.Unlock()
	if stale != nil {
		stale.stop()
	}
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: conversationID})
	return settings, nil
}

func (b *Backend) Models(context.Context) (provider.ModelCatalog, error) {
	return modelCatalog(b.configDir), nil
}

func (b *Backend) PermissionModes(context.Context) ([]provider.PermissionMode, error) {
	return permissionModes(), nil
}
