package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/logging"
	"github.com/hurf/work-anywhere/desktop/internal/platform/identity"
	"github.com/hurf/work-anywhere/desktop/internal/platform/localipc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/platform/tray"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/remote"
	"github.com/hurf/work-anywhere/desktop/internal/store"
	"github.com/hurf/work-anywhere/desktop/internal/traffic"
)

type Manager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	version        string
	startedAt      time.Time
	listener       net.Listener
	job            *platformprocess.Group
	admin          *adminServer
	stopTray       func()
	store          *store.Store
	mu             sync.RWMutex
	instances      map[string]*Instance
	nextConn       atomic.Uint64
	stopping       atomic.Bool
	controlPaused  atomic.Bool
	eventMu        sync.Mutex
	subscribers    map[uint64]chan protocol.Event
	nextSubscriber uint64
	uploadMu       sync.Mutex
	uploads        map[string]*attachmentUpload
	contentRefMu   sync.Mutex
	contentRefs    map[string]mobileContentRef
	traffic        *traffic.Recorder
	remote         *remote.Service
	log            *logging.Logger
}

type attachmentUpload struct {
	ID, InstanceID, ProjectID, ConversationID, MessageID string
	MimeType, FileName, Path, TemporaryPath              string
	ExpectedSize, ReceivedSize                           int64
}

const maxAttachmentBytes = 48 << 20
const remoteAttachmentDirectory = ".work-anywhere-remote-attachments"
const staleAttachmentPartAge = 24 * time.Hour

func Run(ctx context.Context, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sid, err := identity.CurrentUser()
	if err != nil {
		return err
	}
	lease, already, err := identity.AcquireLease(identity.DaemonMutexName(sid))
	if err != nil {
		return err
	}
	if already {
		lease.Close()
		return errors.New("daemon is already running")
	}
	defer lease.Close()

	dataDir, err := paths.DataDir()
	if err != nil {
		return err
	}
	for _, dir := range []string{dataDir, filepath.Join(dataDir, "run"), filepath.Join(dataDir, "logs")} {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	listener, err := localipc.ListenPipe(paths.LocalEndpoint(sid), sid)
	if err != nil {
		return fmt.Errorf("listen management pipe: %w", err)
	}
	job, err := platformprocess.NewGroup()
	if err != nil {
		listener.Close()
		return fmt.Errorf("create child process job: %w", err)
	}
	stateStore, err := store.Open(filepath.Join(dataDir, "state", "work-anywhere.db"), filepath.Join(dataDir, "state", "master.dpapi"))
	if err != nil {
		listener.Close()
		job.Close()
		return fmt.Errorf("open state store: %w", err)
	}
	logger, err := logging.New(filepath.Join(dataDir, "logs", "daemon.ndjson"))
	if err != nil {
		listener.Close()
		job.Close()
		stateStore.Close()
		return err
	}
	m := &Manager{ctx: ctx, cancel: cancel, version: version, startedAt: time.Now(), listener: listener, job: job, store: stateStore, instances: make(map[string]*Instance), subscribers: make(map[uint64]chan protocol.Event), uploads: make(map[string]*attachmentUpload), contentRefs: make(map[string]mobileContentRef), traffic: traffic.New(filepath.Join(dataDir, "logs", "traffic")), log: logger}
	remoteService, err := remote.New(stateStore, m.control, m.subscribe, logger)
	if err != nil {
		listener.Close()
		job.Close()
		stateStore.Close()
		return fmt.Errorf("start remote connector: %w", err)
	}
	m.remote = remoteService
	go remoteService.Run(ctx)
	m.log.Event("daemon.started", map[string]any{"version": version, "pid": os.Getpid()})
	admin, err := startAdmin(m)
	if err != nil {
		listener.Close()
		job.Close()
		stateStore.Close()
		return fmt.Errorf("start management page: %w", err)
	}
	m.admin = admin
	stopTray, err := tray.Start(m)
	if err != nil {
		admin.Close()
		listener.Close()
		job.Close()
		stateStore.Close()
		return fmt.Errorf("start tray icon: %w", err)
	}
	m.stopTray = stopTray
	defer m.close()
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return acceptErr
		}
		go m.handleConnection(conn)
	}
}

func (m *Manager) close() {
	if !m.stopping.CompareAndSwap(false, true) {
		return
	}
	_ = m.listener.Close()
	if m.admin != nil {
		_ = m.admin.Close()
	}
	if m.stopTray != nil {
		m.stopTray()
	}
	m.abortAttachmentUploads()
	if m.store != nil {
		_ = m.store.Close()
	}
	m.mu.RLock()
	instances := make([]*Instance, 0, len(m.instances))
	for _, instance := range m.instances {
		instances = append(instances, instance)
	}
	m.mu.RUnlock()
	for _, instance := range instances {
		instance.Stop(false)
	}
	_ = m.job.Close()
	m.log.Event("daemon.stopped", map[string]any{"pid": os.Getpid()})
}

