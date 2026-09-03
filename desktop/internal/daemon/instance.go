package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/jsonrpc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/identity"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

type requestOrigin struct {
	desktopID  string
	originalID json.RawMessage
	method     string
	internal   bool
	response   chan []byte
}

type pendingAction struct {
	ID        json.RawMessage `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	Resolved  bool            `json:"resolved"`
}

type conversationProjection struct {
	ID       string
	Status   string
	Sequence int64
	Revision int64
	Events   []protocol.Event
}

type initializeWaiter struct {
	DesktopID  string
	OriginalID json.RawMessage
}

type Instance struct {
	manager *Manager

	mu                       sync.Mutex
	descriptor               protocol.InstanceDescriptor
	runtime                  protocol.RuntimeDescriptor
	pendingRuntime           *protocol.RuntimeDescriptor
	args                     []string
	cwd                      string
	environment              []string
	state                    string
	lastError                string
	active                   bool
	activeThreadID           string
	activeTurnID             string
	desktops                 map[string]*clientConn
	requests                 map[string]requestOrigin
	actions                  map[string]*pendingAction
	conversations            map[string]*conversationProjection
	nextRequest              uint64
	eventSequence            int64
	generation               uint64
	cmd                      *exec.Cmd
	stdin                    io.WriteCloser
	lease                    *identity.Lease
	cachedInitializeRequest  []byte
	cachedInitializeResponse []byte
	cachedInitialized        []byte
	pendingInitialize        []byte
	initializeInFlight       bool
	initializeWaiters        []initializeWaiter
}

type InstanceSnapshot struct {
	ID                 string                      `json:"id"`
	Name               string                      `json:"name,omitempty"`
	CodexHome          string                      `json:"codexHome"`
	UserDataDir        string                      `json:"userDataDir"`
	State              string                      `json:"state"`
	Runtime            protocol.RuntimeDescriptor  `json:"runtime"`
	PendingRuntime     *protocol.RuntimeDescriptor `json:"pendingRuntime,omitempty"`
	DesktopConnections int                         `json:"desktopConnections"`
	ActiveTurn         bool                        `json:"activeTurn"`
	PendingActions     int                         `json:"pendingActions"`
	LastError          string                      `json:"lastError,omitempty"`
}

func NewInstance(manager *Manager, hello protocol.Hello) *Instance {
	return &Instance{
		manager: manager, descriptor: hello.Instance, runtime: hello.Runtime,
		args: append([]string(nil), hello.Args...), cwd: hello.CWD,
		environment: append([]string(nil), hello.Environment...), state: "stopped",
		desktops: make(map[string]*clientConn), requests: make(map[string]requestOrigin),
		actions:       make(map[string]*pendingAction),
		conversations: make(map[string]*conversationProjection),
	}
}

func (i *Instance) RegisterRuntime(hello protocol.Hello) error {
	i.mu.Lock()
	i.descriptor = hello.Instance
	i.args = append([]string(nil), hello.Args...)
	i.cwd = hello.CWD
	i.environment = append([]string(nil), hello.Environment...)
	started := i.cmd != nil
	changed := started && i.runtime.SHA256 != "" && hello.Runtime.SHA256 != "" && i.runtime.SHA256 != hello.Runtime.SHA256
	busy := i.active || len(i.actions) > 0
	if changed && busy {
		candidate := hello.Runtime
		i.pendingRuntime = &candidate
		i.state = "upgrade_pending"
		i.mu.Unlock()
		return nil
	}
	if changed {
		i.runtime = hello.Runtime
	}
	if !started {
		i.runtime = hello.Runtime
	}
	i.mu.Unlock()
	if !started {
		return i.Start()
	}
	if changed {
		return i.Restart()
	}
	return nil
}

func (i *Instance) AddDesktop(conn *clientConn) {
	i.mu.Lock()
	i.desktops[conn.id] = conn
	i.mu.Unlock()
}

func (i *Instance) RemoveDesktop(id string) {
	i.mu.Lock()
	delete(i.desktops, id)
	for requestID, origin := range i.requests {
		if origin.desktopID == id {
			delete(i.requests, requestID)
		}
	}
	i.mu.Unlock()
}

func (i *Instance) Start() error {
	i.mu.Lock()
	if i.cmd != nil {
		i.mu.Unlock()
		return nil
	}
	i.state, i.lastError = "starting", ""
	if i.lease == nil {
		lease, already, err := identity.AcquireLease(identity.InstanceMutexName(i.descriptor.ID))
		if err != nil {
			i.state, i.lastError = "error", err.Error()
			i.mu.Unlock()
			return err
		}
		if already {
			lease.Close()
			err = errors.New("instance has an active fallback writer")
			i.state, i.lastError = "error", err.Error()
			i.mu.Unlock()
			return err
		}
		i.lease = lease
	}
	runtime := i.runtime
	args := codex.AppServerArgs(i.args)
	cwd := i.cwd
	environment := append([]string(nil), i.environment...)
	i.generation++
	generation := i.generation
	i.mu.Unlock()

	cmd := exec.Command(runtime.RealExecutable, args...)
	cmd.Dir = cwd
	cmd.Env = environmentList(environment, runtime.RealExecutable)
	platformprocess.ConfigureManagedChild(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return i.startFailed(generation, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return i.startFailed(generation, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return i.startFailed(generation, err)
	}
	if err = cmd.Start(); err != nil {
		return i.startFailed(generation, err)
	}
	if err = i.manager.job.Add(cmd); err != nil {
		_ = cmd.Process.Kill()
		return i.startFailed(generation, err)
	}

	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		_ = cmd.Process.Kill()
		return errors.New("instance generation changed during startup")
	}
	i.cmd, i.stdin, i.state = cmd, stdin, "waiting_initialize"
	cachedInit := append([]byte(nil), i.cachedInitializeRequest...)
	i.mu.Unlock()
	go i.readServer(generation, stdout)
	go i.readServerStderr(generation, stderr)
	go i.waitServer(generation, cmd)
	if len(cachedInit) > 0 {
		if err = i.sendInternalInitialize(cachedInit); err != nil {
			return err
		}
	}
	return nil
}

func (i *Instance) startFailed(generation uint64, err error) error {
	i.mu.Lock()
	if generation == i.generation {
		i.state, i.lastError = "error", err.Error()
		if i.lease != nil {
			_ = i.lease.Close()
			i.lease = nil
		}
	}
	i.mu.Unlock()
	return err
}

func (i *Instance) Restart() error {
	i.stopProcess(false)
	return i.Start()
}

func (i *Instance) Stop(release bool) { i.stopProcess(release) }

func (i *Instance) stopProcess(release bool) {
	i.mu.Lock()
	i.generation++
	cmd := i.cmd
	i.cmd, i.stdin = nil, nil
	if release && i.lease != nil {
		_ = i.lease.Close()
		i.lease = nil
	}
	i.state = "stopped"
	i.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (i *Instance) HandleDesktop(conn *clientConn, raw []byte) error {
	i.manager.traffic.Record(i.descriptor.ID, "desktop_to_server", raw)
	message, err := jsonrpc.Parse(bytes.TrimSpace(raw))
	if err != nil {
		return i.writeServer(raw)
	}
	if message.IsResponse() {
		return i.resolveServerRequest(message, raw)
	}
	if message.IsNotification() {
		if message.Method == "initialized" {
			i.mu.Lock()
			if len(i.cachedInitialized) == 0 {
				i.cachedInitialized = append([]byte(nil), raw...)
			}
			alreadyInitialized := len(i.cachedInitializeResponse) > 0
			i.mu.Unlock()
			if alreadyInitialized {
				return nil
			}
		}
		return i.writeServer(raw)
	}
	if !message.IsRequest() {
		return i.writeServer(raw)
	}
	if message.Method == "turn/start" {
		i.broadcastEvent("frontend.turn.start", map[string]any{"params": json.RawMessage(message.Params), "source": "desktop"})
	}

	if message.Method == "initialize" {
		i.mu.Lock()
		cached := append([]byte(nil), i.cachedInitializeResponse...)
		cachedRequest := append([]byte(nil), i.cachedInitializeRequest...)
		if len(cached) == 0 && i.initializeInFlight {
			i.initializeWaiters = append(i.initializeWaiters, initializeWaiter{DesktopID: conn.id, OriginalID: append(json.RawMessage(nil), message.ID...)})
			i.mu.Unlock()
			return nil
		}
		if len(i.cachedInitializeRequest) == 0 {
			i.cachedInitializeRequest = append([]byte(nil), raw...)
		}
		if len(cached) == 0 {
			i.initializeInFlight = true
		}
		busy := i.active || len(i.actions) > 0
		i.mu.Unlock()
		if len(cached) > 0 {
			if !sameInitializeCapabilities(cachedRequest, raw) {
				if busy {
					i.mu.Lock()
					i.pendingInitialize = append([]byte(nil), raw...)
					i.state = "upgrade_pending"
					i.mu.Unlock()
				} else {
					i.mu.Lock()
					i.cachedInitializeRequest, i.cachedInitializeResponse = nil, nil
					i.initializeInFlight = false
					i.mu.Unlock()
					if restartErr := i.Restart(); restartErr != nil {
						return restartErr
					}
					cached = nil
				}
			}
		}
		if len(cached) > 0 {
			response, rewriteErr := jsonrpc.RewriteRawID(bytes.TrimSpace(cached), message.ID)
			if rewriteErr != nil {
				return rewriteErr
			}
			return conn.Write(ipc.Frame{Kind: ipc.KindServerFrame, Payload: append(response, '\n')})
		}
		i.mu.Lock()
		if len(i.cachedInitializeRequest) == 0 {
			i.cachedInitializeRequest = append([]byte(nil), raw...)
		}
		i.initializeInFlight = true
		i.mu.Unlock()
	}

	i.mu.Lock()
	i.nextRequest++
	proxyID := fmt.Sprintf("wa:d:%s:%d", conn.id, i.nextRequest)
	rewritten, err := jsonrpc.RewriteID(bytes.TrimSpace(raw), proxyID)
	if err == nil {
		i.requests[string(mustJSON(proxyID))] = requestOrigin{desktopID: conn.id, originalID: append(json.RawMessage(nil), message.ID...), method: message.Method}
	}
	i.mu.Unlock()
	if err != nil {
		return err
	}
	return i.writeServer(append(rewritten, '\n'))
}

func (i *Instance) resolveServerRequest(message jsonrpc.Message, raw []byte) error {
	key := string(message.ID)
	i.mu.Lock()
	action := i.actions[key]
	if action == nil || action.Resolved {
		i.mu.Unlock()
		return nil
	}
	action.Resolved = true
	delete(i.actions, key)
	i.mu.Unlock()
	_ = i.manager.store.ResolvePendingAction(i.descriptor.ID, key)
	if err := i.writeServer(raw); err != nil {
		return err
	}
	i.broadcastEvent("action.resolved", map[string]any{"id": json.RawMessage(message.ID)})
	return nil
}

func (i *Instance) writeServer(raw []byte) error {
	i.mu.Lock()
	stdin := i.stdin
	i.mu.Unlock()
	if stdin == nil {
		return errors.New("App Server is not running")
	}
	_, err := stdin.Write(ensureNewline(raw))
	return err
}

func (i *Instance) readServer(generation uint64, stream io.Reader) {
	reader := bufio.NewReaderSize(stream, 64*1024)
	for {
		line, err := ipc.ReadBoundedLine(reader, ipc.DefaultMaxFrame)
		if len(line) > 0 {
			i.handleServerFrame(generation, line)
		}
		if err != nil {
			return
		}
	}
}

func (i *Instance) readServerStderr(generation uint64, stream io.Reader) {
	reader := bufio.NewScanner(stream)
	for reader.Scan() {
		i.mu.Lock()
		if generation == i.generation {
			i.lastError = reader.Text()
		}
		i.mu.Unlock()
	}
}

func (i *Instance) handleServerFrame(generation uint64, raw []byte) {
	i.manager.traffic.Record(i.descriptor.ID, "server_to_frontends", raw)
	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		return
	}
	i.mu.Unlock()
	message, err := jsonrpc.Parse(bytes.TrimSpace(raw))
	if err != nil {
		i.broadcast(raw)
		return
	}
	if message.IsResponse() {
		key := string(message.ID)
		i.mu.Lock()
		origin, ok := i.requests[key]
		if ok {
			delete(i.requests, key)
		}
		i.mu.Unlock()
		if !ok {
			return
		}
		if origin.internal {
			if origin.response != nil {
				select {
				case origin.response <- append([]byte(nil), raw...):
				default:
				}
				return
			}
			i.mu.Lock()
			i.state = "ready"
			i.cachedInitializeResponse = append([]byte(nil), raw...)
			i.initializeInFlight = false
			waiters := append([]initializeWaiter(nil), i.initializeWaiters...)
			i.initializeWaiters = nil
			initialized := append([]byte(nil), i.cachedInitialized...)
			i.mu.Unlock()
			i.answerInitializeWaiters(raw, waiters)
			if len(initialized) == 0 {
				initialized = []byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}` + "\n")
			}
			_ = i.writeServer(initialized)
			return
		}
		restored, rewriteErr := jsonrpc.RewriteRawID(bytes.TrimSpace(raw), origin.originalID)
		if rewriteErr != nil {
			return
		}
		restored = append(restored, '\n')
		if origin.method == "initialize" {
			i.mu.Lock()
			i.cachedInitializeResponse = append([]byte(nil), restored...)
			i.initializeInFlight = false
			waiters := append([]initializeWaiter(nil), i.initializeWaiters...)
			i.initializeWaiters = nil
			i.state = "ready"
			i.mu.Unlock()
			i.answerInitializeWaiters(restored, waiters)
		}
		i.sendDesktop(origin.desktopID, restored)
		return
	}
	if message.IsRequest() {
		action := &pendingAction{ID: append(json.RawMessage(nil), message.ID...), Method: message.Method, Params: append(json.RawMessage(nil), message.Params...), CreatedAt: time.Now().UTC()}
		i.mu.Lock()
		i.actions[string(message.ID)] = action
		i.state = "waiting_action"
		i.mu.Unlock()
		_ = i.manager.store.SavePendingAction(i.descriptor.ID, string(message.ID), message.Method, action)
		i.broadcast(raw)
		i.broadcastEvent("action.created", action)
		return
	}
	if message.IsNotification() {
		i.observeNotification(message.Method, message.Params)
		i.broadcastEvent("provider."+message.Method, map[string]any{"method": message.Method, "params": json.RawMessage(message.Params)})
	}
	i.broadcast(raw)
}

