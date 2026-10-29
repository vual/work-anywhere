package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/jsonrpc"
)

func compatDesktop(i *Instance, id string) *clientConn {
	c := &clientConn{id: id, outbound: make(chan ipc.Frame, 128), queueCap: 8 << 20}
	i.AddDesktop(c)
	return c
}

func compatResponse(t *testing.T, c *clientConn) jsonrpc.Message {
	t.Helper()
	select {
	case frame := <-c.outbound:
		m, err := jsonrpc.Parse(frame.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("no Desktop response")
		return jsonrpc.Message{}
	}
}

func TestInitializeNotificationOncePerGeneration(t *testing.T) {
	i, w, cleanup := testInstance(t)
	defer cleanup()
	a, b := compatDesktop(i, "a"), compatDesktop(i, "b")
	init := []byte(`{"id":1,"method":"initialize","trace":{"future":true},"params":{"capabilities":{"experimentalApi":true,"extensions":{"future":{}}}}}`)
	if err := i.HandleDesktop(a, init); err != nil {
		t.Fatal(err)
	}
	request, _ := jsonrpc.Parse(w.Bytes())
	if !bytes.Contains(w.Bytes(), []byte(`"trace"`)) {
		t.Fatal("lost trace")
	}
	if err := i.HandleDesktop(b, bytes.Replace(init, []byte(`"id":1`), []byte(`"id":"b"`), 1)); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	i.handleServerFrame(0, []byte(fmt.Sprintf(`{"id":%s,"result":{"userAgent":"test","future":true}}`, request.ID)))
	_ = compatResponse(t, a)
	_ = compatResponse(t, b)
	if i.state != "waiting_initialize" || w.Len() != 0 {
		t.Fatal("ready before initialized")
	}
	for _, c := range []*clientConn{a, b, a} {
		if err := i.HandleDesktop(c, []byte(`{"method":"initialized"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(w.String(), "initialized") != 1 || i.state != "ready" {
		t.Fatalf("handshake: %s %s", w.String(), i.state)
	}
	i.generation++
	i.initializedSent = false
	i.cachedInitializeResponse = nil
	w.Reset()
	i.completeInitialize(i.generation, []byte(`{"id":1,"result":{}}`), true)
	if strings.Count(w.String(), "initialized") != 1 || i.state != "ready" {
		t.Fatal("recovery did not initialize")
	}
}

func TestInitializeErrorNotCachedAndConflictsRejected(t *testing.T) {
	i, w, cleanup := testInstance(t)
	defer cleanup()
	a, b := compatDesktop(i, "a"), compatDesktop(i, "b")
	init := []byte(`{"id":1,"method":"initialize","params":{"capabilities":{"experimentalApi":true}}}`)
	_ = i.HandleDesktop(a, init)
	req, _ := jsonrpc.Parse(w.Bytes())
	w.Reset()
	_ = i.HandleDesktop(b, []byte(`{"id":2,"method":"initialize","params":{"capabilities":{"experimentalApi":false}}}`))
	if len(compatResponse(t, b).Error) == 0 || w.Len() != 0 {
		t.Fatal("merged incompatible initialize")
	}
	_ = i.HandleDesktop(b, bytes.Replace(init, []byte(`"id":1`), []byte(`"id":3`), 1))
	i.handleServerFrame(0, []byte(fmt.Sprintf(`{"id":%s,"error":{"code":-32000,"message":"denied","data":{"future":true}}}`, req.ID)))
	for _, c := range []*clientConn{a, b} {
		if !bytes.Contains(compatResponse(t, c).Error, []byte("future")) {
			t.Fatal("lost initialize error")
		}
	}
	if i.state != "error" || len(i.cachedInitializeResponse) != 0 || len(i.cachedInitializeRequest) != 0 || i.initializeInFlight {
		t.Fatal("cached initialize failure")
	}
	_ = i.HandleDesktop(a, init)
	if w.Len() == 0 {
		t.Fatal("retry not forwarded")
	}
	w.Reset()
	i.completeInitialize(0, []byte(`{"id":1,"error":{"code":-1,"message":"recovery denied"}}`), true)
	if w.Len() != 0 || i.state != "error" {
		t.Fatal("automatic initialization ignored error")
	}
}

func TestInitializeBusyCapabilityChangeDoesNotReturnOldSuccess(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	c := compatDesktop(i, "a")
	i.cachedInitializeRequest = []byte(`{"params":{"capabilities":{"x":true}}}`)
	i.cachedInitializeResponse = []byte(`{"id":0,"result":{}}`)
	i.activeTurns["thread"] = "turn"
	i.state = "active"
	_ = i.HandleDesktop(c, []byte(`{"id":1,"method":"initialize","params":{"capabilities":{"x":false}}}`))
	if len(compatResponse(t, c).Error) == 0 || len(i.pendingInitialize) == 0 || i.state != "upgrade_pending" {
		t.Fatal("claimed incompatible initialization succeeded")
	}
}

func TestInfrastructureRequests(t *testing.T) {
	for _, method := range []string{"attestation/generate", "account/chatgptAuthTokens/refresh", "item/tool/call"} {
		t.Run(method, func(t *testing.T) {
			i, w, cleanup := testInstance(t)
			defer cleanup()
			i.state = "ready"
			c := compatDesktop(i, "a")
			raw := mustJSON(map[string]any{"id": 9, "method": method, "params": map[string]any{"threadId": "thread"}})
			i.handleServerFrame(0, raw)
			if compatResponse(t, c).Method != method {
				t.Fatal("not forwarded")
			}
			if i.Snapshot().PendingActions != 0 || !i.CanStartTurn("other") {
				t.Fatal("infrastructure blocked user action gate")
			}
			if i.ResolveAction(json.RawMessage(`9`), json.RawMessage(`{}`)) == nil {
				t.Fatal("control answered infrastructure")
			}
			late := compatDesktop(i, "late")
			if i.HandleDesktop(late, []byte(`{"id":9,"result":{}}`)) == nil {
				t.Fatal("non-recipient answered")
			}
			for range 2 {
				if err := i.HandleDesktop(c, []byte(`{"id":9,"result":{"opaque":"unchanged"}}`)); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Count(w.String(), "opaque") != 1 {
				t.Fatal("duplicate response")
			}
			w.Reset()
			i.handleServerFrame(0, raw)
			_ = compatResponse(t, c)
			_ = compatResponse(t, late)
			i.RemoveDesktop(c.id)
			i.RemoveDesktop(late.id)
			if !strings.Contains(w.String(), "desktop_unavailable") || len(i.actions) != 0 {
				t.Fatal("disconnected request leaked")
			}
			w.Reset()
			i.handleServerFrame(0, raw)
			if !strings.Contains(w.String(), "desktop_unavailable") || len(i.actions) != 0 {
				t.Fatal("missing Desktop not reported")
			}
		})
	}
}

func TestCurrentTimeReadIsLocal(t *testing.T) {
	i, w, cleanup := testInstance(t)
	defer cleanup()
	i.state = "ready"
	c := compatDesktop(i, "a")
	i.handleServerFrame(0, []byte(`{"id":"clock","method":"currentTime/read","params":{"threadId":"thread"}}`))
	m, err := jsonrpc.Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ CurrentTimeAt int64 }
	_ = json.Unmarshal(m.Result, &result)
	if string(m.ID) != `"clock"` || result.CurrentTimeAt < time.Now().Unix()-2 || result.CurrentTimeAt > time.Now().Unix()+2 || len(c.outbound) != 0 || len(i.actions) != 0 || !i.CanStartTurn("thread") {
		t.Fatalf("clock contract: %s", w.Bytes())
	}
}

func TestInitializeSurvivesOriginDisconnect(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			i, w, cleanup := testInstance(t)
			defer cleanup()
			a, b := compatDesktop(i, "a"), compatDesktop(i, "b")
			init := []byte(`{"id":1,"method":"initialize","params":{"capabilities":{"future":true}}}`)
			_ = i.HandleDesktop(a, init)
			req, _ := jsonrpc.Parse(w.Bytes())
			_ = i.HandleDesktop(b, init)
			i.RemoveDesktop(a.id)
			w.Reset()
			payload := `"result":{"userAgent":"test"}`
			if fail {
				payload = `"error":{"code":-32000,"message":"denied"}`
			}
			i.handleServerFrame(0, []byte(fmt.Sprintf(`{"id":%s,%s}`, req.ID, payload)))
			response := compatResponse(t, b)
			if i.initializeInFlight || (len(response.Error) > 0) != fail {
				t.Fatalf("handshake lost after disconnect: %+v", response)
			}
			if !fail && (i.state != "ready" || strings.Count(w.String(), "initialized") != 1) {
				t.Fatal("orphaned successful initialization did not finish")
			}
			if fail && (i.state != "error" || len(i.cachedInitializeResponse) != 0 || w.Len() != 0) {
				t.Fatal("orphaned error treated as success")
			}
		})
	}
}

func TestGenerationBoundResponseCannotReachNewProcess(t *testing.T) {
	i, w, cleanup := testInstance(t)
	defer cleanup()
	old := i.generation
	i.generation++
	if err := i.writeServerForGeneration([]byte(`{"id":1,"result":{}}`), &old); err == nil || w.Len() != 0 {
		t.Fatal("old response reached new process")
	}
	i.handleServerFrame(old, []byte(`{"id":1,"method":"currentTime/read"}`))
	if w.Len() != 0 {
		t.Fatal("stale server request reached new process")
	}
}

func TestSnapshotRestoresProjectFromProviderWorkspace(t *testing.T) {
	for _, workspace := range []string{`"cwd":"C:/project"`, `"environments":[{"runtimeWorkspaceRoots":["C:/project"]}]`} {
		i, _, cleanup := testInstance(t)
		fakeHistoryServer(t, i, func(method string, _ json.RawMessage) (json.RawMessage, error) {
			switch method {
			case "thread/read":
				return json.RawMessage(`{"thread":{"id":"t","projectId":null,"status":{"type":"idle"},` + workspace + `}}`), nil
			case "project/list":
				return json.RawMessage(`{"data":[{"id":"project","roots":[{"path":"C:/project"}]}]}`), nil
			default:
				return json.RawMessage(`{"data":[]}`), nil
			}
		})
		snapshot, err := i.manager.conversationSnapshot(i, i.descriptor.ID, json.RawMessage(`{"threadId":"t"}`))
		if err != nil || snapshot.Conversation.ProjectID != "project" || snapshot.Conversation.Status != "idle" || snapshot.Conversation.Running {
			t.Fatalf("workspace projection: %+v, %v", snapshot.Conversation, err)
		}
		cleanup()
	}
}

func TestNewTimelineShapesDoNotEndTurn(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	i.state = "active"
	i.activeTurns["thread"] = "turn"
	for _, item := range []string{`{"id":"text","type":"agentMessage","phase":"partial_answer","text":"more follows"}`, `{"id":"image","type":"imageGeneration","status":"completed","inputImages":[],"savedPath":"private.png"}`, `{"id":"sub","type":"subAgentActivity","kind":"completed","agentPath":"a","agentThreadId":"b","model":"future","reasoningEffort":"high"}`} {
		page, _, err := normalizeTimelinePage(json.RawMessage(`{"data":[` + item + `]}`))
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("timeline: %v %+v", err, page)
		}
		i.handleServerFrame(0, []byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":`+item+`}}`))
	}
	i.handleServerFrame(0, []byte(`{"method":"thread/prediction/updated","params":{"threadId":"thread","future":true}}`))
	if i.activeTurns["thread"] != "turn" {
		t.Fatal("item incorrectly ended Turn")
	}
}

func TestDiagnosticExitChild(t *testing.T) {
	if os.Getenv("WAW_DIAGNOSTIC_EXIT") == "1" {
		_, _ = os.Stderr.WriteString("error: incompatible startup flags\nUsage: codex app-server\n")
		os.Exit(2)
	}
}

func TestExitRetainsStderrAndCode(t *testing.T) {
	i, _, cleanup := testInstance(t)
	defer cleanup()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDiagnosticExitChild$")
	cmd.Env = append(os.Environ(), "WAW_DIAGNOSTIC_EXIT=1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	i.cmd = cmd
	i.readServerStderr(0, stderr)
	i.waitServer(0, cmd)
	if i.lastExitCode == nil || *i.lastExitCode != 2 || !strings.Contains(i.lastError, "incompatible startup flags") {
		t.Fatalf("diagnostic lost: %+v", i.Snapshot())
	}
	before := i.stderrTail
	i.readServerStderr(99, strings.NewReader("old secret\n"))
	if i.stderrTail != before {
		t.Fatal("stale stderr changed state")
	}
}