func (m *Manager) handleConnection(raw net.Conn) {
	conn := &clientConn{id: fmt.Sprintf("%016x", m.nextConn.Add(1)), conn: raw}
	defer conn.Close()
	_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	frame, err := ipc.ReadFrame(raw)
	_ = raw.SetReadDeadline(time.Time{})
	if err != nil || frame.Kind != ipc.KindHello {
		return
	}
	var hello protocol.Hello
	if json.Unmarshal(frame.Metadata, &hello) != nil {
		return
	}
	var bridgeInstance *Instance
	if hello.Role == "bridge" {
		bridgeInstance, err = m.registerInstance(hello)
		if err != nil {
			_ = conn.Write(ipc.Frame{Kind: ipc.KindError, Payload: []byte(err.Error())})
			return
		}
	}
	ack := protocol.HelloAck{ConnectionID: conn.id, DaemonVersion: m.version, InstanceID: hello.Instance.ID}
	if err = conn.Write(ipc.Frame{Kind: ipc.KindHelloAck, Metadata: ipc.Metadata(ack)}); err != nil {
		return
	}
	switch hello.Role {
	case "bridge":
		m.handleBridge(conn, bridgeInstance)
	case "control":
		m.handleControl(conn)
	}
}

func (m *Manager) handleBridge(conn *clientConn, instance *Instance) {
	if instance == nil {
		_ = conn.Write(ipc.Frame{Kind: ipc.KindError, Payload: []byte("invalid bridge registration")})
		return
	}
	instance.AddDesktop(conn)
	defer instance.RemoveDesktop(conn.id)
	for {
		frame, readErr := ipc.ReadFrame(conn.conn)
		if readErr != nil {
			return
		}
		switch frame.Kind {
		case ipc.KindDesktopFrame:
			if err := instance.HandleDesktop(conn, frame.Payload); err != nil {
				_ = conn.Write(ipc.Frame{Kind: ipc.KindError, Payload: []byte(err.Error())})
				return
			}
		case ipc.KindDesktopEOF:
			return
		case ipc.KindPing:
			_ = conn.Write(ipc.Frame{Kind: ipc.KindPong})
		}
	}
}

func (m *Manager) registerInstance(hello protocol.Hello) (*Instance, error) {
	if hello.Instance.ID == "" || hello.Runtime.RealExecutable == "" {
		return nil, errors.New("invalid bridge registration")
	}
	m.mu.Lock()
	instance := m.instances[hello.Instance.ID]
	if instance == nil {
		instance = NewInstance(m, hello)
		m.instances[hello.Instance.ID] = instance
	}
	m.mu.Unlock()
	if err := instance.RegisterRuntime(hello); err != nil {
		m.log.Event("instance.registration_failed", map[string]any{"instanceId": hello.Instance.ID, "error": err.Error()})
		return nil, err
	}
	m.log.Event("instance.registered", map[string]any{"instanceId": hello.Instance.ID, "version": hello.Runtime.Version, "runtimePath": hello.Runtime.RealExecutable})
	_ = m.store.UpsertInstance(store.InstanceRecord{
		ID: hello.Instance.ID, Name: hello.Instance.Name, CodexHome: hello.Instance.CodexHome,
		UserDataDir: hello.Instance.UserDataDir, State: instance.Snapshot().State,
		RuntimePath: hello.Runtime.RealExecutable, RuntimeVersion: hello.Runtime.Version, RuntimeSHA256: hello.Runtime.SHA256,
		ResourcesDir: hello.Runtime.ResourcesDir, DiscoveredBy: hello.Runtime.DiscoveredBy,
	})
	return instance, nil
}

func (m *Manager) handleControl(conn *clientConn) {
	for {
		frame, err := ipc.ReadFrame(conn.conn)
		if err != nil {
			return
		}
		if frame.Kind != ipc.KindControlRequest {
			continue
		}
		var request protocol.ControlRequest
		if json.Unmarshal(frame.Payload, &request) != nil {
			continue
		}
		response := m.control(request)
		data, _ := json.Marshal(response)
		if conn.Write(ipc.Frame{Kind: ipc.KindControlResponse, Payload: data}) != nil {
			return
		}
	}
}

