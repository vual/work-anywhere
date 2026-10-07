package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/config"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/traffic"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

type synchronizedWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *synchronizedWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}

func (*synchronizedWriteCloser) Close() error { return nil }

func (w *synchronizedWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

func testInstance(t *testing.T) (*Instance, *bufferWriteCloser, func()) {
	t.Helper()
	dir := t.TempDir()
	state, err := config.Open(dir + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{settings: state, syncEpoch: "test-epoch", traffic: traffic.New(dir + "/traffic"), subscribers: make(map[uint64]chan protocol.Event)}
	instance := NewInstance(manager, protocol.Hello{Instance: protocol.InstanceDescriptor{ID: "test-instance"}})
	writer := &bufferWriteCloser{}
	instance.stdin = writer
	instance.state = "waiting_initialize"
	return instance, writer, func() { state.Close() }
}

func TestCollaborationSettingsPersistWithoutDeveloperInstructions(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	params := json.RawMessage(`{"threadId":"thread-1","permissions":":workspace","approvalPolicy":"on-request","approvalsReviewer":"auto_review","collaborationMode":{"mode":"plan","settings":{"model":"gpt-test","reasoning_effort":"high","developer_instructions":"do not persist this secret"}}}`)
	instance.observeOutgoingThreadSettings("turn/start", params)
	instance.observeNotification("turn/started", json.RawMessage(`{"thread":{"id":"thread-1"},"turn":{"id":"turn-1"}}`))
	instance.observeNotification("turn/completed", json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1"}}`))
	instance.manager.configWrites.Wait()

	instance.mu.Lock()
	instance.threadSettings = make(map[string]threadCollaborationSettings)
	instance.mu.Unlock()
	settings, ok := instance.collaborationSettings("thread-1")
	if !ok || settings.Mode != "plan" || settings.Model != "gpt-test" || settings.ReasoningEffort != "high" || settings.PermissionModeID != "autoReview" || settings.PermissionProfile != ":workspace" || settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "auto_review" || settings.TurnID != "turn-1" || !settings.Succeeded {
		t.Fatalf("unexpected restored collaboration settings: %#v, ok=%v", settings, ok)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("developer")) || bytes.Contains(encoded, []byte("secret")) {
		t.Fatalf("developer instructions leaked into the persisted projection: %s", encoded)
	}

	instance.observeThreadSettingsNotification(json.RawMessage(`{"threadId":"thread-1","threadSettings":{"permissions":":read-only","approvalPolicy":"on-request","approvalsReviewer":"user","collaborationMode":{"mode":"default","settings":{"model":"gpt-stale","reasoning_effort":"low","developer_instructions":"stale secret"}}}}`))
	instance.observeOutgoingThreadSettings("thread/settings/update", json.RawMessage(`{"threadId":"thread-1","permissions":":read-only","approvalPolicy":"on-request","approvalsReviewer":"user","collaborationMode":{"mode":"default","settings":{"model":"gpt-next","reasoning_effort":"medium","developer_instructions":"new secret"}}}`))
	instance.manager.configWrites.Wait()
	instance.mu.Lock()
	instance.threadSettings = make(map[string]threadCollaborationSettings)
	instance.mu.Unlock()
	updated, ok := instance.collaborationSettings("thread-1")
	if !ok || updated.Mode != "default" || updated.Model != "gpt-next" || updated.PermissionModeID != "readOnly" || updated.TurnID != "turn-1" || !updated.Succeeded {
		t.Fatalf("latest settings update did not win persistence ordering: %#v", updated)
	}
}

func TestDesktopRequestIDIsRestoredAndInitializeIsVirtualized(t *testing.T) {
	instance, writer, cleanup := testInstance(t)
	defer cleanup()
	serverSide, readerSide := net.Pipe()
	defer serverSide.Close()
	defer readerSide.Close()
	desktop := &clientConn{id: "desktop-a", conn: serverSide}
	instance.AddDesktop(desktop)
	initialize := []byte(`{"jsonrpc":"2.0","id":"original-string-id","method":"initialize","params":{"capabilities":{"x":true}}}` + "\n")
	if err := instance.HandleDesktop(desktop, initialize); err != nil {
		t.Fatal(err)
	}
	forwarded := bytes.TrimSpace(writer.Bytes())
	var request map[string]json.RawMessage
	if err := json.Unmarshal(forwarded, &request); err != nil {
		t.Fatal(err)
	}
	if string(request["id"]) == `"original-string-id"` {
		t.Fatal("desktop id was not rewritten")
	}
	response := append([]byte(`{"jsonrpc":"2.0","id":`), request["id"]...)
	response = append(response, []byte(`,"result":{"server":"fake"}}`+"\n")...)
	done := make(chan struct{})
	go func() { instance.handleServerFrame(0, response); close(done) }()
	frame, err := ipc.ReadFrame(readerSide)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	var restored map[string]json.RawMessage
	if err = json.Unmarshal(bytes.TrimSpace(frame.Payload), &restored); err != nil {
		t.Fatal(err)
	}
	if string(restored["id"]) != `"original-string-id"` {
		t.Fatalf("id not restored: %s", restored["id"])
	}
	before := writer.Len()
	secondServer, secondReader := net.Pipe()
	defer secondServer.Close()
	defer secondReader.Close()
	second := &clientConn{id: "desktop-b", conn: secondServer}
	go func() {
		_ = instance.HandleDesktop(second, []byte(`{"jsonrpc":"2.0","id":27,"method":"initialize","params":{"capabilities":{"x":true}}}`+"\n"))
	}()
	virtual, err := ipc.ReadFrame(secondReader)
	if err != nil {
		t.Fatal(err)
	}
	var virtualMessage map[string]json.RawMessage
	if err = json.Unmarshal(bytes.TrimSpace(virtual.Payload), &virtualMessage); err != nil {
		t.Fatal(err)
	}
	if string(virtualMessage["id"]) != "27" {
		t.Fatalf("numeric id not restored: %s", virtualMessage["id"])
	}
	if writer.Len() != before {
		t.Fatal("virtualized initialize was forwarded to App Server")
	}
}

func TestApprovalFirstResponseWins(t *testing.T) {
	instance, writer, cleanup := testInstance(t)
	defer cleanup()
	aServer, aReader := net.Pipe()
	defer aServer.Close()
	defer aReader.Close()
	bServer, bReader := net.Pipe()
	defer bServer.Close()
	defer bReader.Close()
	a := &clientConn{id: "a", conn: aServer}
	b := &clientConn{id: "b", conn: bServer}
	instance.AddDesktop(a)
	instance.AddDesktop(b)
	request := []byte(`{"jsonrpc":"2.0","id":42,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1"}}` + "\n")
	done := make(chan struct{})
	go func() { instance.handleServerFrame(0, request); close(done) }()
	if _, err := ipc.ReadFrame(aReader); err != nil {
		t.Fatal(err)
	}
	if _, err := ipc.ReadFrame(bReader); err != nil {
		t.Fatal(err)
	}
	<-done
	writer.Reset()
	if err := instance.HandleDesktop(a, []byte(`{"jsonrpc":"2.0","id":42,"result":{"decision":"accept"}}`+"\n")); err != nil {
		t.Fatal(err)
	}
	first := writer.String()
	if err := instance.HandleDesktop(b, []byte(`{"jsonrpc":"2.0","id":42,"result":{"decision":"decline"}}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if writer.String() != first {
		t.Fatalf("second approval was forwarded: %s", writer.String())
	}
	if !bytes.Contains([]byte(first), []byte("accept")) {
		t.Fatalf("first approval missing: %s", first)
	}
}

var _ io.WriteCloser = (*bufferWriteCloser)(nil)

func TestCanStartTurnOnlyBlocksTheAffectedConversation(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.state = "ready"
	instance.activeTurns["thread-busy"] = "turn-1"
	instance.actions["action-other"] = &pendingAction{
		ID:             json.RawMessage(`"action-other"`),
		ConversationID: "thread-action",
	}
	instance.pendingTurnSettings["thread-pending"] = threadCollaborationSettings{
		Mode: "default", Model: "gpt-test",
	}

	if instance.CanStartTurn("thread-busy") {
		t.Fatal("active conversation was allowed to start another turn")
	}
	if instance.CanStartTurn("thread-action") {
		t.Fatal("conversation waiting for an action was allowed to start")
	}
	if instance.CanStartTurn("thread-pending") {
		t.Fatal("conversation with a pending turn submission was allowed to start")
	}
	if !instance.CanStartTurn("thread-idle") {
		t.Fatal("unrelated active conversation blocked an idle conversation")
	}
}

func TestAttestationRequestIsTransparentAndDoesNotBlockControl(t *testing.T) {
	instance, writer, cleanup := testInstance(t)
	defer cleanup()
	instance.state = "ready"
	serverSide, readerSide := net.Pipe()
	defer serverSide.Close()
	defer readerSide.Close()
	desktop := &clientConn{id: "desktop-attestation", conn: serverSide}
	instance.AddDesktop(desktop)

	request := []byte(`{"jsonrpc":"2.0","id":73,"method":"attestation/generate","params":{}}` + "\n")
	done := make(chan struct{})
	go func() { instance.handleServerFrame(0, request); close(done) }()
	frame, err := ipc.ReadFrame(readerSide)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if !bytes.Contains(frame.Payload, []byte(`"attestation/generate"`)) {
		t.Fatalf("attestation request was not forwarded to Desktop: %s", frame.Payload)
	}
	if actions := instance.Actions(); len(actions) != 0 {
		t.Fatalf("attestation escaped into user actions: %#v", actions)
	}
	if snapshot := instance.Snapshot(); snapshot.PendingActions != 0 || snapshot.State != "ready" {
		t.Fatalf("attestation changed user-facing activity: %+v", snapshot)
	}
	if !instance.CanStartTurn("thread-new") || !instance.CanSubmitQueue("thread-new") || !instance.CanUpdateSettings("thread-new") {
		t.Fatal("attestation blocked an unrelated conversation write")
	}
	if err := instance.ResolveAction(json.RawMessage(`73`), json.RawMessage(`{"decision":"accept"}`)); err == nil {
		t.Fatal("control resolved attestation as a user action")
	}

	writer.Reset()
	if err := instance.HandleDesktop(desktop, []byte(`{"jsonrpc":"2.0","id":73,"result":{"token":"opaque-token"}}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(writer.Bytes(), []byte(`"token":"opaque-token"`)) {
		t.Fatalf("Desktop attestation response was not returned to App Server: %s", writer.String())
	}
	instance.mu.Lock()
	pending := len(instance.actions)
	instance.mu.Unlock()
	if pending != 0 {
		t.Fatalf("resolved attestation remained pending: %d", pending)
	}
}

func TestActionsAreOldestFirstWithStableIDTieBreak(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	created := time.Unix(100, 0)
	instance.actions["later"] = &pendingAction{ID: json.RawMessage(`"later"`), Method: "requestApproval", CreatedAt: created.Add(time.Second)}
	instance.actions["same-b"] = &pendingAction{ID: json.RawMessage(`"same-b"`), Method: "requestApproval", CreatedAt: created}
	instance.actions["same-a"] = &pendingAction{ID: json.RawMessage(`"same-a"`), Method: "requestApproval", CreatedAt: created}
	instance.actions["attestation"] = &pendingAction{ID: json.RawMessage(`"infra"`), Method: "attestation/generate", CreatedAt: created.Add(-time.Second)}

	actions := instance.Actions()
	if len(actions) != 3 || string(actions[0].ID) != `"same-a"` || string(actions[1].ID) != `"same-b"` || string(actions[2].ID) != `"later"` {
		t.Fatalf("actions were not stable oldest-first user actions: %#v", actions)
	}
}

func TestCanSubmitQueueAllowsActiveConversationWithoutBlockingAction(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.state = "ready"
	instance.activeTurns["thread-running"] = "turn-1"

	if !instance.CanSubmitQueue("thread-running") {
		t.Fatal("active conversation without an action could not accept queue input")
	}
	instance.actions["action-running"] = &pendingAction{
		ID:             json.RawMessage(`"action-running"`),
		ConversationID: "thread-running",
	}
	if instance.CanSubmitQueue("thread-running") {
		t.Fatal("conversation waiting for an action accepted queue input")
	}
	if !instance.CanSubmitQueue("thread-other") {
		t.Fatal("an action for another conversation blocked queue input")
	}
	instance.actions["action-unknown"] = &pendingAction{ID: json.RawMessage(`"action-unknown"`)}
	if instance.CanSubmitQueue("thread-other") {
		t.Fatal("an unowned blocking action allowed queue input")
	}
}

func TestCompletingOneTurnPreservesOtherConversationActivity(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	instance.state = "ready"
	instance.observeNotification("turn/started", json.RawMessage(`{"threadId":"thread-a","turn":{"id":"turn-a"}}`))
	instance.observeNotification("turn/started", json.RawMessage(`{"threadId":"thread-b","turn":{"id":"turn-b"}}`))
	instance.actions[`"action-b"`] = &pendingAction{
		ID:             json.RawMessage(`"action-b"`),
		ConversationID: "thread-b",
		TurnID:         "turn-b",
		CreatedAt:      time.Now(),
	}

	instance.observeNotification("turn/completed", json.RawMessage(`{"threadId":"thread-a","turn":{"id":"turn-a","status":"completed"}}`))

	snapshot := instance.Snapshot()
	if !snapshot.ActiveTurn || snapshot.PendingActions != 1 {
		t.Fatalf("other conversation activity was lost: %+v", snapshot)
	}
	if instance.CanStartTurn("thread-b") {
		t.Fatal("still-active conversation was allowed to start")
	}
	if !instance.CanStartTurn("thread-a") {
		t.Fatal("completed conversation did not become idle")
	}
}

func TestConcurrentServerWritesRemainWholeJSONLines(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	writer := &synchronizedWriteCloser{}
	instance.stdin = writer
	const count = 200
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": index, "method": "test"})
			if err := instance.writeServer(payload); err != nil {
				t.Errorf("writeServer: %v", err)
			}
		}(index)
	}
	group.Wait()
	lines := bytes.Split(bytes.TrimSpace(writer.Bytes()), []byte("\n"))
	if len(lines) != count {
		t.Fatalf("got %d JSONL lines, want %d", len(lines), count)
	}
	for index, line := range lines {
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", index, err)
		}
	}
}

func TestInitializeCapabilitiesIgnoreClientInfoChanges(t *testing.T) {
	first := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"desktop","version":"1"},"capabilities":{"experimentalApi":true}}}`)
	second := []byte(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"clientInfo":{"name":"desktop","version":"2"},"capabilities":{"experimentalApi":true}}}`)
	if !sameInitializeCapabilities(first, second) {
		t.Fatal("clientInfo changes must not require an App Server restart")
	}
	nullCapabilities := []byte(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"clientInfo":{"name":"desktop","version":"3"},"capabilities":null}}`)
	emptyCapabilities := []byte(`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"clientInfo":{"name":"desktop","version":"4"},"capabilities":{}}}`)
	if !sameInitializeCapabilities(nullCapabilities, emptyCapabilities) {
		t.Fatal("null and empty capabilities must be equivalent")
	}
	changed := []byte(`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"clientInfo":{"name":"desktop","version":"5"},"capabilities":{"experimentalApi":false}}}`)
	if sameInitializeCapabilities(first, changed) {
		t.Fatal("capability changes must require an App Server restart")
	}
}

func TestCallTimeoutRemovesRequest(t *testing.T) {
	instance, _, cleanup := testInstance(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := instance.Call(ctx, "test/request", json.RawMessage(`{"value":true}`)); err == nil {
		t.Fatal("Call unexpectedly succeeded without a response")
	}
	instance.mu.Lock()
	defer instance.mu.Unlock()
	if len(instance.requests) != 0 {
		t.Fatalf("timed out request remained routed: %#v", instance.requests)
	}
}