func (i *Instance) answerInitializeWaiters(response []byte, waiters []initializeWaiter) {
	for _, waiter := range waiters {
		rewritten, err := jsonrpc.RewriteRawID(bytes.TrimSpace(response), waiter.OriginalID)
		if err == nil {
			i.sendDesktop(waiter.DesktopID, append(rewritten, '\n'))
		}
	}
}

func (i *Instance) observeNotification(method string, params json.RawMessage) {
	lower := strings.ToLower(method)
	i.mu.Lock()
	switch {
	case strings.Contains(lower, "serverrequest/resolved"):
		var resolved struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(params, &resolved)
		delete(i.actions, string(resolved.RequestID))
		_ = i.manager.store.ResolvePendingAction(i.descriptor.ID, string(resolved.RequestID))
		if i.active {
			i.state = "active"
		} else {
			i.state = "ready"
		}
	case strings.Contains(lower, "turn/started") || strings.HasSuffix(lower, "turn.started"):
		i.active, i.state = true, "active"
		var identifiers struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		_ = json.Unmarshal(params, &identifiers)
		i.activeThreadID = firstNonempty(identifiers.ThreadID, identifiers.Thread.ID)
		i.activeTurnID = firstNonempty(identifiers.TurnID, identifiers.Turn.ID)
	case strings.Contains(lower, "turn/completed") || strings.Contains(lower, "turn/failed") || strings.Contains(lower, "turn/interrupted"):
		i.active = false
		i.activeThreadID, i.activeTurnID = "", ""
		actionIDs := make([]string, 0, len(i.actions))
		for id := range i.actions {
			actionIDs = append(actionIDs, id)
		}
		i.actions = make(map[string]*pendingAction)
		i.state = "ready"
		pending := i.pendingRuntime
		pendingInitialize := append([]byte(nil), i.pendingInitialize...)
		if pending != nil {
			i.runtime = *pending
			i.pendingRuntime = nil
		}
		if len(pendingInitialize) > 0 {
			i.cachedInitializeRequest = pendingInitialize
			i.cachedInitializeResponse = nil
			i.pendingInitialize = nil
		}
		i.mu.Unlock()
		for _, id := range actionIDs {
			_ = i.manager.store.ResolvePendingAction(i.descriptor.ID, id)
		}
		if pending != nil || len(pendingInitialize) > 0 {
			go i.Restart()
		}
		return
	}
	i.mu.Unlock()
}