func (m *Manager) control(request protocol.ControlRequest) (response protocol.ControlResponse) {
	started := time.Now()
	response = protocol.ControlResponse{Type: "response", ID: request.ID, Contract: request.Contract}
	defer func() {
		if request.Contract != protocol.MobileControlContract {
			return
		}
		encoded, _ := json.Marshal(response)
		if len(encoded) > mobileControlResponseBytes {
			response.Result = nil
			response.Error = &protocol.ControlError{Code: "response_too_large", Message: "control response exceeds 512 KiB"}
			encoded, _ = json.Marshal(response)
		}
		if m.log != nil {
			code := ""
			if response.Error != nil {
				code = response.Error.Code
			}
			m.log.Event("mobile.control", map[string]any{"method": request.Method, "durationMs": time.Since(started).Milliseconds(), "responseBytes": len(encoded), "errorCode": code})
		}
	}()
	if request.Contract != "" && request.Contract != protocol.MobileControlContract {
		response.Error = &protocol.ControlError{Code: "unsupported_contract", Message: "unsupported control contract: " + request.Contract}
		return response
	}
	if m.controlPaused.Load() && isMutatingControlMethod(request.Method) {
		response.Error = &protocol.ControlError{Code: "control_paused", Message: "local control is paused from the tray menu"}
		return response
	}
	switch request.Method {
	case "system.status":
		response.Result = map[string]any{"version": m.version, "pid": os.Getpid(), "uptimeSeconds": int64(time.Since(m.startedAt).Seconds()), "controlPaused": m.controlPaused.Load(), "instances": m.instanceSnapshots(), "remote": m.remote.Status()}
	case "system.ui_url":
		response.Result = map[string]string{"url": m.admin.OneTimeURL()}
	case "system.control.pause":
		m.controlPaused.Store(true)
		response.Result = map[string]any{"paused": true}
	case "system.control.resume":
		m.controlPaused.Store(false)
		response.Result = map[string]any{"paused": false}
	case "instance.list":
		response.Result = m.instanceSnapshots()
	case "agent.list":
		if request.Contract != protocol.MobileControlContract {
			response.Error = &protocol.ControlError{Code: "contract_required", Message: "agent.list requires mobile-control-v1"}
		} else {
			response.Result = m.mobileAgents()
		}
	case "instance.get":
		var params struct {
			InstanceID string `json:"instanceId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		m.mu.RLock()
		instance := m.instances[params.InstanceID]
		m.mu.RUnlock()
		if instance == nil {
			response.Error = &protocol.ControlError{Code: "not_found", Message: "instance not found"}
		} else {
			response.Result = instance.Snapshot()
		}
	case "instance.restart":
		var params struct {
			InstanceID string `json:"instanceId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		m.mu.RLock()
		instance := m.instances[params.InstanceID]
		m.mu.RUnlock()
		if instance == nil {
			response.Error = &protocol.ControlError{Code: "not_found", Message: "instance not found"}
		} else if err := instance.Restart(); err != nil {
			response.Error = &protocol.ControlError{Code: "restart_failed", Message: err.Error()}
		} else {
			response.Result = instance.Snapshot()
		}
	case "project.list":
		if request.Contract != protocol.MobileControlContract {
			response.Error = &protocol.ControlError{Code: "contract_required", Message: "project.list requires mobile-control-v1"}
			break
		}
		instance, params, err := m.mobileControlTarget(request.Params)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		result, callErr := m.mobileProjectList(instance, params)
		if callErr != nil {
			response.Error = &protocol.ControlError{Code: "provider_error", Message: callErr.Error()}
		} else {
			response.Result = result
		}
	case "conversation.read", "conversation.timeline.list", "conversation.content.read", "conversation.content.resolve":
		if request.Contract != protocol.MobileControlContract {
			response.Error = &protocol.ControlError{Code: "contract_required", Message: request.Method + " requires mobile-control-v1"}
			break
		}
		instance, params, err := m.mobileControlTarget(request.Params)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		var result any
		switch request.Method {
		case "conversation.read":
			result, err = m.mobileConversationRead(instance, params)
		case "conversation.timeline.list":
			result, err = m.mobileTimelineList(instance, params)
		case "conversation.content.read":
			result, err = m.mobileContentRead(instance, params)
		case "conversation.content.resolve":
			result, err = m.mobileContentResolve(instance, params)
		}
		if err != nil {
			response.Error = mobileControlError(err)
		} else {
			response.Result = result
		}
	case "conversation.create":
		if request.Contract != protocol.MobileControlContract {
			response.Error = &protocol.ControlError{Code: "contract_required", Message: "conversation.create requires mobile-control-v1"}
			break
		}
		instance, params, err := m.mobileControlTarget(request.Params)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		var requestParams struct {
			ProjectID string `json:"projectId"`
		}
		if json.Unmarshal(params, &requestParams) != nil || requestParams.ProjectID == "" {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "projectId is required"}
			break
		}
		result, callErr := instance.Call(m.ctx, "thread/start", params)
		if callErr != nil {
			response.Error = &protocol.ControlError{Code: "provider_error", Message: callErr.Error()}
			break
		}
		response.Result, callErr = normalizeConversationCreate(result)
		if callErr != nil {
			response.Error = &protocol.ControlError{Code: "invalid_provider_response", Message: callErr.Error()}
		}
	case "plan.respond":
		if request.Contract != protocol.MobileControlContract {
			response.Error = &protocol.ControlError{Code: "contract_required", Message: "plan.respond requires mobile-control-v1"}
			break
		}
		instance, params, err := m.mobileControlTarget(request.Params)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		result, respondErr := m.mobilePlanRespond(instance, instance.descriptor.ID, params)
		if respondErr != nil {
			response.Error = mobileControlError(respondErr)
		} else {
			response.Result = result
		}
	case "conversation.list", "turn.start", "turn.interrupt", "catalog.models":
		var instance *Instance
		var params json.RawMessage
		var err error
		if request.Contract == protocol.MobileControlContract {
			instance, params, err = m.mobileControlTarget(request.Params)
		} else {
			instance, params, err = m.controlTarget(request.Params)
		}
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		method := map[string]string{
			"conversation.list": "thread/list", "conversation.read": "thread/read",
			"turn.start": "turn/start", "turn.interrupt": "turn/interrupt", "catalog.models": "model/list",
		}[request.Method]
		if request.Method == "turn.start" {
			var turnTarget struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(params, &turnTarget)
			if !instance.CanStartTurn(turnTarget.ThreadID) {
				response.Error = &protocol.ControlError{Code: "conversation_busy", Message: "a turn is already active or waiting for an action"}
				break
			}
			if err = instance.CheckExpectedRevision(params); err != nil {
				response.Error = &protocol.ControlError{Code: "revision_conflict", Message: err.Error()}
				break
			}
			params, err = m.normalizeTurnStart(instance.descriptor.ID, params)
			if err != nil {
				response.Error = &protocol.ControlError{Code: "invalid_turn", Message: err.Error()}
				break
			}
		}
		var result json.RawMessage
		var callErr error
		if request.Contract == protocol.MobileControlContract && request.Method == "conversation.list" {
			result, callErr = m.mobileConversationList(instance, params)
		} else {
			result, callErr = instance.Call(m.ctx, method, params)
		}
		if callErr != nil {
			response.Error = &protocol.ControlError{Code: "provider_error", Message: callErr.Error()}
		} else if request.Contract == protocol.MobileControlContract && request.Method == "conversation.list" {
			response.Result, callErr = normalizeConversationList(instance.descriptor.ID, result)
			if callErr != nil {
				response.Error = &protocol.ControlError{Code: "invalid_provider_response", Message: callErr.Error()}
			}
		} else {
			response.Result = result
		}
	case "action.list":
		var instance *Instance
		var err error
		if request.Contract == protocol.MobileControlContract {
			instance, _, err = m.mobileControlTarget(request.Params)
		} else {
			instance, _, err = m.controlTarget(request.Params)
		}
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
		} else {
			response.Result = instance.Actions()
		}
	case "action.resolve":
		var instance *Instance
		var params json.RawMessage
		var err error
		if request.Contract == protocol.MobileControlContract {
			instance, params, err = m.mobileControlTarget(request.Params)
		} else {
			instance, params, err = m.controlTarget(request.Params)
		}
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		var action struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(params, &action) != nil || len(action.ID) == 0 {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "id and result are required"}
			break
		}
		if err = instance.ResolveAction(action.ID, action.Result); err != nil {
			response.Error = &protocol.ControlError{Code: "already_resolved", Message: err.Error()}
		} else {
			response.Result = map[string]any{"resolved": true}
		}
	case "catalog.permissionProfiles":
		var instance *Instance
		var params json.RawMessage
		var err error
		if request.Contract == protocol.MobileControlContract {
			instance, params, err = m.mobileControlTarget(request.Params)
		} else {
			instance, params, err = m.controlTarget(request.Params)
		}
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		result, callErr := instance.Call(m.ctx, "permissionProfile/list", params)
		if callErr != nil {
			response.Error = &protocol.ControlError{Code: "provider_error", Message: callErr.Error()}
		} else {
			response.Result = result
		}
	case "conversation.subscribe":
		var instance *Instance
		var params json.RawMessage
		var err error
		if request.Contract == protocol.MobileControlContract {
			instance, params, err = m.mobileControlTarget(request.Params)
		} else {
			instance, params, err = m.controlTarget(request.Params)
		}
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
		} else {
			if request.Contract == protocol.MobileControlContract {
				response.Result = instance.MobileConversationSubscription(params)
			} else {
				response.Result = instance.ConversationSubscription(params)
			}
		}
	case "attachment.begin":
		rawParams := request.Params
		if request.Contract == protocol.MobileControlContract {
			instance, normalized, err := m.mobileControlTarget(request.Params)
			if err != nil {
				response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
				break
			}
			// mobileControlTarget returns provider parameters and intentionally
			// removes the routing target. Attachments are stored before the
			// provider call, so retain the resolved owner for the upload record.
			var object map[string]json.RawMessage
			if err = json.Unmarshal(normalized, &object); err != nil {
				response.Error = &protocol.ControlError{Code: "invalid_params", Message: err.Error()}
				break
			}
			object["instanceId"], _ = json.Marshal(instance.descriptor.ID)
			rawParams, _ = json.Marshal(object)
		}
		var params struct {
			InstanceID     string `json:"instanceId"`
			ProjectID      string `json:"projectId"`
			ConversationID string `json:"conversationId"`
			MessageID      string `json:"clientMessageId"`
			MimeType       string `json:"mimeType"`
			FileName       string `json:"fileName"`
			Size           int64  `json:"size"`
		}
		if json.Unmarshal(rawParams, &params) != nil || params.InstanceID == "" || params.MimeType == "" || params.ConversationID == "" || params.MessageID == "" {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "target, conversationId, clientMessageId, and mimeType are required"}
			break
		}
		if params.Size < 0 || params.Size > maxAttachmentBytes {
			response.Error = &protocol.ControlError{Code: "too_large", Message: "attachment exceeds 48 MiB"}
			break
		}
		instance, err := m.instanceByID(params.InstanceID)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_target", Message: err.Error()}
			break
		}
		root, err := m.attachmentProjectRoot(instance, params.ProjectID, params.ConversationID)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "project_not_resolved", Message: err.Error()}
			break
		}
		if err = validateAttachmentSegment(params.ConversationID); err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "invalid conversationId"}
			break
		}
		if err = validateAttachmentSegment(params.MessageID); err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "invalid clientMessageId"}
			break
		}
		if err = validateAttachmentFileName(params.FileName); err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "invalid fileName"}
			break
		}
		id := randomToken(18)
		fileName := attachmentFileName(params.FileName)
		directory := filepath.Join(root, remoteAttachmentDirectory, params.ConversationID, params.MessageID)
		if err = os.MkdirAll(directory, 0o700); err != nil {
			response.Error = &protocol.ControlError{Code: "attachment_store_error", Message: err.Error()}
			break
		}
		cleanupStaleAttachmentParts(filepath.Join(root, remoteAttachmentDirectory))
		ext := filepath.Ext(fileName)
		finalPath := filepath.Join(directory, strings.TrimSuffix(fileName, ext)+"-"+id+ext)
		temporaryPath := filepath.Join(directory, "."+id+".part")
		file, createErr := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			response.Error = &protocol.ControlError{Code: "attachment_store_error", Message: createErr.Error()}
			break
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(temporaryPath)
			response.Error = &protocol.ControlError{Code: "attachment_store_error", Message: closeErr.Error()}
			break
		}
		m.uploadMu.Lock()
		m.uploads[id] = &attachmentUpload{ID: id, InstanceID: params.InstanceID, ProjectID: params.ProjectID, ConversationID: params.ConversationID, MessageID: params.MessageID, MimeType: params.MimeType, FileName: fileName, Path: finalPath, TemporaryPath: temporaryPath, ExpectedSize: params.Size}
		m.uploadMu.Unlock()
		response.Result = map[string]any{"attachmentId": id}
	case "attachment.chunk":
		var params struct{ AttachmentID, Data string }
		if json.Unmarshal(request.Params, &params) != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "invalid chunk"}
			break
		}
		chunk, decodeErr := base64.StdEncoding.DecodeString(params.Data)
		if decodeErr != nil {
			response.Error = &protocol.ControlError{Code: "invalid_base64", Message: decodeErr.Error()}
			break
		}
		m.uploadMu.Lock()
		upload := m.uploads[params.AttachmentID]
		tooLarge := upload != nil && (upload.ReceivedSize+int64(len(chunk)) > maxAttachmentBytes || upload.ReceivedSize+int64(len(chunk)) > upload.ExpectedSize)
		var writeErr error
		if upload != nil && !tooLarge {
			var file *os.File
			file, writeErr = os.OpenFile(upload.TemporaryPath, os.O_WRONLY|os.O_APPEND, 0o600)
			if writeErr == nil {
				_, writeErr = file.Write(chunk)
				if closeErr := file.Close(); writeErr == nil {
					writeErr = closeErr
				}
			}
			if writeErr == nil {
				upload.ReceivedSize += int64(len(chunk))
			}
		}
		if tooLarge || writeErr != nil {
			if upload != nil {
				delete(m.uploads, params.AttachmentID)
				_ = os.Remove(upload.TemporaryPath)
			}
		}
		m.uploadMu.Unlock()
		if upload == nil {
			response.Error = &protocol.ControlError{Code: "not_found", Message: "attachment upload not found"}
		} else if tooLarge {
			response.Error = &protocol.ControlError{Code: "too_large", Message: "attachment exceeds declared size or 48 MiB"}
		} else if writeErr != nil {
			response.Error = &protocol.ControlError{Code: "attachment_store_error", Message: writeErr.Error()}
		} else {
			response.Result = map[string]any{"received": upload.ReceivedSize}
		}
	case "attachment.commit":
		var params struct {
			AttachmentID string `json:"attachmentId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		m.uploadMu.Lock()
		upload := m.uploads[params.AttachmentID]
		delete(m.uploads, params.AttachmentID)
		m.uploadMu.Unlock()
		if upload == nil {
			response.Error = &protocol.ControlError{Code: "not_found", Message: "attachment upload not found"}
			break
		}
		if upload.ReceivedSize != upload.ExpectedSize {
			_ = os.Remove(upload.TemporaryPath)
			response.Error = &protocol.ControlError{Code: "size_mismatch", Message: "attachment size does not match declaration"}
			break
		}
		if err := os.Rename(upload.TemporaryPath, upload.Path); err != nil {
			_ = os.Remove(upload.TemporaryPath)
			response.Error = &protocol.ControlError{Code: "attachment_store_error", Message: err.Error()}
			break
		}
		if err := m.store.SaveAttachmentMetadata(store.Attachment{ID: upload.ID, InstanceID: upload.InstanceID, ProjectID: upload.ProjectID, ConversationID: upload.ConversationID, MessageID: upload.MessageID, MimeType: upload.MimeType, FileName: upload.FileName, Path: upload.Path, Size: upload.ReceivedSize}); err != nil {
			_ = os.Remove(upload.Path)
			response.Error = &protocol.ControlError{Code: "store_error", Message: err.Error()}
		} else {
			response.Result = map[string]any{"attachmentId": upload.ID, "size": upload.ReceivedSize}
		}
	case "attachment.abort":
		var params struct {
			AttachmentID string `json:"attachmentId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		m.uploadMu.Lock()
		upload := m.uploads[params.AttachmentID]
		delete(m.uploads, params.AttachmentID)
		m.uploadMu.Unlock()
		if upload == nil {
			response.Result = map[string]any{"aborted": false}
		} else {
			_ = os.Remove(upload.TemporaryPath)
			response.Result = map[string]any{"aborted": true}
		}
	case "traffic.enable", "traffic.disable":
		var params struct {
			InstanceID string `json:"instanceId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.InstanceID == "" {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "instanceId is required"}
			break
		}
		enabled := request.Method == "traffic.enable"
		m.traffic.Set(params.InstanceID, enabled)
		response.Result = map[string]any{"enabled": enabled, "directory": "logs/traffic/" + params.InstanceID}
	case "remote.status", "remote.pair.list":
		response.Result = m.remote.Status()
	case "remote.configure":
		var params struct {
			RelayURL string `json:"relayUrl"`
			Enabled  bool   `json:"enabled"`
		}
		if json.Unmarshal(request.Params, &params) != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "relayUrl and enabled are required"}
		} else if err := m.remote.Configure(params.RelayURL, params.Enabled); err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_remote_config", Message: err.Error()}
		} else {
			response.Result = m.remote.Status()
		}
	case "remote.enroll":
		var params struct {
			Token       string `json:"token"`
			DisplayName string `json:"displayName"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.Token == "" {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "token is required"}
		} else if err := m.remote.Enroll(m.ctx, params.Token, params.DisplayName); err != nil {
			response.Error = &protocol.ControlError{Code: "enrollment_failed", Message: err.Error()}
		} else {
			response.Result = m.remote.Status()
		}
	case "remote.pair.start":
		var params struct {
			TTLSeconds int64 `json:"ttlSeconds"`
		}
		_ = json.Unmarshal(request.Params, &params)
		attempt, err := m.remote.StartPairing(time.Duration(params.TTLSeconds) * time.Second)
		if err != nil {
			response.Error = &protocol.ControlError{Code: "pairing_failed", Message: err.Error()}
		} else {
			response.Result = attempt
		}
	case "remote.pair.resolve":
		var params struct {
			PairingID string   `json:"pairingId"`
			Approved  bool     `json:"approved"`
			Scopes    []string `json:"scopes"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.PairingID == "" {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: "pairingId is required"}
		} else if err := m.remote.ResolvePairing(params.PairingID, params.Approved, params.Scopes); err != nil {
			response.Error = &protocol.ControlError{Code: "pairing_failed", Message: err.Error()}
		} else {
			response.Result = map[string]any{"submitted": true}
		}
	case "remote.pair.cancel":
		var params struct {
			PairingID string `json:"pairingId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if err := m.remote.CancelPairing(params.PairingID); err != nil {
			response.Error = &protocol.ControlError{Code: "pairing_failed", Message: err.Error()}
		} else {
			response.Result = map[string]any{"cancelled": true}
		}
	case "remote.pair.revoke":
		var params struct {
			PairingID string `json:"pairingId"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if err := m.remote.RevokePairing(params.PairingID); err != nil {
			response.Error = &protocol.ControlError{Code: "revoke_failed", Message: err.Error()}
		} else {
			response.Result = map[string]any{"submitted": true}
		}
	case "remote.reconnect":
		m.remote.Reconnect()
		response.Result = map[string]any{"reconnecting": true}
	case "system.shutdown":
		active := 0
		for _, item := range m.instanceSnapshots() {
			if item.ActiveTurn {
				active++
			}
		}
		var params struct {
			Force bool `json:"force"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if active > 0 && !params.Force {
			response.Error = &protocol.ControlError{Code: "active_turns", Message: fmt.Sprintf("%d turn(s) are active", active)}
		} else {
			response.Result = map[string]any{"stopping": true, "activeTurns": active}
			go func() { time.Sleep(100 * time.Millisecond); m.gracefulShutdown(params.Force) }()
		}
	default:
		response.Error = &protocol.ControlError{Code: "method_not_found", Message: request.Method}
	}
	return response
}

