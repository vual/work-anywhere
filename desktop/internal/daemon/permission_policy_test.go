package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type policyCaller struct {
	config  string
	methods []string
}

func (c *policyCaller) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	c.methods = append(c.methods, method)
	switch method {
	case "thread/read":
		return json.RawMessage(`{"thread":{"cwd":"D:/project"}}`), nil
	case "config/read":
		if c.config != "" {
			return json.RawMessage(c.config), nil
		}
	case "model/list":
		return json.RawMessage(`{"data":[{"id":"m","supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}`), nil
	case "permissionProfile/list":
		return json.RawMessage(`{"data":[{"id":":workspace","allowed":true}]}`), nil
	case "configRequirements/read":
		return json.RawMessage(`{"requirements":null}`), nil
	}
	return nil, errors.New("unavailable")
}

func TestFullWorkspacePolicySurvivesSettingsAndHydratesLegacy(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	c := &policyCaller{config: `{"config":{"sandbox_workspace_write":{"network_access":true,"writable_roots":["D:/extra"],"exclude_slash_tmp":true,"exclude_tmpdir_env_var":true}}}`}
	policy, err := m.permissionPolicy(c, "t", "default", threadCollaborationSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if policy["networkAccess"] != true || policy["excludeSlashTmp"] != true || policy["excludeTmpdirEnvVar"] != true || !reflect.DeepEqual(policy["writableRoots"], []string{"D:/extra"}) {
		t.Fatalf("policy lost effective config: %#v", policy)
	}
	raw, _ := json.Marshal(policy)
	current := threadCollaborationSettings{PermissionModeID: "default", SandboxPolicyJSON: string(raw)}
	c.methods = nil
	_, err = m.permissionPolicy(c, "t", "autoReview", current)
	if err != nil || len(c.methods) != 0 {
		t.Fatalf("reviewer change rebuilt sandbox: %v %v", err, c.methods)
	}
	c.config = ""
	if _, err = m.permissionPolicy(c, "t", "default", threadCollaborationSettings{}); err == nil {
		t.Fatal("missing configuration guessed")
	}
}

func TestSandboxOnlySettingsProjectionAndCustom(t *testing.T) {
	for _, tt := range []struct{ profile, kind, approval, reviewer, want string }{
		{"", "workspaceWrite", "on-request", "user", "default"},
		{"", "workspaceWrite", "on-request", "auto_review", "autoReview"},
		{"", "readOnly", "on-request", "user", "readOnly"},
		{"", "dangerFullAccess", "never", "user", "fullAccess"},
		{"", "workspaceWrite", "never", "user", "custom"},
		{"named-profile", "workspaceWrite", "on-request", "user", "custom"},
	} {
		settings := map[string]any{"sandboxPolicy": map[string]any{"type": tt.kind, "networkAccess": true}, "approvalPolicy": tt.approval, "approvalsReviewer": tt.reviewer, "collaborationMode": map[string]any{"mode": "default", "settings": map[string]any{"model": "m"}}}
		if tt.profile != "" {
			settings["activePermissionProfile"] = map[string]any{"id": tt.profile}
		}
		raw, _ := json.Marshal(map[string]any{"threadId": "t", "threadSettings": settings})
		_, got, ok := collaborationSettingsFromParams(raw, true)
		if !ok || got.PermissionModeID != tt.want || got.SandboxPolicyJSON == "" {
			t.Fatalf("%+v => %+v", tt, got)
		}
	}
}

func TestPermissionRequestsMatchBothProtocolFixtures(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	c := &policyCaller{}
	params, _, err := m.providerThreadSettings(c, mobileConversationSettings{Mode: "plan", ModelID: "m", ReasoningEffort: "high", PermissionModeID: "default"}, threadCollaborationSettings{PermissionModeID: "default", SandboxPolicyJSON: `{"type":"workspaceWrite","networkAccess":true,"writableRoots":["D:/extra"],"excludeSlashTmp":true,"excludeTmpdirEnvVar":false}`}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := params["permissions"]; exists {
		t.Fatal("mutually exclusive permissions field emitted")
	}
	for _, version := range []string{"0.151.0-alpha.7.2", "0.159.2"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "app-server-schema", version, "v2", "TurnStartParams.json"))
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err = json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"sandboxPolicy", "approvalPolicy", "approvalsReviewer"} {
			if schema.Properties[key] == nil {
				t.Fatalf("%s does not support %s", version, key)
			}
		}
	}
}

func TestCustomPolicyModelChangeInheritsLivePermissions(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	params, projected, err := m.providerThreadSettings(&policyCaller{}, mobileConversationSettings{Mode: "plan", ModelID: "m", ReasoningEffort: "high", PermissionModeID: "custom"}, threadCollaborationSettings{PermissionModeID: "custom", SandboxPolicyJSON: `{"type":"externalSandbox"}`}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if projected.PermissionModeID != "custom" || params["sandboxPolicy"] != nil || params["approvalPolicy"] != nil {
		t.Fatalf("custom policy overwritten: %#v", params)
	}
}

func TestLegacyPolicyModelChangeDoesNotGuessNetworkOrRoots(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	params, projected, err := m.providerThreadSettings(&policyCaller{}, mobileConversationSettings{Mode: "plan", ModelID: "m", ReasoningEffort: "high", PermissionModeID: "default"}, threadCollaborationSettings{PermissionModeID: "default", PermissionProfile: ":workspace"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if params["sandboxPolicy"] != nil || projected.SandboxPolicyJSON != "" {
		t.Fatal("legacy policy guessed")
	}
}

func TestHydratedSettingsAcceptFirstTurnAndInheritedEffort(t *testing.T) {
	m := &Manager{ctx: context.Background()}
	raw := json.RawMessage(strings.ReplaceAll(startedSettings, "model-a", "m"))
	_, current, ok := settingsFromThreadResponse("thread/start", threadSettingsRequest{}, raw, threadCollaborationSettings{})
	if !ok {
		t.Fatal("response not hydrated")
	}
	requested := projectedMobileSettings(current)
	params, _, err := m.providerThreadSettings(&policyCaller{}, requested, current, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if params["effort"] != nil {
		t.Fatalf("inherited effort changed: %#v", params)
	}
	policy := params["sandboxPolicy"].(map[string]any)
	if policy["futurePolicy"] != float64(42) {
		t.Fatal("unknown sandbox field lost")
	}
	requested.PermissionModeID = "custom"
	if _, _, err = m.providerThreadSettings(&policyCaller{}, requested, current, "fresh"); !historyError(err, "invalid_selection") {
		t.Fatalf("invented custom accepted: %v", err)
	}
}
