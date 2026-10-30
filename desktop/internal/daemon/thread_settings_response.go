package daemon

import (
	"encoding/json"
)

// Only retain settings metadata, never the request's instructions or content.
type threadSettingsRequest struct {
	mode    string
	version uint64
}

func (i *Instance) threadSettingsRequestLocked(method string, params json.RawMessage) threadSettingsRequest {
	if method != "thread/start" && method != "thread/resume" {
		return threadSettingsRequest{}
	}
	var request struct {
		CollaborationMode struct {
			Mode string `json:"mode"`
		} `json:"collaborationMode"`
	}
	_ = json.Unmarshal(params, &request)
	return threadSettingsRequest{mode: request.CollaborationMode.Mode, version: i.settingsClock}
}

func settingsFromThreadResponse(method string, request threadSettingsRequest, raw json.RawMessage, current threadCollaborationSettings) (string, threadCollaborationSettings, bool) {
	var response map[string]json.RawMessage
	if json.Unmarshal(raw, &response) != nil {
		return "", threadCollaborationSettings{}, false
	}
	var thread struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(response["thread"], &thread)
	var model string
	_ = json.Unmarshal(response["model"], &model)
	policyRaw := response["sandboxPolicy"]
	if len(policyRaw) == 0 {
		policyRaw = response["sandbox"]
	}
	policy, validPolicy := decodeSandboxPolicy(string(policyRaw))
	if thread.ID == "" || model == "" || !validPolicy || len(response["approvalPolicy"]) == 0 {
		return thread.ID, threadCollaborationSettings{}, false
	}
	settings := current
	settings.Model = model
	settings.ReasoningEffort = ""
	_ = json.Unmarshal(response["reasoningEffort"], &settings.ReasoningEffort)
	var collaboration struct {
		Mode     string `json:"mode"`
		Settings struct {
			Model  string  `json:"model"`
			Effort *string `json:"reasoning_effort"`
		} `json:"settings"`
	}
	_ = json.Unmarshal(response["collaborationMode"], &collaboration)
	if collaboration.Mode == "default" || collaboration.Mode == "plan" {
		settings.Mode = collaboration.Mode
		if collaboration.Settings.Model != "" {
			settings.Model = collaboration.Settings.Model
		}
		if collaboration.Settings.Effort != nil {
			settings.ReasoningEffort = *collaboration.Settings.Effort
		}
	} else if method == "thread/start" {
		settings.Mode = "default"
		if request.mode == "plan" {
			settings.Mode = "plan"
		}
	}
	// Older resume responses cannot prove a collaboration mode we never observed.
	if settings.Mode != "default" && settings.Mode != "plan" {
		return thread.ID, settings, false
	}
	settings.SandboxMode, _ = policy["type"].(string)
	encoded, _ := json.Marshal(policy)
	settings.SandboxPolicyJSON = string(encoded)
	settings.PermissionProfile = sandboxProfile(settings.SandboxMode)
	var active struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response["activePermissionProfile"], &active) == nil && active.ID != "" {
		settings.PermissionProfile = active.ID
	}
	settings.ApprovalPolicy, settings.ApprovalsReviewer = "", ""
	settings.ApprovalPolicyJSON = string(response["approvalPolicy"])
	_ = json.Unmarshal(response["approvalPolicy"], &settings.ApprovalPolicy)
	_ = json.Unmarshal(response["approvalsReviewer"], &settings.ApprovalsReviewer)
	settings.PermissionModeID = permissionModeID(settings.PermissionProfile, settings.ApprovalPolicy, settings.ApprovalsReviewer)
	if settings.PermissionModeID == "" {
		settings.PermissionModeID = "custom"
	}
	return thread.ID, settings, true
}

func (i *Instance) observeThreadSettingsResponse(generation uint64, origin requestOrigin, result json.RawMessage) {
	if origin.method != "thread/start" && origin.method != "thread/resume" {
		return
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &response) != nil || response.Thread.ID == "" {
		return
	}
	threadID := response.Thread.ID
	// Load persisted settings before taking the commit lock, then re-read under it.
	_, _ = i.collaborationSettings(threadID)
	i.mu.Lock()
	if generation != i.generation || i.settingsNotifications[threadID] > origin.settingsContext.version {
		i.mu.Unlock()
		return
	}
	current := i.threadSettings[threadID]
	_, settings, ok := settingsFromThreadResponse(origin.method, origin.settingsContext, result, current)
	if !ok || settings == current {
		i.mu.Unlock()
		return
	}
	i.threadSettings[threadID] = settings
	i.settingsClock++
	i.settingsNotifications[threadID] = i.settingsClock
	i.mu.Unlock()
	i.persistThreadSettingsAsync(threadID, settings)
	i.broadcastEvent("frontend.thread.settings.updated", map[string]any{"threadId": threadID})
}