func (i *Instance) InterruptActive() error {
	i.mu.Lock()
	threadID, turnID, active := i.activeThreadID, i.activeTurnID, i.active
	i.mu.Unlock()
	if !active {
		return nil
	}
	params, _ := json.Marshal(map[string]string{"threadId": threadID, "turnId": turnID})
	_, err := i.Call(context.Background(), "turn/interrupt", params)
	return err
}

func (i *Instance) sendInternalInitialize(cached []byte) error {
	message, err := jsonrpc.Parse(bytes.TrimSpace(cached))
	if err != nil {
		return err
	}
	const internalID = "wa:c:daemon:initialize"
	rewritten, err := jsonrpc.RewriteID(bytes.TrimSpace(cached), internalID)
	if err != nil {
		return err
	}
	i.mu.Lock()
	i.requests[string(mustJSON(internalID))] = requestOrigin{originalID: message.ID, method: "initialize", internal: true}
	i.mu.Unlock()
	i.manager.traffic.Record(i.descriptor.ID, "daemon_to_server", rewritten)
	return i.writeServer(append(rewritten, '\n'))
}

func (i *Instance) waitServer(generation uint64, cmd *exec.Cmd) {
	err := cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		return
	}
	i.cmd, i.stdin = nil, nil
	i.active = false
	i.actions = make(map[string]*pendingAction)
	i.state = "error"
	if err != nil {
		i.lastError = err.Error()
	}
	i.mu.Unlock()
	i.broadcastFrame(ipc.Frame{Kind: ipc.KindServerExit, Metadata: ipc.Metadata(map[string]any{"code": code, "error": errorText(err)})})
}

