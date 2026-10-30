package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
)

const startedSettings = `{"thread":{"id":"fresh","futureThreadField":true},"model":"model-a","reasoningEffort":null,"approvalPolicy":"on-request","approvalsReviewer":"user","sandbox":{"type":"workspaceWrite","writableRoots":["D:/safe"],"networkAccess":false,"futurePolicy":42},"futureResponse":true}`

func TestThreadResponseSettingsMapping(t *testing.T) {
	for _, tc := range []struct {
		name, method, result, mode, permission, effort string
		current                                        threadCollaborationSettings
		valid                                          bool
	}{
		{name: "stable start", method: "thread/start", result: startedSettings, mode: "default", permission: "default", valid: true},
		{name: "resume retains plan", method: "thread/resume", result: startedSettings, current: threadCollaborationSettings{Mode: "plan", ReasoningEffort: "high"}, mode: "plan", permission: "default", valid: true},
		{name: "unknown resume mode", method: "thread/resume", result: startedSettings},
		{name: "experimental", method: "thread/start", result: strings.TrimSuffix(startedSettings, "}") + `,"activePermissionProfile":{"id":":read-only"},"collaborationMode":{"mode":"plan","settings":{"model":"model-b","reasoning_effort":"high"}}}`, mode: "plan", permission: "readOnly", effort: "high", valid: true},
		{name: "custom policy", method: "thread/start", result: strings.Replace(startedSettings, `"on-request"`, `{"reject":{"sandbox_approval":true,"future":true}}`, 1), mode: "default", permission: "custom", valid: true},
		{name: "missing policy", method: "thread/start", result: `{"thread":{"id":"fresh"},"model":"model-a"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, got, ok := settingsFromThreadResponse(tc.method, threadSettingsRequest{}, json.RawMessage(tc.result), tc.current)
			if ok != tc.valid {
				t.Fatalf("valid=%v settings=%#v", ok, got)
			}
			if !ok {
				return
			}
			if id != "fresh" || got.Mode != tc.mode || got.PermissionModeID != tc.permission || got.ReasoningEffort != tc.effort || !strings.Contains(got.SandboxPolicyJSON, `"futurePolicy":42`) {
				t.Fatalf("settings=%#v", got)
			}
		})
	}
	_, got, ok := settingsFromThreadResponse("thread/start", threadSettingsRequest{mode: "plan"}, json.RawMessage(startedSettings), threadCollaborationSettings{})
	if !ok || got.Mode != "plan" {
		t.Fatalf("request mode lost: %#v", got)
	}
}

func TestThreadSettingsPublishedBeforeInternalResponseAndPersisted(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	fakeHistoryServer(t, i, func(method string, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(startedSettings), nil
	})
	if _, err := i.Call(context.Background(), "thread/start", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	settings, ok := i.collaborationSettings("fresh")
	if !ok || settings.Model != "model-a" || settings.PermissionModeID != "default" {
		t.Fatalf("missing settings at response: %#v", settings)
	}
	if revision, _ := i.ConversationVersion("fresh"); revision == 0 {
		t.Fatal("settings did not invalidate snapshot")
	}
	i.manager.configWrites.Wait()
	i.mu.Lock()
	i.threadSettings = make(map[string]threadCollaborationSettings)
	i.mu.Unlock()
	restored, ok := i.collaborationSettings("fresh")
	if !ok || restored != settings {
		t.Fatalf("restore mismatch %#v", restored)
	}
}

func TestThreadSettingsDesktopResponseAndStaleProtection(t *testing.T) {
	for _, scenario := range []string{"success", "error", "notification", "old generation"} {
		t.Run(scenario, func(t *testing.T) {
			i, writer, cleanup := testInstance(t)
			defer cleanup()
			defer i.manager.configWrites.Wait()
			i.state = "ready"
			desktop := compatDesktop(i, "settings-desktop")
			if err := i.HandleDesktop(desktop, []byte(`{"id":17,"method":"thread/start","params":{"collaborationMode":{"mode":"plan"}},"trace":{"future":true}}`)); err != nil {
				t.Fatal(err)
			}
			var request struct{ ID json.RawMessage }
			if err := json.Unmarshal(writer.Bytes(), &request); err != nil {
				t.Fatal(err)
			}
			response := map[string]any{"id": request.ID, "result": json.RawMessage(startedSettings)}
			generation := i.generation
			if scenario == "notification" {
				i.observeThreadSettingsNotification(json.RawMessage(`{"threadId":"fresh","threadSettings":{"sandboxPolicy":{"type":"readOnly"},"approvalPolicy":"on-request","approvalsReviewer":"user","collaborationMode":{"mode":"default","settings":{"model":"newer","reasoning_effort":"low"}}}}`))
			}
			if scenario == "error" {
				delete(response, "result")
				response["error"] = map[string]any{"code": -1, "message": "failure"}
			}
			if scenario == "old generation" {
				i.generation++
			}
			i.handleServerFrame(generation, mustJSON(response))
			settings, exists := i.collaborationSettings("fresh")
			switch scenario {
			case "success":
				if !exists || settings.Mode != "plan" {
					t.Fatalf("missing settings %#v", settings)
				}
			case "notification":
				if !exists || settings.Model != "newer" {
					t.Fatalf("overwrote notification %#v", settings)
				}
			default:
				if exists {
					t.Fatalf("published failed/stale response %#v", settings)
				}
			}
			if scenario != "old generation" {
				select {
				case frame := <-desktop.outbound:
					if frame.Kind != ipc.KindServerFrame || !strings.Contains(string(frame.Payload), `"id":17`) {
						t.Fatalf("bad restored response %s", frame.Payload)
					}
					if scenario != "error" && !strings.Contains(string(frame.Payload), `"futureResponse":true`) {
						t.Fatal("unknown response field lost")
					}
				default:
					t.Fatal("response not forwarded")
				}
			}
		})
	}
}