func isMutatingControlMethod(method string) bool {
	return method == "conversation.create" || method == "plan.respond" || strings.HasPrefix(method, "turn.") || method == "action.resolve" || method == "instance.restart" || strings.HasPrefix(method, "attachment.") || strings.HasPrefix(method, "traffic.") || (strings.HasPrefix(method, "remote.") && method != "remote.status" && method != "remote.pair.list")
}

func (m *Manager) restartErroredInstances() int {
	m.mu.RLock()
	instances := make([]*Instance, 0)
	for _, instance := range m.instances {
		if instance.Snapshot().State == "error" {
			instances = append(instances, instance)
		}
	}
	m.mu.RUnlock()
	for _, instance := range instances {
		go instance.Restart()
	}
	return len(instances)
}

func (m *Manager) normalizeTurnStart(instanceID string, raw json.RawMessage) (json.RawMessage, error) {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	instance, err := m.instanceByID(instanceID)
	if err != nil {
		return nil, err
	}
	threadID, _ := params["threadId"].(string)
	if threadID == "" {
		return nil, errors.New("threadId is required")
	}
	if _, ok := params["input"]; !ok {
		inputs := make([]map[string]any, 0)
		if text, ok := params["text"].(string); ok && text != "" {
			inputs = append(inputs, map[string]any{"type": "text", "text": text})
		}
		if ids, ok := params["attachmentIds"].([]any); ok {
			for _, rawID := range ids {
				id, ok := rawID.(string)
				if !ok {
					continue
				}
				attachment, err := m.store.ReadAttachment(id)
				if err != nil {
					return nil, fmt.Errorf("attachment %s: %w", id, err)
				}
				if attachment.InstanceID != instanceID {
					return nil, errors.New("attachment belongs to another instance")
				}
				if !strings.HasPrefix(attachment.MimeType, "image/") {
					return nil, fmt.Errorf("unsupported_attachment_type: attachment %s is not an image", id)
				}
				if attachment.Path == "" {
					conversationID := attachment.ConversationID
					if conversationID == "" {
						conversationID = threadID
					}
					root, rootErr := m.attachmentProjectRoot(instance, attachment.ProjectID, conversationID)
					if rootErr != nil {
						return nil, fmt.Errorf("attachment %s: project_not_resolved: %w", id, rootErr)
					}
					legacyMessageID := "legacy-" + id
					if err = validateAttachmentSegment(conversationID); err != nil {
						return nil, fmt.Errorf("attachment %s: invalid conversation id", id)
					}
					legacyDirectory := filepath.Join(root, remoteAttachmentDirectory, conversationID, legacyMessageID)
					fileName := attachmentFileName(attachment.FileName)
					ext := filepath.Ext(fileName)
					finalPath := filepath.Join(legacyDirectory, strings.TrimSuffix(fileName, ext)+"-"+id+ext)
					attachment, err = m.store.MaterializeLegacyAttachment(id, finalPath, attachment.ProjectID, legacyMessageID)
					if err != nil {
						return nil, fmt.Errorf("attachment %s: migrate legacy content: %w", id, err)
					}
				}
				content, readErr := os.ReadFile(attachment.Path)
				if readErr != nil {
					return nil, fmt.Errorf("attachment %s: read file: %w", id, readErr)
				}
				inputs = append(inputs, map[string]any{"type": "image", "url": "data:" + attachment.MimeType + ";base64," + base64.StdEncoding.EncodeToString(content)})
			}
		}
		params["input"] = inputs
	}
	if effort, ok := params["reasoningEffort"]; ok {
		params["effort"] = effort
	}
	if messageID, ok := params["clientMessageId"]; ok {
		params["clientUserMessageId"] = messageID
	}
	for _, key := range []string{"text", "attachmentIds", "reasoningEffort", "clientMessageId", "expectedRevision"} {
		delete(params, key)
	}
	return json.Marshal(params)
}

