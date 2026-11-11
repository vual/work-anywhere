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
	"sort"
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
	desktopID       string
	originalID      json.RawMessage
	method          string
	internal        bool
	response        chan []byte
	startedAt       time.Time
	settingsContext threadSettingsRequest
}

type pendingAction struct {
	desktopIDs     map[string]bool
	ID             json.RawMessage `json:"id"`
	Method         string          `json:"method"`
	Params         json.RawMessage `json:"params,omitempty"`
	ConversationID string          `json:"conversationId,omitempty"`
	TurnID         string          `json:"turnId,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	Resolved       bool            `json:"resolved"`
}

func requiresUserAction(action *pendingAction) bool {
	if action == nil {
		return false
	}
	switch action.Method {
	case "attestation/generate", "account/chatgptAuthTokens/refresh", "item/tool/call", "currentTime/read":
		return false
	default:
		return true
	}
}

func actionBlocksThread(action *pendingAction, threadID string) bool {
	return requiresUserAction(action) && (action.ConversationID == "" || action.ConversationID == threadID)
}

// Called with i.mu held.
func (i *Instance) pendingUserActionCountLocked() int {
	count := 0
	for _, action := range i.actions {
		if requiresUserAction(action) && !action.Resolved {
			count++
		}
	}
	return count
}

type conversationProjection struct {
	ID          string
	Status      string
	Sequence    int64
	Revision    int64
	ReplayFloor int64
	Events      []protocol.Event
	EventBytes  []int
	EventTimes  []time.Time
	LastAccess  time.Time
}

type threadCollaborationSettings struct {
	TurnID             string `json:"turnId,omitempty"`
	Mode               string `json:"mode"`
	Model              string `json:"model"`
	ReasoningEffort    string `json:"reasoningEffort,omitempty"`
	PermissionModeID   string `json:"permissionModeId,omitempty"`
	PermissionProfile  string `json:"permissionProfile,omitempty"`
	SandboxMode        string `json:"sandboxMode,omitempty"`
	SandboxPolicyJSON  string `json:"sandboxPolicyJSON,omitempty"`
	ApprovalPolicy     string `json:"approvalPolicy,omitempty"`
	ApprovalPolicyJSON string `json:"approvalPolicyJSON,omitempty"`
	ApprovalsReviewer  string `json:"approvalsReviewer,omitempty"`
	Succeeded          bool   `json:"succeeded"`
}

type initializeWaiter struct {
	DesktopID  string
	OriginalID json.RawMessage
}

type Instance struct {
	manager *Manager

	mu                       sync.Mutex
	initializeMu             sync.Mutex
	serverWriteMu            sync.Mutex
	settingsPersistMu        sync.Mutex
	controlWrites            map[string]*sync.Mutex
	pendingTurnStarts        map[string]bool
	createdThreads           map[string]bool
	legacyTimeline           map[string]uint64
	descriptor               protocol.InstanceDescriptor
	runtime                  protocol.RuntimeDescriptor
	pendingRuntime           *protocol.RuntimeDescriptor
	args                     []string
	cwd                      string
	environment              []string
	state                    string
	lastError                string
	outputError              string
	stderrTail               string
	lastExitCode             *int
	activeTurns              map[string]string
	desktops                 map[string]*clientConn
	requests                 map[string]requestOrigin
	actions                  map[string]*pendingAction
	conversations            map[string]*conversationProjection
	threadSettings           map[string]threadCollaborationSettings
	settingsNotifications    map[string]uint64
	settingsClock            uint64
	emptyThreadsToResume     map[string]uint64
	pendingTurnSettings      map[string]threadCollaborationSettings
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
	initializedSent          bool
	initializeWaiters        []initializeWaiter
	// restartHook replaces Restart in tests that cannot spawn an App Server.
	restartHook          func() error
	autoRestartAttempted bool
	uncertainWrite       bool
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
	LastExitCode       *int                        `json:"lastExitCode,omitempty"`
}

func NewInstance(manager *Manager, hello protocol.Hello) *Instance {
	return &Instance{
		manager: manager, descriptor: hello.Instance, runtime: hello.Runtime,
		args: append([]string(nil), hello.Args...), cwd: hello.CWD,
		environment: append([]string(nil), hello.Environment...), state: "stopped",
		desktops: make(map[string]*clientConn), requests: make(map[string]requestOrigin),
		actions:               make(map[string]*pendingAction),
		activeTurns:           make(map[string]string),
		conversations:         make(map[string]*conversationProjection),
		threadSettings:        make(map[string]threadCollaborationSettings),
		pendingTurnSettings:   make(map[string]threadCollaborationSettings),
		settingsNotifications: make(map[string]uint64),
		pendingTurnStarts:     make(map[string]bool), createdThreads: make(map[string]bool), legacyTimeline: make(map[string]uint64),
	}
}

func (i *Instance) threadSettingsKey(threadID string) string {
	return "thread-collaboration:" + i.descriptor.ID + ":" + threadID
}

func (i *Instance) persistThreadSettings(threadID string, settings threadCollaborationSettings) {
	if threadID == "" || settings.Mode == "" || settings.Model == "" {
		return
	}
	i.settingsPersistMu.Lock()
	defer i.settingsPersistMu.Unlock()
	i.mu.Lock()
	current, currentOK := i.threadSettings[threadID]
	i.mu.Unlock()
	if !currentOK || current != settings {
		return
	}
	if err := i.manager.settings.PutSetting(i.threadSettingsKey(threadID), settings); err != nil && i.manager.log != nil {
		i.manager.log.Event("thread_settings.persist_failed", map[string]any{"instanceId": i.descriptor.ID, "threadId": threadID, "error": err.Error()})
	}
}

func (i *Instance) persistThreadSettingsAsync(threadID string, settings threadCollaborationSettings) {
	if threadID == "" || settings.Mode == "" || settings.Model == "" || i.manager == nil || i.manager.stopping.Load() {
		return
	}
	i.manager.configWriteMu.Lock()
	if i.manager.stopping.Load() {
		i.manager.configWriteMu.Unlock()
		return
	}
	i.manager.configWrites.Add(1)
	i.manager.configWriteMu.Unlock()
	go func() {
		defer i.manager.configWrites.Done()
		i.persistThreadSettings(threadID, settings)
	}()
}

func (i *Instance) collaborationSettings(threadID string) (threadCollaborationSettings, bool) {
	i.mu.Lock()
	settings, ok := i.threadSettings[threadID]
	i.mu.Unlock()
	if ok {
		return settings, true
	}
	var stored threadCollaborationSettings
	found, err := i.manager.settings.GetSetting(i.threadSettingsKey(threadID), &stored)
	if err != nil || !found || stored.Mode == "" || stored.Model == "" {
		return threadCollaborationSettings{}, false
	}
	i.mu.Lock()
	if current, exists := i.threadSettings[threadID]; exists {
		stored = current
	} else {
		i.threadSettings[threadID] = stored
	}
	i.mu.Unlock()
	return stored, true
}

func collaborationSettingsFromParams(params json.RawMessage, notification bool) (string, threadCollaborationSettings, bool) {
	var root map[string]any
	if json.Unmarshal(params, &root) != nil {
		return "", threadCollaborationSettings{}, false
	}
	threadID, _ := root["threadId"].(string)
	settingsRoot := root
	if notification {
		if value, ok := root["threadSettings"].(map[string]any); ok {
			settingsRoot = value
		}
	}
	modeValue, ok := settingsRoot["collaborationMode"].(map[string]any)
	if !ok {
		return threadID, threadCollaborationSettings{}, false
	}
	mode, _ := modeValue["mode"].(string)
	modeSettings, _ := modeValue["settings"].(map[string]any)
	model, _ := modeSettings["model"].(string)
	effort, _ := modeSettings["reasoning_effort"].(string)
	if effort == "" {
		effort, _ = settingsRoot["effort"].(string)
	}
	if model == "" {
		model, _ = settingsRoot["model"].(string)
	}
	permissionProfile, _ := settingsRoot["permissions"].(string)
	if active, ok := settingsRoot["activePermissionProfile"].(map[string]any); ok {
		if id, ok := active["id"].(string); ok {
			permissionProfile = id
		}
	}
	approvalPolicy, _ := settingsRoot["approvalPolicy"].(string)
	approvalJSON := ""
	if value, exists := settingsRoot["approvalPolicy"]; exists {
		encoded, _ := json.Marshal(value)
		approvalJSON = string(encoded)
	}
	approvalsReviewer, _ := settingsRoot["approvalsReviewer"].(string)
	sandboxMode := ""
	sandboxJSON := ""
	if sandbox, ok := settingsRoot["sandboxPolicy"].(map[string]any); ok {
		sandboxMode, _ = sandbox["type"].(string)
		encoded, _ := json.Marshal(sandbox)
		sandboxJSON = string(encoded)
	}
	if sandboxMode == "" {
		sandboxMode = sandboxPolicyTypeForProfile(permissionProfile)
	}
	if permissionProfile == "" {
		permissionProfile = sandboxProfile(sandboxMode)
	}
	permissionModeID := permissionModeID(permissionProfile, approvalPolicy, approvalsReviewer)
	if permissionModeID == "" {
		permissionModeID = "custom"
	}
	if threadID == "" || (mode != "plan" && mode != "default") || model == "" {
		return threadID, threadCollaborationSettings{}, false
	}
	return threadID, threadCollaborationSettings{
		Mode: mode, Model: model, ReasoningEffort: effort,
		PermissionModeID: permissionModeID, PermissionProfile: permissionProfile,
		SandboxMode:       sandboxMode,
		SandboxPolicyJSON: sandboxJSON,
		ApprovalPolicy:    approvalPolicy, ApprovalPolicyJSON: approvalJSON, ApprovalsReviewer: approvalsReviewer,
	}, true
}

func sandboxPolicyTypeForProfile(profile string) string {
	switch profile {
	case ":danger-full-access":
		return "dangerFullAccess"
	case ":read-only":
		return "readOnly"
	case ":workspace":
		return "workspaceWrite"
	default:
		return ""
	}
}

func permissionModeID(profile, approvalPolicy, reviewer string) string {
	switch {
	case profile == ":workspace" && approvalPolicy == "on-request" && reviewer == "auto_review":
		return "autoReview"
	case profile == ":workspace" && approvalPolicy == "on-request" && reviewer == "user":
		return "default"
	case profile == ":read-only" && approvalPolicy == "on-request" && reviewer == "user":
		return "readOnly"
	case profile == ":danger-full-access" && approvalPolicy == "never" && reviewer == "user":
		return "fullAccess"
	default:
		return ""
	}
}

func (i *Instance) observeOutgoingThreadSettings(method string, params json.RawMessage) {
	if method != "turn/start" && method != "thread/settings/update" {
		return
	}
	threadID, settings, explicit := collaborationSettingsFromParams(params, false)
	if threadID == "" {
		return
	}
	if explicit && settings.SandboxPolicyJSON == "" && settings.PermissionProfile == "" {
		if current, exists := i.collaborationSettings(threadID); exists {
			settings.PermissionModeID, settings.PermissionProfile = current.PermissionModeID, current.PermissionProfile
			settings.SandboxMode, settings.SandboxPolicyJSON = current.SandboxMode, current.SandboxPolicyJSON
			if settings.ApprovalPolicy == "" {
				settings.ApprovalPolicy = current.ApprovalPolicy
				settings.ApprovalPolicyJSON = current.ApprovalPolicyJSON
			}
			if settings.ApprovalsReviewer == "" {
				settings.ApprovalsReviewer = current.ApprovalsReviewer
			}
		}
	}
	if !explicit {
		var ok bool
		settings, ok = i.collaborationSettings(threadID)
		if !ok {
			return
		}
		var values map[string]any
		if json.Unmarshal(params, &values) == nil {
			if model, valueOK := values["model"].(string); valueOK && model != "" {
				settings.Model = model
			}
			if effort, valueOK := values["effort"].(string); valueOK {
				settings.ReasoningEffort = effort
			} else if effort, valueOK := values["reasoningEffort"].(string); valueOK {
				settings.ReasoningEffort = effort
			}
		}
	}
	if method == "thread/settings/update" {
		if current, exists := i.collaborationSettings(threadID); exists {
			settings.TurnID = current.TurnID
			settings.Succeeded = current.Succeeded
		}
	} else {
		settings.TurnID = ""
	}
	i.mu.Lock()
	if method == "turn/start" {
		i.pendingTurnSettings[threadID] = settings
	} else {
		i.threadSettings[threadID] = settings
		i.settingsClock++
		i.settingsNotifications[threadID] = i.settingsClock
	}
	i.mu.Unlock()
	if method != "turn/start" {
		i.persistThreadSettingsAsync(threadID, settings)
	}
}

func (i *Instance) clearPendingTurnSettings(method string, params json.RawMessage) {
	if method != "turn/start" {
		return
	}
	threadID := conversationIDFromPayload(json.RawMessage(params))
	if threadID == "" {
		return
	}
	i.mu.Lock()
	delete(i.pendingTurnSettings, threadID)
	delete(i.pendingTurnStarts, threadID)
	i.mu.Unlock()
	i.broadcastEvent("frontend.turn.failed", map[string]any{"threadId": threadID})
}

func (i *Instance) observeThreadSettingsNotification(params json.RawMessage) {
	threadID, settings, ok := collaborationSettingsFromParams(params, true)
	if !ok {
		return
	}
	i.mu.Lock()
	current, hasCurrent := i.threadSettings[threadID]
	if hasCurrent {
		settings.TurnID = current.TurnID
		settings.Succeeded = current.Succeeded
	}
	i.settingsClock++
	i.settingsNotifications[threadID] = i.settingsClock
	i.threadSettings[threadID] = settings
	i.mu.Unlock()
	i.persistThreadSettingsAsync(threadID, settings)
}

func (i *Instance) RegisterRuntime(hello protocol.Hello) error {
	i.mu.Lock()
	i.descriptor = hello.Instance
	i.args = append([]string(nil), hello.Args...)
	i.cwd = hello.CWD
	i.environment = append([]string(nil), hello.Environment...)
	started := i.cmd != nil
	changed := started && i.runtime.SHA256 != "" && hello.Runtime.SHA256 != "" && i.runtime.SHA256 != hello.Runtime.SHA256
	busy := len(i.activeTurns) > 0 || len(i.actions) > 0
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
			if origin.method == "initialize" {
				// The upstream handshake belongs to the process, even if its
				// initiating frontend disconnects before the response arrives.
				origin.internal, origin.desktopID = true, ""
				i.requests[requestID] = origin
			} else {
				delete(i.requests, requestID)
			}
		}
	}
	generation := i.generation
	var unavailable []json.RawMessage
	for key, action := range i.actions {
		if !requiresUserAction(action) {
			delete(action.desktopIDs, id)
			if len(action.desktopIDs) == 0 {
				unavailable = append(unavailable, action.ID)
				delete(i.actions, key)
			}
		}
	}
	i.mu.Unlock()
	for _, requestID := range unavailable {
		_ = i.writeServerForGeneration(desktopUnavailable(requestID), &generation)
	}
}

func (i *Instance) Start() error { return i.startGeneration(nil) }

func (i *Instance) startGeneration(expected *uint64) error {
	i.mu.Lock()
	if expected != nil && i.generation != *expected {
		i.mu.Unlock()
		return nil
	}
	if i.cmd != nil {
		i.mu.Unlock()
		return nil
	}
	i.uncertainWrite = false
	i.state, i.lastError, i.outputError, i.stderrTail = "starting", "", "", ""
	i.lastExitCode = nil
	i.initializedSent = false
	args := codex.AppServerArgs(i.args)
	if !codex.IsPlainAppServer(args) {
		i.state, i.lastError = "error", "only a long-running stdio app-server can be supervised"
		i.mu.Unlock()
		return errors.New("only a long-running stdio app-server can be supervised")
	}
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
		_ = cmd.Wait()
		return i.startFailed(generation, err)
	}

	i.mu.Lock()
	if generation != i.generation {
		i.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("instance generation changed during startup")
	}
	i.cmd, i.stdin, i.state = cmd, stdin, "waiting_initialize"
	cachedInit := append([]byte(nil), i.cachedInitializeRequest...)
	i.mu.Unlock()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); i.readServer(generation, stdout) }()
	go func() { defer readers.Done(); i.readServerStderr(generation, stderr) }()
	go func() { readers.Wait(); i.waitServer(generation, cmd) }()
	if len(cachedInit) > 0 {
		i.mu.Lock()
		i.initializeInFlight = true
		i.mu.Unlock()
		if err = i.sendInternalInitialize(cachedInit); err != nil {
			i.mu.Lock()
			i.initializeInFlight = false
			i.mu.Unlock()
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

func (i *Instance) restartProcess() error {
	if i.restartHook != nil {
		return i.restartHook()
	}
	return i.Restart()
}

func (i *Instance) Stop(release bool) { i.stopProcess(release) }

func (i *Instance) stopProcess(release bool) {
	i.mu.Lock()
	i.generation++
	cmd := i.cmd
	origins := i.requests
	i.requests = make(map[string]requestOrigin)
	affected := make(map[string]bool)
	for threadID := range i.activeTurns {
		affected[threadID] = true
	}
	for threadID := range i.pendingTurnStarts {
		affected[threadID] = true
	}
	for _, action := range i.actions {
		if action.ConversationID != "" {
			affected[action.ConversationID] = true
		}
	}
	i.activeTurns = make(map[string]string)
	i.pendingTurnStarts = make(map[string]bool)
	i.pendingTurnSettings = make(map[string]threadCollaborationSettings)
	i.actions = make(map[string]*pendingAction)
	i.createdThreads = make(map[string]bool)
	i.emptyThreadsToResume = nil
	i.legacyTimeline = make(map[string]uint64)
	i.cachedInitializeResponse = nil
	i.initializedSent = false
	i.initializeInFlight = false
	i.cmd, i.stdin = nil, nil
	if release && i.lease != nil {
		_ = i.lease.Close()
		i.lease = nil
	}
	i.state = "stopped"
	i.mu.Unlock()
	for id, origin := range origins {
		i.notifyRequestFailure(id, origin, "App Server stopped")
	}
	for threadID := range affected {
		i.broadcastEvent("frontend.thread.state.reset", map[string]any{"threadId": threadID})
	}
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
		return i.resolveServerRequest(conn.id, message, raw)
	}
	if message.IsNotification() {
		if message.Method == "initialized" {
			return i.sendInitialized(raw)
		}
		return i.writeServer(raw)
	}
	if !message.IsRequest() {
		return i.writeServer(raw)
	}
	if message.Method == "turn/start" {
		i.observeOutgoingThreadSettings(message.Method, message.Params)
		i.broadcastEvent("frontend.turn.start", map[string]any{"params": json.RawMessage(message.Params), "source": "desktop"})
	} else if message.Method == "thread/settings/update" {
		i.observeOutgoingThreadSettings(message.Method, message.Params)
	}

	if message.Method == "initialize" {
		i.initializeMu.Lock()
		defer i.initializeMu.Unlock()
		i.mu.Lock()
		cached := append([]byte(nil), i.cachedInitializeResponse...)
		cachedRequest := append([]byte(nil), i.cachedInitializeRequest...)
		if len(cached) == 0 && i.initializeInFlight {
			if sameInitializeCapabilities(cachedRequest, raw) {
				i.initializeWaiters = append(i.initializeWaiters, initializeWaiter{DesktopID: conn.id, OriginalID: append(json.RawMessage(nil), message.ID...)})
				i.mu.Unlock()
				return nil
			}
			// A restart (for example after a Codex Desktop upgrade) replays the
			// previous frontend's handshake. Nobody waits on that replay, so a
			// connecting frontend with different capabilities supersedes it
			// instead of being rejected.
			_, replaying := i.requests[internalInitializeKey]
			if !replaying || len(i.initializeWaiters) > 0 || len(i.activeTurns) > 0 || len(i.actions) > 0 {
				i.mu.Unlock()
				return initializeConflict(conn, message.ID)
			}
			i.cachedInitializeRequest, i.cachedInitialized = nil, nil
			i.initializeInFlight = false
			i.mu.Unlock()
			if err := i.restartProcess(); err != nil {
				return err
			}
			i.mu.Lock()
		}
		if len(i.cachedInitializeRequest) == 0 {
			i.cachedInitializeRequest = append([]byte(nil), raw...)
		}
		if len(cached) == 0 {
			i.initializeInFlight = true
		}
		busy := len(i.activeTurns) > 0 || len(i.actions) > 0
		i.mu.Unlock()
		if len(cached) > 0 {
			if !sameInitializeCapabilities(cachedRequest, raw) {
				if busy {
					i.mu.Lock()
					i.pendingInitialize = append([]byte(nil), raw...)
					i.state = "upgrade_pending"
					i.mu.Unlock()
					return initializeConflict(conn, message.ID)
				} else {
					i.mu.Lock()
					i.cachedInitializeRequest, i.cachedInitializeResponse = nil, nil
					i.initializeInFlight = false
					i.mu.Unlock()
					if restartErr := i.restartProcess(); restartErr != nil {
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
			return conn.Enqueue(ipc.Frame{Kind: ipc.KindServerFrame, Payload: append(response, '\n')})
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
	proxyKey := string(mustJSON(proxyID))
	if err == nil {
		i.requests[proxyKey] = requestOrigin{desktopID: conn.id, originalID: append(json.RawMessage(nil), message.ID...), method: message.Method, startedAt: time.Now(), settingsContext: i.threadSettingsRequestLocked(message.Method, message.Params)}
	}
	i.mu.Unlock()
	if err != nil {
		return err
	}
	if err := i.writeServer(append(rewritten, '\n')); err != nil {
		i.removeRequest(proxyKey)
		if message.Method == "initialize" {
			i.mu.Lock()
			i.initializeInFlight = false
			i.cachedInitializeRequest = nil
			waiters := i.initializeWaiters
			i.initializeWaiters = nil
			i.mu.Unlock()
			i.answerInitializeFailure(waiters, "initialize write failed")
		}
		return err
	}
	return nil
}

func (i *Instance) removeRequest(key string) {
	i.mu.Lock()
	delete(i.requests, key)
	i.mu.Unlock()
}

func (i *Instance) abandonRequest(key string) {
	i.mu.Lock()
	if origin, ok := i.requests[key]; ok && !readOnlyProviderMethod(origin.method) {
		i.uncertainWrite = true
	}
	delete(i.requests, key)
	i.mu.Unlock()
}

func (i *Instance) logRequestFailure(key, method, reason string, started time.Time) {
	if i.manager.log != nil {
		i.manager.log.Event("appserver.request_failed", map[string]any{
			"instanceId": i.descriptor.ID, "requestId": key, "method": method,
			"durationMs": time.Since(started).Milliseconds(), "reason": reason,
		})
	}
}

func (i *Instance) resolveServerRequest(desktopID string, message jsonrpc.Message, raw []byte) error {
	key := string(message.ID)
	i.mu.Lock()
	action := i.actions[key]
	if action == nil || action.Resolved {
		i.mu.Unlock()
		return nil
	}
	if !requiresUserAction(action) && !action.desktopIDs[desktopID] {
		i.mu.Unlock()
		return errors.New("infrastructure response must come from a receiving Desktop")
	}
	action.Resolved = true
	delete(i.actions, key)
	generation := i.generation
	i.updateActivityStateLocked()
	i.mu.Unlock()
	if err := i.writeServerForGeneration(raw, &generation); err != nil {
		return err
	}
	if requiresUserAction(action) {
		i.broadcastEvent("action.resolved", map[string]any{"id": json.RawMessage(message.ID), "threadId": action.ConversationID})
	}
	return nil
}

func (i *Instance) writeServer(raw []byte) error {
	return i.writeServerForGeneration(raw, nil)
}

func (i *Instance) writeServerForGeneration(raw []byte, expected *uint64) error {
	if len(ensureNewline(raw)) > ipc.DefaultMaxFrame {
		return &mobileContractError{code: "too_large", message: "serialized App Server request exceeds 64 MiB"}
	}
	i.mu.Lock()
	stdin := i.stdin
	i.mu.Unlock()
	if stdin == nil {
		return errors.New("App Server is not running")
	}
	i.serverWriteMu.Lock()
	defer i.serverWriteMu.Unlock()
	// Recheck the handle after waiting for concurrent process shutdown.
	i.mu.Lock()
	if expected != nil && *expected != i.generation {
		i.mu.Unlock()
		return errors.New("App Server generation changed")
	}
	stdin = i.stdin
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
			if !errors.Is(err, io.EOF) {
				i.failServerOutput(generation, "stdout", err)
			}
			return
		}
	}
}

func (i *Instance) readServerStderr(generation uint64, stream io.Reader) {
	reader := bufio.NewReaderSize(stream, 16<<10)
	const maxDiagnostic = 64 << 10
	line := make([]byte, 0, maxDiagnostic)
	truncated := false
	for {
		fragment, err := reader.ReadSlice('\n')
		remaining := maxDiagnostic - len(line)
		if len(fragment) > remaining {
			line = append(line, fragment[:remaining]...)
			truncated = true
		} else {
			line = append(line, fragment...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(line) > 0 {
			message := strings.TrimRight(string(line), "\r\n")
			if truncated {
				message += " [truncated]"
			}
			i.mu.Lock()
			if generation == i.generation && i.outputError == "" {
				i.lastError = message
				i.stderrTail += message + "\n"
				if len(i.stderrTail) > maxDiagnostic {
					i.stderrTail = i.stderrTail[len(i.stderrTail)-maxDiagnostic:]
				}
			}
			i.mu.Unlock()
		}
		line, truncated = line[:0], false
		if err != nil {
			if !errors.Is(err, io.EOF) {
				i.failServerOutput(generation, "stderr", err)
			}
			return
		}
	}
}

func (i *Instance) failServerOutput(generation uint64, stream string, err error) {
	i.mu.Lock()
	if generation != i.generation || i.outputError != "" {
		i.mu.Unlock()
		return
	}
	i.outputError = fmt.Sprintf("App Server %s read failed: %v", stream, err)
	i.lastError, i.state = i.outputError, "error"
	cmd := i.cmd
	i.mu.Unlock()
	// Never acquire serverWriteMu: the child may be blocked writing output
	// while another goroutine is blocked writing its stdin.
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
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
		if generation != i.generation {
			i.mu.Unlock()
			return
		}
		origin, ok := i.requests[key]
		if ok {
			delete(i.requests, key)
		}
		i.mu.Unlock()
		if !ok {
			return
		}
		if len(message.Error) == 0 || bytes.Equal(bytes.TrimSpace(message.Error), []byte("null")) {
			i.observeThreadSettingsResponse(generation, origin, message.Result)
		}
		if origin.internal {
			if origin.response != nil {
				select {
				case origin.response <- append([]byte(nil), raw...):
				default:
				}
				return
			}
			i.completeInitialize(generation, raw)
			return
		}
		restored, rewriteErr := jsonrpc.RewriteRawID(bytes.TrimSpace(raw), origin.originalID)
		if rewriteErr != nil {
			return
		}
		restored = append(restored, '\n')
		if origin.method == "initialize" {
			restored = i.completeInitialize(generation, restored)
			if restored == nil {
				return
			}
		}
		i.sendDesktop(origin.desktopID, restored)
		return
	}
	if message.IsRequest() {
		if message.Method == "currentTime/read" {
			_ = i.writeServerForGeneration(mustJSON(map[string]any{"id": message.ID, "result": map[string]any{"currentTimeAt": time.Now().Unix()}}), &generation)
			return
		}
		action := &pendingAction{
			ID: append(json.RawMessage(nil), message.ID...), Method: message.Method,
			Params:         append(json.RawMessage(nil), message.Params...),
			ConversationID: conversationIDFromPayload(json.RawMessage(message.Params)),
			TurnID:         identifierFromPayload(json.RawMessage(message.Params), "turnId"),
			CreatedAt:      time.Now().UTC(),
		}
		i.mu.Lock()
		if generation != i.generation {
			i.mu.Unlock()
			return
		}
		if !requiresUserAction(action) {
			action.desktopIDs = make(map[string]bool)
			for id, desktop := range i.desktops {
				if !desktop.closed.Load() {
					action.desktopIDs[id] = true
				}
			}
		}
		if !requiresUserAction(action) && len(action.desktopIDs) == 0 {
			i.mu.Unlock()
			_ = i.writeServerForGeneration(desktopUnavailable(message.ID), &generation)
			return
		}
		i.actions[string(message.ID)] = action
		recipients := make([]string, 0, len(action.desktopIDs))
		for id := range action.desktopIDs {
			recipients = append(recipients, id)
		}
		i.updateActivityStateLocked()
		i.mu.Unlock()
		if requiresUserAction(action) {
			i.broadcast(raw)
			i.broadcastEvent("action.created", action)
		} else {
			// Use the captured recipient set, not desktops joining after the request.
			for _, id := range recipients {
				i.sendDesktop(id, raw)
			}
		}
		return
	}
	if message.IsNotification() {
		eventPayload := map[string]any{"method": message.Method, "params": json.RawMessage(message.Params)}
		if message.Method == "serverRequest/resolved" {
			var resolved struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(message.Params, &resolved)
			i.mu.Lock()
			if action := i.actions[string(resolved.RequestID)]; action != nil {
				eventPayload["threadId"] = action.ConversationID
			}
			i.mu.Unlock()
		}
		i.observeNotification(message.Method, message.Params)
		i.broadcastEvent("provider."+message.Method, eventPayload)
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
	if strings.Contains(lower, "thread/settings/updated") {
		i.observeThreadSettingsNotification(params)
	}
	var settingsToPersist *threadCollaborationSettings
	var settingsThreadID string
	i.mu.Lock()
	switch {
	case strings.Contains(lower, "serverrequest/resolved"):
		var resolved struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(params, &resolved)
		delete(i.actions, string(resolved.RequestID))
		i.updateActivityStateLocked()
	case strings.Contains(lower, "turn/started") || strings.HasSuffix(lower, "turn.started"):
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
		threadID := firstNonempty(identifiers.ThreadID, identifiers.Thread.ID)
		turnID := firstNonempty(identifiers.TurnID, identifiers.Turn.ID)
		if threadID != "" {
			i.activeTurns[threadID] = turnID
			delete(i.pendingTurnStarts, threadID)
			delete(i.createdThreads, threadID)
			if settings, ok := i.pendingTurnSettings[threadID]; ok {
				settings.TurnID = turnID
				settings.Succeeded = false
				i.threadSettings[threadID] = settings
				i.settingsClock++
				i.settingsNotifications[threadID] = i.settingsClock
				delete(i.pendingTurnSettings, threadID)
				copy := settings
				settingsToPersist = &copy
				settingsThreadID = threadID
			}
		}
		i.updateActivityStateLocked()
	case strings.Contains(lower, "turn/completed") || strings.Contains(lower, "turn/failed") || strings.Contains(lower, "turn/interrupted"):
		threadID := conversationIDFromPayload(json.RawMessage(params))
		turnID := identifierFromPayload(json.RawMessage(params), "turnId")
		if threadID != "" {
			delete(i.activeTurns, threadID)
		} else if turnID != "" {
			for activeThreadID, activeTurnID := range i.activeTurns {
				if activeTurnID == turnID {
					delete(i.activeTurns, activeThreadID)
					threadID = activeThreadID
					break
				}
			}
		}
		if settings, ok := i.threadSettings[threadID]; ok && settings.TurnID == turnID {
			settings.Succeeded = strings.Contains(lower, "turn/completed")
			i.threadSettings[threadID] = settings
			copy := settings
			settingsToPersist = &copy
			settingsThreadID = threadID
		}
		for id, action := range i.actions {
			if (threadID != "" && action.ConversationID == threadID) || (turnID != "" && action.TurnID == turnID) {
				delete(i.actions, id)
			}
		}
		pending := i.pendingRuntime
		pendingInitialize := append([]byte(nil), i.pendingInitialize...)
		activityDrained := len(i.activeTurns) == 0 && len(i.actions) == 0
		if activityDrained && pending != nil {
			i.runtime = *pending
			i.pendingRuntime = nil
		}
		if activityDrained && len(pendingInitialize) > 0 {
			i.cachedInitializeRequest = pendingInitialize
			i.cachedInitializeResponse = nil
			i.pendingInitialize = nil
		}
		i.updateActivityStateLocked()
		i.mu.Unlock()
		if settingsToPersist != nil {
			i.persistThreadSettingsAsync(settingsThreadID, *settingsToPersist)
		}
		if activityDrained && (pending != nil || len(pendingInitialize) > 0) {
			go i.Restart()
		}
		return
	}
	i.mu.Unlock()
	if settingsToPersist != nil {
		i.persistThreadSettingsAsync(settingsThreadID, *settingsToPersist)
	}
}

func (i *Instance) InterruptActive() error {
	i.mu.Lock()
	turns := make(map[string]string, len(i.activeTurns))
	for threadID, turnID := range i.activeTurns {
		turns[threadID] = turnID
	}
	i.mu.Unlock()
	var firstErr error
	for threadID, turnID := range turns {
		params, _ := json.Marshal(map[string]string{"threadId": threadID, "turnId": turnID})
		if _, err := i.Call(context.Background(), "turn/interrupt", params); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

const internalInitializeID = "wa:c:daemon:initialize"

var internalInitializeKey = string(mustJSON(internalInitializeID))

func (i *Instance) sendInternalInitialize(cached []byte) error {
	message, err := jsonrpc.Parse(bytes.TrimSpace(cached))
	if err != nil {
		return err
	}
	rewritten, err := jsonrpc.RewriteID(bytes.TrimSpace(cached), internalInitializeID)
	if err != nil {
		return err
	}
	i.mu.Lock()
	i.requests[internalInitializeKey] = requestOrigin{originalID: message.ID, method: "initialize", internal: true, startedAt: time.Now()}
	i.mu.Unlock()
	i.manager.traffic.Record(i.descriptor.ID, "daemon_to_server", rewritten)
	if err := i.writeServer(append(rewritten, '\n')); err != nil {
		i.removeRequest(internalInitializeKey)
		return err
	}
	return nil
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
	origins := i.requests
	i.requests = make(map[string]requestOrigin)
	waiters := append([]initializeWaiter(nil), i.initializeWaiters...)
	restartable := len(i.activeTurns) == 0 && len(i.actions) == 0 && len(i.pendingTurnStarts) == 0 && len(i.cachedInitializeRequest) > 0 && !i.autoRestartAttempted && !i.uncertainWrite
	for _, origin := range origins {
		if !readOnlyProviderMethod(origin.method) {
			restartable = false
		}
	}
	if i.outputError != "" {
		err = errors.New(i.outputError)
	}
	if restartable {
		i.autoRestartAttempted = true
		for _, origin := range origins {
			if origin.method == "initialize" && origin.desktopID != "" {
				waiters = append(waiters, initializeWaiter{DesktopID: origin.desktopID, OriginalID: append(json.RawMessage(nil), origin.originalID...)})
			}
		}
	} else {
		i.initializeWaiters = nil
	}
	i.cmd, i.stdin = nil, nil
	i.activeTurns = make(map[string]string)
	i.pendingTurnStarts = make(map[string]bool)
	i.pendingTurnSettings = make(map[string]threadCollaborationSettings)
	i.createdThreads = make(map[string]bool)
	i.emptyThreadsToResume = nil
	i.legacyTimeline = make(map[string]uint64)
	i.actions = make(map[string]*pendingAction)
	i.cachedInitializeResponse = nil
	i.initializedSent = false
	i.initializeInFlight = false
	i.state = "error"
	i.lastExitCode = &code
	runtime := i.runtime
	if err != nil {
		i.lastError = err.Error()
		if i.outputError == "" && strings.TrimSpace(i.stderrTail) != "" {
			i.lastError += ": " + strings.TrimSpace(i.stderrTail)
		}
	}
	i.mu.Unlock()
	if i.manager.log != nil {
		i.manager.log.Event("appserver.exited", map[string]any{"instanceId": i.descriptor.ID, "generation": generation, "stage": "process_exit", "exitCode": code, "runtimeVersion": runtime.Version, "runtimeSHA256": runtime.SHA256})
	}
	for id, origin := range origins {
		i.notifyRequestFailure(id, origin, "App Server exited: "+errorText(err))
	}
	if !restartable {
		i.answerInitializeFailure(waiters, "App Server exited")
	}
	i.broadcastFrame(ipc.Frame{Kind: ipc.KindServerExit, Metadata: ipc.Metadata(map[string]any{"code": code, "error": errorText(err)})})
	i.broadcastEvent("frontend.instance.exited", map[string]any{"code": code, "error": errorText(err)})
	if restartable {
		go func() {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			if i.manager.ctx != nil {
				select {
				case <-timer.C:
				case <-i.manager.ctx.Done():
					return
				}
			} else {
				<-timer.C
			}
			if restartErr := i.startGeneration(&generation); restartErr != nil && i.manager.log != nil {
				i.manager.log.Event("instance.auto_restart_failed", map[string]any{"instanceId": i.descriptor.ID, "error": restartErr.Error()})
			}
		}()
	}
}

func (i *Instance) notifyRequestFailure(key string, origin requestOrigin, message string) {
	if i.manager.log != nil {
		i.manager.log.Event("appserver.request_failed", map[string]any{
			"instanceId": i.descriptor.ID, "requestId": key, "method": origin.method,
			"durationMs": time.Since(origin.startedAt).Milliseconds(), "reason": message,
		})
	}
	id := json.RawMessage(key)
	if origin.internal && origin.response != nil {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": message}})
		select {
		case origin.response <- payload:
		default:
		}
		return
	}
	if origin.desktopID == "" || len(origin.originalID) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": origin.originalID, "error": map[string]any{"code": -32000, "message": message}})
	i.sendDesktop(origin.desktopID, append(payload, '\n'))
}

func (i *Instance) answerInitializeFailure(waiters []initializeWaiter, message string) {
	for _, waiter := range waiters {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": waiter.OriginalID, "error": map[string]any{"code": -32000, "message": message}})
		i.sendDesktop(waiter.DesktopID, append(payload, '\n'))
	}
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
		if err := conn.Enqueue(frame); err != nil {
			i.RemoveDesktop(conn.id)
		}
	}
}

func (i *Instance) sendDesktop(id string, raw []byte) {
	i.mu.Lock()
	conn := i.desktops[id]
	i.mu.Unlock()
	if conn != nil {
		if err := conn.Enqueue(ipc.Frame{Kind: ipc.KindServerFrame, Payload: raw}); err != nil {
			i.RemoveDesktop(id)
		}
	}
}

func (i *Instance) broadcastEvent(eventType string, payload any) {
	conversationID := conversationIDFromPayload(payload)
	i.mu.Lock()
	sequence := int64(0)
	if conversationID != "" {
		projection := i.projectionLocked(conversationID)
		if eventType == "frontend.turn.start" {
			delete(i.createdThreads, conversationID)
		}
		projection.Sequence++
		if revisionEvent(eventType) {
			projection.Revision++
		}
		projection.Status = statusForEvent(eventType, projection.Status)
		sequence = projection.Sequence
	} else {
		i.eventSequence++
		sequence = i.eventSequence
	}
	instanceID := i.descriptor.ID
	i.mu.Unlock()
	event := protocol.Event{Type: "event", Sequence: sequence, SyncEpoch: i.manager.syncEpoch, Event: eventType, InstanceID: instanceID, ConversationID: conversationID, Payload: payload}
	if conversationID != "" {
		storedAt := time.Now()
		payloadBytes := 0
		if encoded, err := json.Marshal(payload); err == nil && i.manager.reserveEventPayload(len(encoded)) {
			payloadBytes = len(encoded)
		} else {
			event.Payload = nil
		}
		i.mu.Lock()
		projection := i.projectionLocked(conversationID)
		projection.Events = append(projection.Events, event)
		projection.EventBytes = append(projection.EventBytes, payloadBytes)
		projection.EventTimes = append(projection.EventTimes, storedAt)
		if len(projection.Events) > 256 {
			projection.ReplayFloor = projection.Events[0].Sequence
			removed := projection.EventBytes[0]
			projection.Events = append([]protocol.Event(nil), projection.Events[1:]...)
			projection.EventBytes = append([]int(nil), projection.EventBytes[1:]...)
			projection.EventTimes = append([]time.Time(nil), projection.EventTimes[1:]...)
			if removed > 0 {
				i.manager.eventPayloadBytes.Add(-int64(removed))
			}
		}
		i.mu.Unlock()
	}
	i.manager.publish(event)
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
	current := i.projectionLocked(request.ThreadID).Revision
	i.mu.Unlock()
	if current != *request.ExpectedRevision {
		return fmt.Errorf("revision conflict: expected %d, current %d", *request.ExpectedRevision, current)
	}
	return nil
}

const mobileSubscriptionEventLimit = 64

func (i *Instance) ConversationSubscription(params json.RawMessage) any {
	return i.conversationSubscription(params, false)
}

func (i *Instance) MobileConversationSubscription(params json.RawMessage) any {
	return i.conversationSubscription(params, true)
}

func (i *Instance) conversationSubscription(params json.RawMessage, mobile bool) any {
	var request struct {
		ThreadID string `json:"threadId"`
		Cursor   int64  `json:"cursor"`
	}
	_ = json.Unmarshal(params, &request)
	syncEpoch := ""
	if i.manager != nil {
		syncEpoch = i.manager.syncEpoch
	}
	i.mu.Lock()
	projection := i.conversations[request.ThreadID]
	if projection == nil && request.ThreadID != "" {
		projection = i.projectionLocked(request.ThreadID)
	}
	if projection == nil {
		i.mu.Unlock()
		return map[string]any{"threadId": request.ThreadID, "status": "idle", "revision": 0, "sequence": 0, "syncEpoch": syncEpoch, "events": []protocol.Event{}, "snapshot": true}
	}
	projection.LastAccess = time.Now()
	events := make([]protocol.Event, 0)
	cursorValid := request.Cursor == projection.ReplayFloor
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
	if mobile {
		// Mobile treats subscription events as invalidation hints and obtains
		// authoritative content through the bounded conversation snapshot. Do
		// not resend event payloads, which may contain command output or diffs.
		// If the client is too far behind, force a snapshot instead of returning
		// an unbounded replay from the in-memory/store projection.
		if !cursorValid || len(events) > mobileSubscriptionEventLimit {
			events = make([]protocol.Event, 0)
			cursorValid = false
		} else {
			metadata := make([]protocol.Event, 0, len(events))
			for _, event := range events {
				event.Payload = nil
				metadata = append(metadata, event)
			}
			events = metadata
		}
	}
	result := map[string]any{"threadId": projection.ID, "status": projection.Status, "revision": projection.Revision, "sequence": projection.Sequence, "syncEpoch": syncEpoch, "events": events, "snapshot": !cursorValid}
	i.mu.Unlock()
	return result
}

func (i *Instance) ConversationVersion(threadID string) (revision, sequence int64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	projection := i.projectionLocked(threadID)
	return projection.Revision, projection.Sequence
}

func (i *Instance) Snapshot() InstanceSnapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	return InstanceSnapshot{
		ID: i.descriptor.ID, Name: i.descriptor.Name, CodexHome: i.descriptor.CodexHome,
		UserDataDir: i.descriptor.UserDataDir, State: i.state, Runtime: i.runtime,
		PendingRuntime: i.pendingRuntime, DesktopConnections: len(i.desktops), ActiveTurn: len(i.activeTurns) > 0,
		PendingActions: i.pendingUserActionCountLocked(), LastError: i.lastError, LastExitCode: i.lastExitCode,
	}
}

func (i *Instance) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return i.call(ctx, method, params, true)
}

func (i *Instance) callWithoutSettingsProjection(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return i.call(ctx, method, params, false)
}

func (i *Instance) commitThreadSettings(params json.RawMessage) {
	i.observeOutgoingThreadSettings("thread/settings/update", params)
	i.broadcastEvent("frontend.thread.settings.updated", map[string]any{"threadId": conversationIDFromPayload(json.RawMessage(params))})
}

func (i *Instance) call(ctx context.Context, method string, params json.RawMessage, observeSettings bool) (json.RawMessage, error) {
	i.mu.Lock()
	if i.stdin == nil {
		i.mu.Unlock()
		i.clearPendingTurnSettings(method, params)
		return nil, errors.New("App Server is not running")
	}
	createdBeforeStart := method == "turn/start" && i.createdThreads[conversationIDFromPayload(json.RawMessage(params))]
	i.nextRequest++
	id := fmt.Sprintf("wa:c:local:%d", i.nextRequest)
	response := make(chan []byte, 1)
	i.requests[string(mustJSON(id))] = requestOrigin{method: method, internal: true, response: response, startedAt: time.Now(), settingsContext: i.threadSettingsRequestLocked(method, params)}
	i.mu.Unlock()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if len(bytes.TrimSpace(params)) > 0 && !bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		request["params"] = json.RawMessage(params)
	}
	raw, _ := json.Marshal(request)
	if len(raw)+1 > ipc.DefaultMaxFrame {
		i.removeRequest(string(mustJSON(id)))
		i.clearPendingTurnSettings(method, params)
		return nil, &mobileContractError{code: "too_large", message: "serialized App Server request exceeds 64 MiB"}
	}
	if observeSettings {
		i.observeOutgoingThreadSettings(method, params)
	}
	if method == "turn/start" {
		i.broadcastEvent("frontend.turn.start", map[string]any{"params": json.RawMessage(params), "source": "control"})
	}
	i.manager.traffic.Record(i.descriptor.ID, "control_to_server", raw)
	requestKey := string(mustJSON(id))
	started := time.Now()
	i.mu.Lock()
	if origin, ok := i.requests[requestKey]; ok {
		started = origin.startedAt
	}
	i.mu.Unlock()
	if err := i.writeServer(append(raw, '\n')); err != nil {
		i.abandonRequest(requestKey)
		i.logRequestFailure(requestKey, method, err.Error(), started)
		i.clearPendingTurnSettings(method, params)
		return nil, err
	}
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case rawResponse := <-response:
		var message jsonrpc.Message
		if err := json.Unmarshal(rawResponse, &message); err != nil {
			return nil, err
		}
		if len(message.Error) > 0 && !bytes.Equal(bytes.TrimSpace(message.Error), []byte("null")) {
			i.clearPendingTurnSettings(method, params)
			if createdBeforeStart {
				i.rememberCreatedThread(conversationIDFromPayload(json.RawMessage(params)))
			}
			return nil, fmt.Errorf("App Server: %s", message.Error)
		}
		if method == "turn/start" {
			threadID := conversationIDFromPayload(json.RawMessage(params))
			var result struct {
				Turn struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(message.Result, &result)
			i.mu.Lock()
			reserved := i.pendingTurnStarts[threadID]
			if i.pendingTurnStarts[threadID] && result.Turn.ID != "" {
				i.activeTurns[threadID] = result.Turn.ID
				delete(i.pendingTurnStarts, threadID)
				delete(i.createdThreads, threadID)
			}
			i.mu.Unlock()
			if reserved && result.Turn.ID == "" {
				i.clearPendingTurnSettings(method, params)
				return nil, errors.New("turn/start response has no turn id")
			}
		}
		return append(json.RawMessage(nil), message.Result...), nil
	case <-ctx.Done():
		i.abandonRequest(requestKey)
		i.logRequestFailure(requestKey, method, ctx.Err().Error(), started)
		i.clearPendingTurnSettings(method, params)
		return nil, ctx.Err()
	case <-timer.C:
		i.abandonRequest(requestKey)
		i.logRequestFailure(requestKey, method, "App Server request timed out", started)
		i.clearPendingTurnSettings(method, params)
		return nil, errors.New("App Server request timed out")
	}
}

func (i *Instance) Actions() []pendingAction {
	i.mu.Lock()
	defer i.mu.Unlock()
	result := make([]pendingAction, 0, len(i.actions))
	for _, action := range i.actions {
		if requiresUserAction(action) && !action.Resolved {
			result = append(result, *action)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return string(result[left].ID) < string(result[right].ID)
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result
}

func (i *Instance) CanStartTurn(threadID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.canStartTurnLocked(threadID)
}

// CanSubmitQueue allows queue changes while a turn is active, but keeps the
// same transport and blocking-action gate used by other conversation writes.
func (i *Instance) CanSubmitQueue(threadID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stdin == nil || threadID == "" || i.state == "starting" || i.state == "waiting_initialize" || i.state == "stopped" || i.state == "error" || i.state == "upgrade_pending" {
		return false
	}
	for _, action := range i.actions {
		if actionBlocksThread(action, threadID) {
			return false
		}
	}
	return true
}

// CanUpdateSettings allows changing settings for subsequent turns while a
// thread is active, but keeps the transport and blocking-action gate used by
// other conversation writes.
func (i *Instance) CanUpdateSettings(threadID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stdin == nil || threadID == "" || i.state == "starting" || i.state == "waiting_initialize" || i.state == "stopped" || i.state == "error" || i.state == "upgrade_pending" {
		return false
	}
	for _, action := range i.actions {
		if actionBlocksThread(action, threadID) {
			return false
		}
	}
	return true
}

func (i *Instance) updateActivityStateLocked() {
	if i.state == "starting" || i.state == "waiting_initialize" || i.state == "stopped" || i.state == "error" || i.state == "upgrade_pending" {
		return
	}
	if i.pendingUserActionCountLocked() > 0 {
		i.state = "waiting_action"
	} else if len(i.activeTurns) > 0 {
		i.state = "active"
	} else {
		i.state = "ready"
	}
}

func (i *Instance) ResolveAction(id, result json.RawMessage) error {
	key := string(id)
	i.mu.Lock()
	action := i.actions[key]
	if action == nil || action.Resolved {
		i.mu.Unlock()
		return errors.New("action is no longer pending")
	}
	if !requiresUserAction(action) {
		i.mu.Unlock()
		return errors.New("request cannot be resolved as a user action")
	}
	action.Resolved = true
	delete(i.actions, key)
	i.updateActivityStateLocked()
	i.mu.Unlock()
	response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": json.RawMessage(result)}
	raw, _ := json.Marshal(response)
	i.manager.traffic.Record(i.descriptor.ID, "control_to_server", raw)
	if err := i.writeServer(append(raw, '\n')); err != nil {
		return err
	}
	i.broadcastEvent("action.resolved", map[string]any{"id": json.RawMessage(id), "threadId": action.ConversationID})
	return nil
}

func environmentList(values []string, realExecutable string) []string {
	return codex.ChildEnvironment(values, realExecutable)
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
	if thread, ok := object["thread"].(map[string]any); ok {
		if id, ok := thread["id"].(string); ok {
			return id
		}
	}
	if params, ok := object["params"].(map[string]any); ok {
		if thread, ok := params["thread"].(map[string]any); ok {
			if id, ok := thread["id"].(string); ok {
				return id
			}
		}
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
	case strings.Contains(lower, "turn/failed") || lower == "frontend.thread.state.reset":
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
	var firstParams, secondParams struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(firstMessage.Params, &firstParams) != nil || json.Unmarshal(secondMessage.Params, &secondParams) != nil {
		return false
	}
	normalize := func(raw json.RawMessage) any {
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return map[string]any{}
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return nil
		}
		return value
	}
	return reflect.DeepEqual(normalize(firstParams.Capabilities), normalize(secondParams.Capabilities))
}
