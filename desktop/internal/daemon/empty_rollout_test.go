package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedEmptyRolloutBoundaries(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "session.jsonl")
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"matching", `{"type":"session_meta","payload":{"id":"t"}}`, true},
		{"wrong id", `{"type":"session_meta","payload":{"id":"other"}}`, false},
		{"missing id", `{"type":"session_meta","payload":{}}`, false},
		{"empty", "", false},
		{"corrupt", "{", false},
		{"content", "{\"type\":\"session_meta\",\"payload\":{\"id\":\"t\"}}\n{\"type\":\"response_item\"}", false},
		{"too large", strings.Repeat(" ", 1<<20+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := verifiedEmptyRollout(home, path, "t"); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
	outside := filepath.Join(home, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session_meta","payload":{"id":"t"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if verifiedEmptyRollout(home, outside, "t") {
		t.Fatal("accepted outside sessions")
	}
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(outside, link); err == nil && verifiedEmptyRollout(home, link, "t") {
		t.Fatal("accepted symlink escape")
	}
}

func TestEmptySnapshotAfterRestartRequiresDiskEvidence(t *testing.T) {
	for _, scenario := range []string{"empty", "active", "pending", "content", "ordinary error", "changes during read"} {
		t.Run(scenario, func(t *testing.T) {
			i, _, cleanup := testInstance(t)
			defer cleanup()
			i.descriptor.CodexHome = t.TempDir()
			root := filepath.Join(i.descriptor.CodexHome, "sessions")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "session.jsonl")
			if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"t"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "active" {
				i.activeTurns["t"] = "turn"
			}
			if scenario == "pending" {
				i.pendingTurnStarts["t"] = true
			}
			if scenario == "content" {
				if err := os.WriteFile(path, []byte(`{"type":"response_item"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			fakeHistoryServer(t, i, func(method string, params json.RawMessage) (json.RawMessage, error) {
				if method == "thread/read" {
					var p struct {
						IncludeTurns bool `json:"includeTurns"`
					}
					_ = json.Unmarshal(params, &p)
					if !p.IncludeTurns {
						return mustJSON(map[string]any{"thread": map[string]any{"id": "t", "path": path, "status": map[string]any{"type": "idle"}}}), nil
					}
				}
				if scenario == "changes during read" {
					_ = os.WriteFile(path, []byte(`{"type":"response_item"}`), 0600)
				}
				if scenario == "ordinary error" {
					return nil, errors.New(`App Server: {"code":-32603,"message":"permission denied"}`)
				}
				return nil, errors.New(`App Server: {"code":-32601,"message":"list_turns is not supported yet"}`)
			})
			snapshot, err := i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"t"}`))
			if scenario == "empty" {
				if err != nil || snapshot.Settings != nil || len(snapshot.Timeline.Items) != 0 {
					t.Fatalf("snapshot %#v %v", snapshot, err)
				}
				raw, _ := json.Marshal(snapshot)
				if !strings.Contains(string(raw), `"settings":null`) {
					t.Fatalf("unknown settings fabricated: %s", raw)
				}
			} else if err == nil {
				t.Fatal("unsafe empty fallback")
			}
		})
	}
}

func TestEmptyResumeOnlyRunsForExplicitWriteAndPreservesProviderFailure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[reject], func(t *testing.T) {
			i, _, cleanup := testInstance(t)
			defer cleanup()
			defer i.manager.configWrites.Wait()
			i.descriptor.CodexHome = t.TempDir()
			root := filepath.Join(i.descriptor.CodexHome, "sessions")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "empty.jsonl")
			if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"fresh"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			resumes := 0
			fakeHistoryServer(t, i, func(method string, params json.RawMessage) (json.RawMessage, error) {
				switch method {
				case "thread/read":
					return mustJSON(map[string]any{"thread": map[string]any{"id": "fresh", "path": path, "status": map[string]any{"type": "notLoaded"}}}), nil
				case "thread/items/list", "thread/queue/list":
					return json.RawMessage(`{"data":[]}`), nil
				case "thread/resume":
					resumes++
					var p map[string]any
					_ = json.Unmarshal(params, &p)
					if len(p) != 2 || p["threadId"] != "fresh" || p["excludeTurns"] != true {
						t.Errorf("unexpected resume overrides %s", params)
					}
					if reject {
						return nil, errors.New(`App Server: {"code":-32603,"message":"resume denied"}`)
					}
					return json.RawMessage(startedSettings), nil
				default:
					return nil, errors.New("unexpected method")
				}
			})
			if _, err := i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"fresh"}`)); err != nil {
				t.Fatal(err)
			}
			if resumes != 0 {
				t.Fatal("read resumed thread")
			}
			if err := i.reserveTurn(json.RawMessage(`{"threadId":"fresh"}`)); err != nil {
				t.Fatal(err)
			}
			err := i.manager.resumeEmptyThreadForTurn(i, "fresh")
			if resumes != 1 {
				t.Fatalf("resume calls=%d", resumes)
			}
			if reject {
				if err == nil || !strings.Contains(err.Error(), "resume denied") {
					t.Fatalf("provider failure lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
