package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/store"
	"github.com/hurf/work-anywhere/desktop/internal/traffic"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func testInstance(t *testing.T) (*Instance, *bufferWriteCloser, func()) {
	t.Helper()
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "state.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: state, traffic: traffic.New(filepath.Join(dir, "traffic")), subscribers: make(map[uint64]chan protocol.Event)}
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

	instance.observeThreadSettingsNotification(json.RawMessage(`{"threadId":"thread-1","threadSettings":{"permissions":":read-only","approvalPolicy":"on-request","approvalsReviewer":"user","collaborationMode":{"mode":"default","settings":{"model":"gpt-next","reasoning_effort":"medium","developer_instructions":"new secret"}}}}`))
	updated, ok := instance.collaborationSettings("thread-1")
	if !ok || updated.Mode != "default" || updated.Model != "gpt-next" || updated.PermissionModeID != "readOnly" || updated.TurnID != "turn-1" || !updated.Succeeded {
		t.Fatalf("settings notification did not preserve authoritative turn status: %#v", updated)
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
