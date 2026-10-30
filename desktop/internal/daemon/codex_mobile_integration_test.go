package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

// Opt-in: consumes two short model responses; all session files use a temporary CODEX_HOME.
func TestCodexMobileLifecycleIntegration(t *testing.T) {
	runtime := os.Getenv("WORK_ANYWHERE_TEST_CODEX")
	authSource := os.Getenv("WORK_ANYWHERE_TEST_AUTH")
	if runtime == "" || authSource == "" {
		t.Skip("set WORK_ANYWHERE_TEST_CODEX and WORK_ANYWHERE_TEST_AUTH for isolated live verification")
	}
	i, _, cleanup := testInstance(t)
	defer cleanup()
	root := t.TempDir()
	home := filepath.Join(root, "codex-home")
	project := filepath.Join(root, "project")
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := os.ReadFile(authSource)
	if err != nil {
		t.Fatal("cannot read authentication file")
	}
	if err = os.WriteFile(filepath.Join(home, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte("approval_policy = \"never\"\nsandbox_mode = \"read-only\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	group, err := platformprocess.NewGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	ctx, cancel := context.WithCancel(context.Background())
	i.manager.ctx = ctx
	defer cancel()
	i.manager.job = group
	i.descriptor.ID = "mobile-integration-" + time.Now().Format("150405.000000000")
	i.descriptor.CodexHome = home
	i.runtime.RealExecutable = runtime
	i.args = []string{"app-server"}
	i.cwd = project
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "CODEX_HOME=") {
			i.environment = append(i.environment, entry)
		}
	}
	i.environment = append(i.environment, "CODEX_HOME="+home)
	i.stdin = nil
	i.state = "stopped"
	i.manager.instances = map[string]*Instance{i.descriptor.ID: i}
	if err = i.Start(); err != nil {
		t.Fatal(err)
	}
	defer i.Stop(true)
	init := json.RawMessage(`{"clientInfo":{"name":"waw-mobile-integration","version":"0.1"},"capabilities":{"experimentalApi":true}}`)
	desktop := compatDesktop(i, "live-desktop")
	if err = i.HandleDesktop(desktop, mustJSON(map[string]any{"id": "live-init", "method": "initialize", "params": init})); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-desktop.outbound:
		var response struct {
			ID    string
			Error json.RawMessage
		}
		if json.Unmarshal(frame.Payload, &response) != nil || response.ID != "live-init" || len(response.Error) > 0 {
			t.Fatalf("Desktop initialize failed: %s", frame.Payload)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Desktop initialize timed out")
	}
	if i.Snapshot().State != "ready" {
		t.Fatal("upstream handshake not completed")
	}
	// Drain notifications like the Desktop transport, without sending initialized.
	go func() {
		for {
			select {
			case <-desktop.outbound:
			case <-i.manager.ctx.Done():
				return
			}
		}
	}()
	call := func(method string, params map[string]any) protocol.ControlResponse {
		params["agentId"] = i.descriptor.ID
		raw, _ := json.Marshal(params)
		return i.manager.control(protocol.ControlRequest{ID: "integration", Method: method, Contract: protocol.MobileControlContract, Params: raw})
	}
	projectParams, _ := json.Marshal(map[string]any{"name": "Mobile integration", "idempotencyKey": i.descriptor.ID, "roots": []map[string]any{{"path": project}}})
	projectRaw, err := i.Call(context.Background(), "project/create", projectParams)
	if err != nil {
		t.Fatal(err)
	}
	var projectResult struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if json.Unmarshal(projectRaw, &projectResult) != nil || projectResult.Project.ID == "" {
		t.Fatal("project/create response has no id")
	}
	created := call("conversation.create", map[string]any{"projectId": projectResult.Project.ID, "sandbox": "read-only", "approvalPolicy": "never"})
	if created.Error != nil {
		t.Fatal(created.Error)
	}
	if created.Result.(map[string]any)["projectId"] != projectResult.Project.ID {
		t.Fatalf("new thread was not assigned to project %q: %#v", projectResult.Project.ID, created.Result)
	}
	tid := created.Result.(map[string]any)["id"].(string)
	read := func() mobileConversationSnapshot {
		t.Helper()
		for n := 0; n < 10; n++ {
			r := call("conversation.read", map[string]any{"threadId": tid})
			if r.Error == nil {
				return r.Result.(mobileConversationSnapshot)
			}
			if r.Error.Code != "snapshot_unstable" {
				t.Fatal(r.Error)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("snapshot never stabilized")
		return mobileConversationSnapshot{}
	}
	if snapshot := read(); len(snapshot.Timeline.Items) != 0 || snapshot.Conversation.ProjectID != projectResult.Project.ID {
		t.Fatalf("new thread has unexpected project or timeline: %#v", snapshot)
	}
	models, err := i.manager.mobileCatalogModels(i)
	if err != nil {
		t.Fatal(err)
	}
	var selected mobileConversationSettings
	for _, model := range models.Items {
		if model.Default && model.Selectable {
			selected = mobileConversationSettings{Mode: "default", ModelID: model.ID, ReasoningEffort: model.DefaultReasoningEffort, PermissionModeID: "readOnly"}
			break
		}
	}
	if selected.ModelID == "" {
		t.Fatal("catalog has no selectable default model")
	}
	if snapshot := read(); snapshot.Settings == nil || snapshot.Settings.ModelID == "" {
		t.Fatal("thread/start settings were not hydrated")
	}
	// Exercise a persisted metadata-only thread, as left by a rejected first send.
	if err = i.Restart(); err != nil {
		t.Fatal(err)
	}
	readyDeadline := time.Now().Add(15 * time.Second)
	for i.Snapshot().State != "ready" && time.Now().Before(readyDeadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if i.Snapshot().State != "ready" {
		t.Fatal("empty thread restart did not initialize")
	}
	for turn := 0; turn < 2; turn++ {
		snapshot := read()
		i.mu.Lock()
		t.Logf("turn %d status=%s emptyResume=%v generation=%d", turn, snapshot.Conversation.Status, i.emptyThreadsToResume[tid], i.generation)
		i.mu.Unlock()
		started := call("turn.start", map[string]any{"threadId": tid, "text": "Reply only with WAW_SMOKE_OK. Do not use tools.", "settings": selected, "expectedRevision": snapshot.Revision})
		if started.Error != nil {
			t.Fatal(started.Error)
		}
		deadline := time.Now().Add(90 * time.Second)
		for !i.CanStartTurn(tid) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if !i.CanStartTurn(tid) {
			t.Fatal("turn did not finish")
		}
		snapshot = read()
		found := false
		for _, item := range snapshot.Timeline.Items {
			for _, block := range item.Blocks {
				if strings.Contains(block.Text, "WAW_SMOKE_OK") && item.Kind == "agentMessage" {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("completed turn has no expected assistant output")
		}
	}
	i.mu.Lock()
	deltas := 0
	for _, event := range i.conversations[tid].Events {
		if event.Event == "provider.item/agentMessage/delta" {
			deltas++
		}
	}
	i.mu.Unlock()
	if deltas == 0 {
		t.Fatal("no streaming delta observed")
	}
	// Control reconnect uses a fresh subscription and an authoritative read, without replaying writes.
	subscription := call("conversation.subscribe", map[string]any{"threadId": tid, "cursor": 0})
	if subscription.Error != nil {
		t.Fatal(subscription.Error)
	}
	if snapshot := read(); len(snapshot.Timeline.Items) == 0 {
		t.Fatal("reconnect lost timeline")
	}
	// Restart the isolated App Server and read the persisted thread again.
	if err = i.Restart(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for i.Snapshot().State != "ready" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if i.Snapshot().State != "ready" {
		t.Fatal("isolated App Server did not reinitialize")
	}
	if snapshot := read(); len(snapshot.Timeline.Items) == 0 {
		t.Fatal("process restart lost history")
	}
	for _, method := range []string{"thread/read", "thread/resume"} {
		params := map[string]any{"threadId": tid}
		if method == "thread/read" {
			params["includeTurns"] = true
		}
		result, err := i.Call(context.Background(), method, mustJSON(params))
		if err != nil {
			t.Fatalf("Desktop history path %s: %v", method, err)
		}
		var decoded struct {
			Thread struct {
				Turns []json.RawMessage `json:"turns"`
			} `json:"thread"`
		}
		if json.Unmarshal(result, &decoded) != nil || len(decoded.Thread.Turns) < 2 {
			t.Fatalf("Desktop %s lost turns", method)
		}
	}
	t.Log("isolated create, empty snapshot, two turns, streaming, reconnect and process restart verified")
}