func (m *Manager) instanceByID(instanceID string) (*Instance, error) {
	m.mu.RLock()
	instance := m.instances[instanceID]
	m.mu.RUnlock()
	if instance == nil {
		return nil, errors.New("instance not found")
	}
	return instance, nil
}

func (m *Manager) abortAttachmentUploads() {
	m.uploadMu.Lock()
	uploads := make([]*attachmentUpload, 0, len(m.uploads))
	for id, upload := range m.uploads {
		uploads = append(uploads, upload)
		delete(m.uploads, id)
	}
	m.uploadMu.Unlock()
	for _, upload := range uploads {
		_ = os.Remove(upload.TemporaryPath)
	}
}

func cleanupStaleAttachmentParts(root string) {
	cutoff := time.Now().Add(-staleAttachmentPartAge)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func (m *Manager) attachmentProjectRoot(instance *Instance, projectID, conversationID string) (string, error) {
	if projectID != "" {
		if root, err := m.projectRoot(instance, projectID); err == nil {
			if resolved, resolveErr := existingDirectory(root); resolveErr == nil {
				return resolved, nil
			}
		}
	}
	if conversationID != "" {
		params, _ := json.Marshal(map[string]any{"threadId": conversationID, "includeTurns": false})
		if raw, err := instance.Call(m.ctx, "thread/read", params); err == nil {
			var response struct {
				Thread struct {
					CWD string `json:"cwd"`
				} `json:"thread"`
			}
			if json.Unmarshal(raw, &response) == nil {
				if resolved, resolveErr := existingDirectory(response.Thread.CWD); resolveErr == nil {
					return resolved, nil
				}
			}
		}
	}
	instance.mu.Lock()
	cwd := instance.cwd
	instance.mu.Unlock()
	if resolved, err := existingDirectory(cwd); err == nil {
		return resolved, nil
	}
	return "", errors.New("project root, conversation cwd, and instance cwd are unavailable")
}

func existingDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("directory is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return abs, nil
}

func validateAttachmentSegment(value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return errors.New("path segment is unsafe")
	}
	for _, character := range value {
		if character < 0x20 {
			return errors.New("path segment contains a control character")
		}
	}
	return nil
}