func (i *Instance) broadcast(raw []byte) {
	i.broadcastFrame(ipc.Frame{Kind: ipc.KindServerFrame, Payload: raw})
}

func (i *Instance) broadcastFrame(frame ipc.Frame) {
	i.mu.Lock()
	clients := make([]*clientConn, 0, len(i.desktops))
	for _, conn := range i.desktops {
		clients = append(clients, conn)
	}
	i.mu.Unlock()
	for _, conn := range clients {
		_ = conn.Write(frame)
	}
}

func (i *Instance) sendDesktop(id string, raw []byte) {
	i.mu.Lock()
	conn := i.desktops[id]
	i.mu.Unlock()
	if conn != nil {
		_ = conn.Write(ipc.Frame{Kind: ipc.KindServerFrame, Payload: raw})
	}
}

func (i *Instance) broadcastEvent(eventType string, payload any) {
	conversationID := conversationIDFromPayload(payload)
	i.mu.Lock()
	sequence := int64(0)
	if conversationID != "" {
		projection := i.conversations[conversationID]
		if projection == nil {
			last := i.manager.store.MaxSequence(i.descriptor.ID, conversationID)
			projection = &conversationProjection{ID: conversationID, Status: "idle", Sequence: last, Revision: last}
			i.conversations[conversationID] = projection
		}
		projection.Sequence++
		projection.Revision++
		projection.Status = statusForEvent(eventType, projection.Status)
		sequence = projection.Sequence
	} else {
		i.eventSequence++
		sequence = i.eventSequence
	}
	instanceID := i.descriptor.ID
	i.mu.Unlock()
	event := protocol.Event{Type: "event", Sequence: sequence, Event: eventType, InstanceID: instanceID, ConversationID: conversationID, Payload: payload}
	if conversationID != "" {
		i.mu.Lock()
		projection := i.conversations[conversationID]
		projection.Events = append(projection.Events, event)
		if len(projection.Events) > 1000 {
			projection.Events = append([]protocol.Event(nil), projection.Events[len(projection.Events)-1000:]...)
		}
		i.mu.Unlock()
	}
	i.manager.publish(event)
	_ = i.manager.store.AppendEvent(instanceID, conversationID, sequence, eventType, payload)
	if conversationID != "" {
		i.mu.Lock()
		projection := i.conversations[conversationID]
		status, revision, currentSequence := projection.Status, projection.Revision, projection.Sequence
		i.mu.Unlock()
		_ = i.manager.store.UpsertConversation(instanceID, conversationID, status, revision, currentSequence)
		_ = i.manager.store.UpsertTurn(instanceID, conversationID, identifierFromPayload(payload, "turnId"), status)
	}
}

