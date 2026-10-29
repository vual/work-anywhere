package daemon

import (
	"encoding/json"
	"strings"
)

// Policy is stored as JSON text so settings remain comparable and unknown
// provider fields survive a model/mode change without sharing mutable maps.
func decodeSandboxPolicy(raw string) (map[string]any, bool) {
	var policy map[string]any
	if json.Unmarshal([]byte(raw), &policy) != nil || policy == nil {
		return nil, false
	}
	kind, _ := policy["type"].(string)
	return policy, kind != ""
}

func sandboxProfile(kind string) string {
	switch kind {
	case "workspaceWrite":
		return ":workspace"
	case "readOnly":
		return ":read-only"
	case "dangerFullAccess":
		return ":danger-full-access"
	}
	return ""
}

func (m *Manager) permissionPolicy(caller mobileProviderCaller, threadID string, requested string, current threadCollaborationSettings) (map[string]any, error) {
	if requested == current.PermissionModeID {
		if policy, ok := decodeSandboxPolicy(current.SandboxPolicyJSON); ok {
			return policy, nil
		}
	}
	if requested == "custom" {
		return nil, &mobileContractError{code: "selection_unavailable", message: "the current custom permission policy is unavailable"}
	}
	switch requested {
	case "fullAccess":
		return map[string]any{"type": "dangerFullAccess"}, nil
	case "readOnly":
		return map[string]any{"type": "readOnly", "networkAccess": false}, nil
	case "default", "autoReview":
		// Changing only the reviewer must not change workspace access.
		if policy, ok := decodeSandboxPolicy(current.SandboxPolicyJSON); ok && policy["type"] == "workspaceWrite" {
			return policy, nil
		}
	default:
		return nil, &mobileContractError{code: "invalid_selection", message: "unknown permission mode"}
	}
	threadParams, _ := json.Marshal(map[string]any{"threadId": threadID, "includeTurns": false})
	raw, err := caller.Call(m.ctx, "thread/read", threadParams)
	var thread struct {
		Thread struct {
			CWD string `json:"cwd"`
		} `json:"thread"`
	}
	if err != nil || json.Unmarshal(raw, &thread) != nil || strings.TrimSpace(thread.Thread.CWD) == "" {
		return nil, &mobileContractError{code: "selection_unavailable", message: "project configuration is unavailable for this thread"}
	}
	configParams, _ := json.Marshal(map[string]any{"cwd": thread.Thread.CWD, "includeLayers": false})
	raw, err = caller.Call(m.ctx, "config/read", configParams)
	var config struct {
		Config *struct {
			Workspace *struct {
				Network    bool     `json:"network_access"`
				Roots      []string `json:"writable_roots"`
				ExcludeTmp bool     `json:"exclude_slash_tmp"`
				ExcludeEnv bool     `json:"exclude_tmpdir_env_var"`
			} `json:"sandbox_workspace_write"`
		} `json:"config"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || config.Config == nil {
		return nil, &mobileContractError{code: "selection_unavailable", message: "effective workspace sandbox configuration is unavailable"}
	}
	policy := map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{}, "excludeSlashTmp": false, "excludeTmpdirEnvVar": false}
	if workspace := config.Config.Workspace; workspace != nil {
		policy["networkAccess"], policy["excludeSlashTmp"], policy["excludeTmpdirEnvVar"] = workspace.Network, workspace.ExcludeTmp, workspace.ExcludeEnv
		if workspace.Roots != nil {
			policy["writableRoots"] = workspace.Roots
		}
	}
	return policy, nil
}

func readOnlyProviderMethod(method string) bool {
	switch method {
	case "initialize", "thread/read", "thread/list", "thread/turns/list", "thread/items/list", "thread/queue/list", "model/list", "permissionProfile/list", "config/read", "configRequirements/read", "project/list":
		return true
	}
	return false
}