func attachmentFileName(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	if value == "" || value == "." || value == ".." {
		return "attachment"
	}
	value = strings.NewReplacer("<", "_", ">", "_", ":", "_", `"`, "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_").Replace(value)
	value = strings.Trim(value, " .")
	if value == "" {
		return "attachment"
	}
	return value
}

func validateAttachmentFileName(value string) error {
	if value == "" {
		return nil
	}
	if value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return errors.New("file name must be a single path segment")
	}
	for _, character := range value {
		if character < 0x20 {
			return errors.New("file name contains a control character")
		}
	}
	return nil
}

func (m *Manager) subscribe() (<-chan protocol.Event, func()) {
	m.eventMu.Lock()
	m.nextSubscriber++
	id := m.nextSubscriber
	channel := make(chan protocol.Event, 128)
	m.subscribers[id] = channel
	m.eventMu.Unlock()
	return channel, func() {
		m.eventMu.Lock()
		if current := m.subscribers[id]; current != nil {
			delete(m.subscribers, id)
			close(current)
		}
		m.eventMu.Unlock()
	}
}

func (m *Manager) publish(event protocol.Event) {
	m.eventMu.Lock()
	for id, channel := range m.subscribers {
		select {
		case channel <- event:
		default:
			delete(m.subscribers, id)
			close(channel)
		}
	}
	m.eventMu.Unlock()
}