func (i *Instance) CheckExpectedRevision(params json.RawMessage) error {
	var request struct {
		ThreadID         string `json:"threadId"`
		ExpectedRevision *int64 `json:"expectedRevision"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	if request.ExpectedRevision == nil {
		return nil
	}
	i.mu.Lock()
	projection := i.conversations[request.ThreadID]
	current := int64(0)
	if projection != nil {
		current = projection.Revision
	}
	i.mu.Unlock()
	if current != *request.ExpectedRevision {
		return fmt.Errorf("revision conflict: expected %d, current %d", *request.ExpectedRevision, current)
	}
	return nil
}

func (i *Instance) ConversationSubscription(params json.RawMessage) any {
	var request struct {
		ThreadID string `json:"threadId"`
		Cursor   int64  `json:"cursor"`
	}
	_ = json.Unmarshal(params, &request)
	i.mu.Lock()
	projection := i.conversations[request.ThreadID]
	if projection == nil && request.ThreadID != "" {
		stored, _ := i.manager.store.ReadEvents(i.descriptor.ID, request.ThreadID, 0, 1000)
		projection = &conversationProjection{ID: request.ThreadID, Status: "idle"}
		for _, item := range stored {
			event := protocol.Event{Type: "event", Sequence: item.Sequence, Event: item.EventType, InstanceID: i.descriptor.ID, ConversationID: request.ThreadID, Payload: item.Payload}
			projection.Events = append(projection.Events, event)
			projection.Sequence, projection.Revision = item.Sequence, item.Sequence
			projection.Status = statusForEvent(item.EventType, projection.Status)
		}
		i.conversations[request.ThreadID] = projection
	}
	if projection == nil {
		i.mu.Unlock()
		return map[string]any{"threadId": request.ThreadID, "status": "idle", "revision": 0, "sequence": 0, "events": []protocol.Event{}, "snapshot": true}
	}
	events := make([]protocol.Event, 0)
	cursorValid := request.Cursor == 0
	for _, event := range projection.Events {
		if event.Sequence == request.Cursor {
			cursorValid = true
		}
		if event.Sequence > request.Cursor {
			events = append(events, event)
		}
	}
	if !cursorValid {
		events = append([]protocol.Event(nil), projection.Events...)
	}
	result := map[string]any{"threadId": projection.ID, "status": projection.Status, "revision": projection.Revision, "sequence": projection.Sequence, "events": events, "snapshot": !cursorValid}
	i.mu.Unlock()
	return result
}

func (i *Instance) Snapshot() InstanceSnapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	return InstanceSnapshot{
		ID: i.descriptor.ID, Name: i.descriptor.Name, CodexHome: i.descriptor.CodexHome,
		UserDataDir: i.descriptor.UserDataDir, State: i.state, Runtime: i.runtime,
		PendingRuntime: i.pendingRuntime, DesktopConnections: len(i.desktops), ActiveTurn: i.active,
		PendingActions: len(i.actions), LastError: i.lastError,
	}
}

func (i *Instance) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	i.mu.Lock()
	if i.stdin == nil {
		i.mu.Unlock()
		return nil, errors.New("App Server is not running")
	}
	i.nextRequest++
	id := fmt.Sprintf("wa:c:local:%d", i.nextRequest)
	response := make(chan []byte, 1)
	i.requests[string(mustJSON(id))] = requestOrigin{method: method, internal: true, response: response}
	i.mu.Unlock()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if len(bytes.TrimSpace(params)) > 0 && !bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		request["params"] = json.RawMessage(params)
	}
	raw, _ := json.Marshal(request)
	if method == "turn/start" {
		i.broadcastEvent("frontend.turn.start", map[string]any{"params": json.RawMessage(params), "source": "control"})
	}
	i.manager.traffic.Record(i.descriptor.ID, "control_to_server", raw)
	if err := i.writeServer(append(raw, '\n')); err != nil {
		return nil, err
	}
	select {
	case rawResponse := <-response:
		var message jsonrpc.Message
		if err := json.Unmarshal(rawResponse, &message); err != nil {
			return nil, err
		}
		if len(message.Error) > 0 && !bytes.Equal(bytes.TrimSpace(message.Error), []byte("null")) {
			return nil, fmt.Errorf("App Server: %s", message.Error)
		}
		return append(json.RawMessage(nil), message.Result...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(60 * time.Second):
		return nil, errors.New("App Server request timed out")
	}
}

func (i *Instance) Actions() []pendingAction {
	i.mu.Lock()
	defer i.mu.Unlock()
	result := make([]pendingAction, 0, len(i.actions))
	for _, action := range i.actions {
		result = append(result, *action)
	}
	return result
}

func (i *Instance) CanStartTurn() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.stdin != nil && !i.active && len(i.actions) == 0 && i.state == "ready"
}

func (i *Instance) ResolveAction(id, result json.RawMessage) error {
	key := string(id)
	i.mu.Lock()
	action := i.actions[key]
	if action == nil || action.Resolved {
		i.mu.Unlock()
		return errors.New("action is no longer pending")
	}
	action.Resolved = true
	delete(i.actions, key)
	i.mu.Unlock()
	_ = i.manager.store.ResolvePendingAction(i.descriptor.ID, key)
	response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": json.RawMessage(result)}
	raw, _ := json.Marshal(response)
	i.manager.traffic.Record(i.descriptor.ID, "control_to_server", raw)
	if err := i.writeServer(append(raw, '\n')); err != nil {
		return err
	}
	i.broadcastEvent("action.resolved", map[string]any{"id": json.RawMessage(id)})
	return nil
}

func environmentList(values []string, realExecutable string) []string {
	result := make([]string, 0, len(values)+1)
	for _, item := range values {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, "CODEX_CLI_PATH") {
			continue
		}
		result = append(result, item)
	}
	return append(result, "CODEX_CLI_PATH="+realExecutable)
}

func ensureNewline(raw []byte) []byte {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return append(append([]byte(nil), raw...), '\n')
	}
	return raw
}

func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func conversationIDFromPayload(payload any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return ""
	}
	for _, key := range []string{"conversationId", "threadId"} {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	if params, ok := object["params"].(map[string]any); ok {
		for _, key := range []string{"conversationId", "threadId"} {
			if value, ok := params[key].(string); ok {
				return value
			}
		}
	}
	return ""
}

func identifierFromPayload(payload any, key string) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			if found, ok := typed[key].(string); ok {
				return found
			}
			if key == "turnId" {
				if turn, ok := typed["turn"].(map[string]any); ok {
					if id, ok := turn["id"].(string); ok {
						return id
					}
				}
			}
			for _, nested := range typed {
				if found := walk(nested); found != "" {
					return found
				}
			}
		case []any:
			for _, nested := range typed {
				if found := walk(nested); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return walk(value)
}

func statusForEvent(eventType, current string) string {
	lower := strings.ToLower(eventType)
	switch {
	case strings.Contains(lower, "turn/started"):
		return "running"
	case strings.Contains(lower, "action.created"):
		return "waiting_action"
	case strings.Contains(lower, "turn/completed"):
		return "completed"
	case strings.Contains(lower, "turn/failed"):
		return "failed"
	case strings.Contains(lower, "turn/interrupted"):
		return "interrupted"
	default:
		return current
	}
}

func sameInitializeCapabilities(first, second []byte) bool {
	firstMessage, firstErr := jsonrpc.Parse(bytes.TrimSpace(first))
	secondMessage, secondErr := jsonrpc.Parse(bytes.TrimSpace(second))
	if firstErr != nil || secondErr != nil {
		return false
	}
	var a, b any
	if json.Unmarshal(firstMessage.Params, &a) != nil || json.Unmarshal(secondMessage.Params, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