func (m *Manager) gracefulShutdown(interrupt bool) {
	if interrupt {
		m.mu.RLock()
		instances := make([]*Instance, 0, len(m.instances))
		for _, instance := range m.instances {
			instances = append(instances, instance)
		}
		m.mu.RUnlock()
		for _, instance := range instances {
			if instance.Snapshot().ActiveTurn {
				go instance.InterruptActive()
			}
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			active := false
			for _, instance := range instances {
				if instance.Snapshot().ActiveTurn {
					active = true
					break
				}
			}
			if !active {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	m.cancel()
}

func (m *Manager) controlTarget(raw json.RawMessage) (*Instance, json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, nil, err
	}
	var instanceID string
	_ = json.Unmarshal(object["instanceId"], &instanceID)
	if instanceID == "" {
		m.mu.RLock()
		if len(m.instances) == 1 {
			for id := range m.instances {
				instanceID = id
			}
		}
		m.mu.RUnlock()
	}
	m.mu.RLock()
	instance := m.instances[instanceID]
	m.mu.RUnlock()
	if instance == nil {
		return nil, nil, errors.New("instance not found or instanceId is ambiguous")
	}
	delete(object, "instanceId")
	params, _ := json.Marshal(object)
	return instance, params, nil
}

func (m *Manager) instanceSnapshots() []InstanceSnapshot {
	m.mu.RLock()
	items := make([]*Instance, 0, len(m.instances))
	for _, instance := range m.instances {
		items = append(items, instance)
	}
	m.mu.RUnlock()
	result := make([]InstanceSnapshot, 0, len(items))
	for _, instance := range items {
		result = append(result, instance.Snapshot())
	}
	return result
}

type clientConn struct {
	id     string
	conn   net.Conn
	mu     sync.Mutex
	closed atomic.Bool
}

func (c *clientConn) Write(frame ipc.Frame) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := ipc.WriteFrame(c.conn, frame)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *clientConn) Close() error {
	c.closed.Store(true)
	return c.conn.Close()
}
