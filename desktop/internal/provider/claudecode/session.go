package claudecode

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

const (
	actionKindApproval  = "approval"
	actionKindUserInput = "userInput"
	actionKindPlan      = "plan"
	interruptGrace      = 10 * time.Second
	stopGrace           = 5 * time.Second
	declinedMessage     = "The user declined this request from Work Anywhere Mobile."
	defaultReviseText   = "Please revise the plan."
)

// sessionProcess is one headless Claude Code process serving a single
// conversation. Mutable state is guarded by the owning Backend's mutex;
// stdin writes are serialized by writeMu so no lock is held across I/O.
type sessionProcess struct {
	backend  *Backend
	id       string
	cwd      string
	settings provider.ConversationSettings
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	writeMu  sync.Mutex
	done     chan struct{}
	stderr   *diagnosticTail

	turnID       string
	turnActive   bool
	interrupting bool
	lastUsed     time.Time
	requests     map[string]*pendingRequest
	exited       bool
}

type pendingRequest struct {
	requestID string
	actionID  string
	kind      string
	toolName  string
	toolUseID string
	turnID    string
	input     json.RawMessage
	questions []provider.ActionQuestion
	detail    *provider.ActionDetail
	createdAt time.Time
}

// streamLine is the subset of a stream-json stdout message the session uses.
type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	IsError         bool            `json:"is_error"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Event           json.RawMessage `json:"event"`
	APIMessageID    string          `json:"api_message_id"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
}

type streamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

type toolPermissionRequest struct {
	Subtype     string          `json:"subtype"`
	ToolName    string          `json:"tool_name"`
	Input       json.RawMessage `json:"input"`
	Description string          `json:"description"`
	BlockedPath string          `json:"blocked_path"`
	ToolUseID   string          `json:"tool_use_id"`
}

// startProcessLocked launches the CLI for a conversation. The caller holds
// b.mu. resume selects --resume for conversations that already have a
// transcript and --session-id for new ones.
func (b *Backend) startProcessLocked(id, cwd string, resume bool, settings provider.ConversationSettings) (*sessionProcess, error) {
	if b.runtime.Executable == "" {
		return nil, provider.NewError("runtime_unavailable", "Claude Code CLI is not available: "+b.runtimeError)
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return nil, provider.NewError("project_not_resolved", "project directory is not accessible")
	}
	cmd := exec.Command(b.runtime.Executable, sessionArgs(id, resume, settings)...)
	cmd.Dir = cwd
	cmd.Env = childEnvironment(os.Environ())
	configureChild(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &diagnosticTail{}
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		return nil, provider.NewError("runtime_unavailable", b.processFailure("start", cmd, err, stderr).Error())
	}
	if b.attach != nil {
		if err = b.attach(cmd); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = stdin.Close()
			return nil, b.processFailure("attach", cmd, err, stderr)
		}
	}
	process := &sessionProcess{backend: b, id: id, cwd: cwd, settings: settings, cmd: cmd, stdin: stdin, stderr: stderr, done: make(chan struct{}), lastUsed: time.Now(), requests: make(map[string]*pendingRequest)}
	b.lastProcessError = ""
	b.processes[id] = process
	go process.read(stdout)
	// The initialize request declares this host as the approval surface;
	// without it the CLI denies every prompt and hides question tools.
	if err = process.write(map[string]any{"type": "control_request", "request_id": "wa-init-" + randomHex(6), "request": map[string]any{"subtype": "initialize"}}); err != nil {
		process.kill()
		return nil, err
	}
	return process, nil
}

func (p *sessionProcess) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

func (p *sessionProcess) kill() {
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// stop closes stdin so the CLI can exit cleanly, then kills it after a grace
// period.
func (p *sessionProcess) stop() {
	_ = p.stdin.Close()
	go func() {
		select {
		case <-p.done:
		case <-time.After(stopGrace):
			p.kill()
		}
	}()
}

func (p *sessionProcess) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 256<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '{' {
			var message streamLine
			if json.Unmarshal(trimmed, &message) == nil {
				p.handle(message)
			}
		}
		if err != nil {
			break
		}
	}
	err := p.cmd.Wait()
	p.backend.mu.Lock()
	if err != nil {
		if p.backend.processes[p.id] == p {
			_ = p.backend.processFailure("exit", p.cmd, err, p.stderr)
		} else {
			p.backend.processDiagnostic("exit", p.cmd, err)
		}
	} else {
		p.backend.processDiagnostic("exit", p.cmd, nil)
	}
	p.backend.mu.Unlock()
	p.exit()
	close(p.done)
}

func (p *sessionProcess) handle(message streamLine) {
	b := p.backend
	switch message.Type {
	case "stream_event":
		if message.ParentToolUseID != nil && *message.ParentToolUseID != "" {
			return
		}
		var event streamEvent
		if json.Unmarshal(message.Event, &event) != nil || event.Type != "content_block_delta" || message.APIMessageID == "" {
			return
		}
		kind, text := "", ""
		switch event.Delta.Type {
		case "text_delta":
			kind, text = provider.DeltaAssistantText, event.Delta.Text
		case "thinking_delta":
			kind, text = provider.DeltaReasoning, event.Delta.Thinking
		}
		if kind == "" || text == "" {
			return
		}
		b.mu.Lock()
		turnID := p.turnID
		b.mu.Unlock()
		b.emit(provider.Event{Kind: provider.EventDelta, ConversationID: p.id, TurnID: turnID, ItemID: assistantItemID(message.APIMessageID, event.Index), DeltaKind: kind, Delta: text})
	case "assistant", "user":
		if message.ParentToolUseID != nil && *message.ParentToolUseID != "" {
			return
		}
		b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: p.id})
	case "control_request":
		p.handleControlRequest(message)
	case "result":
		p.finishTurn(message)
	}
}

func (p *sessionProcess) handleControlRequest(message streamLine) {
	var request toolPermissionRequest
	if json.Unmarshal(message.Request, &request) != nil || request.Subtype != "can_use_tool" {
		// Hooks, MCP elicitation and other host callbacks are not offered
		// by this host; answer so the CLI does not wait forever.
		_ = p.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": message.RequestID, "error": "not supported by Work Anywhere"}})
		return
	}
	b := p.backend
	pending := &pendingRequest{requestID: message.RequestID, actionID: "cc-" + randomHex(12), toolName: request.ToolName, toolUseID: request.ToolUseID, input: request.Input, createdAt: time.Now()}
	switch request.ToolName {
	case "AskUserQuestion":
		pending.kind = actionKindUserInput
		pending.questions = questionsFromInput(request.Input)
	case "ExitPlanMode":
		pending.kind = actionKindPlan
	default:
		pending.kind = actionKindApproval
		pending.detail = approvalDetail(request)
	}
	b.mu.Lock()
	pending.turnID = p.turnID
	p.requests[pending.actionID] = pending
	b.mu.Unlock()
	b.emit(provider.Event{Kind: provider.EventActionRequested, ConversationID: p.id, TurnID: pending.turnID})
}

func (p *sessionProcess) finishTurn(message streamLine) {
	b := p.backend
	b.mu.Lock()
	turnID := p.turnID
	active := p.turnActive
	interrupted := p.interrupting
	p.turnActive, p.interrupting = false, false
	p.lastUsed = time.Now()
	stale := p.clearRequestsLocked()
	b.mu.Unlock()
	if !active {
		return
	}
	kind := provider.EventTurnCompleted
	switch {
	case interrupted:
		kind = provider.EventTurnInterrupted
	case message.IsError || (message.Subtype != "" && message.Subtype != "success"):
		kind = provider.EventTurnFailed
	}
	if stale {
		b.emit(provider.Event{Kind: provider.EventActionResolved, ConversationID: p.id, TurnID: turnID})
	}
	b.emit(provider.Event{Kind: kind, ConversationID: p.id, TurnID: turnID})
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: p.id})
	// The list running indicator changes with the turn state.
	b.emit(provider.Event{Kind: provider.EventChanged})
}

// exit runs once the process has ended. An active turn fails and pending
// requests are dropped because nothing can answer them any more.
func (p *sessionProcess) exit() {
	b := p.backend
	b.mu.Lock()
	p.exited = true
	if b.processes[p.id] == p {
		delete(b.processes, p.id)
	}
	turnID, active, interrupted := p.turnID, p.turnActive, p.interrupting
	p.turnActive = false
	stale := p.clearRequestsLocked()
	b.mu.Unlock()
	if stale {
		b.emit(provider.Event{Kind: provider.EventActionResolved, ConversationID: p.id, TurnID: turnID})
	}
	if active {
		kind := provider.EventTurnFailed
		if interrupted {
			kind = provider.EventTurnInterrupted
		}
		b.emit(provider.Event{Kind: kind, ConversationID: p.id, TurnID: turnID})
	}
	b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: p.id})
	b.emit(provider.Event{Kind: provider.EventChanged})
}

func (p *sessionProcess) clearRequestsLocked() bool {
	had := len(p.requests) > 0
	clear(p.requests)
	return had
}

// respond answers a permission request. allow passes updatedInput through to
// the tool; deny returns message to the model.
func (p *sessionProcess) respond(requestID string, allow bool, updatedInput json.RawMessage, message string) error {
	response := map[string]any{"behavior": "deny", "message": message}
	if allow {
		if len(updatedInput) == 0 {
			updatedInput = json.RawMessage(`{}`)
		}
		response = map[string]any{"behavior": "allow", "updatedInput": updatedInput}
	}
	return p.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": requestID, "response": response}})
}

func (p *sessionProcess) userMessage(turnID, text string, images []provider.Image) map[string]any {
	content := make([]map[string]any, 0, 1+len(images))
	if strings.TrimSpace(text) != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, image := range images {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.MimeType, "data": base64.StdEncoding.EncodeToString(image.Data)}})
	}
	return map[string]any{"type": "user", "uuid": turnID, "session_id": p.id, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}}
}

func questionsFromInput(raw json.RawMessage) []provider.ActionQuestion {
	var input struct {
		Questions []struct {
			Question string                  `json:"question"`
			Header   string                  `json:"header"`
			Options  []provider.ActionOption `json:"options"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(raw, &input)
	result := make([]provider.ActionQuestion, 0, len(input.Questions))
	for index, question := range input.Questions {
		result = append(result, provider.ActionQuestion{ID: fmt.Sprintf("q%d", index), Header: question.Header, Question: question.Question, IsOther: true, Options: question.Options})
	}
	return result
}

func approvalDetail(request toolPermissionRequest) *provider.ActionDetail {
	var input map[string]any
	_ = json.Unmarshal(request.Input, &input)
	str := func(key string) string {
		value, _ := input[key].(string)
		return value
	}
	detail := &provider.ActionDetail{ToolName: request.ToolName, Description: summaryValue(oneLine(request.Description)), Reason: summaryValue(oneLine(request.BlockedPath))}
	switch request.ToolName {
	case "Bash", "PowerShell":
		detail.Command = summaryValue(str("command"))
	default:
		for _, key := range []string{"file_path", "notebook_path", "path"} {
			if value := str(key); value != "" {
				detail.FilePath = summaryValue(value)
				break
			}
		}
	}
	if detail.Description == "" {
		detail.Description = summaryValue(oneLine(str("description")))
	}
	return detail
}

// answeredInput adds Mobile answers to an AskUserQuestion input. The CLI
// expects answers keyed by question text; multiple selections are joined.
func answeredInput(raw json.RawMessage, questions []provider.ActionQuestion, result json.RawMessage) (json.RawMessage, error) {
	var payload struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if json.Unmarshal(result, &payload) != nil || len(payload.Answers) == 0 {
		return nil, provider.NewError("invalid_params", "answers are required")
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil || input == nil {
		input = make(map[string]any)
	}
	answers := make(map[string]string, len(questions))
	for _, question := range questions {
		if values := payload.Answers[question.ID].Answers; len(values) > 0 {
			answers[question.Question] = strings.Join(values, ", ")
		}
	}
	if len(answers) == 0 {
		return nil, provider.NewError("invalid_params", "answers do not match the pending questions")
	}
	input["answers"] = answers
	return json.Marshal(input)
}

func assistantItemID(messageID string, blockIndex int) string {
	return fmt.Sprintf("%s:%d", messageID, blockIndex)
}

var hexUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// turnIDForClientMessage derives the user record UUID from the Mobile client
// message ID. The CLI keeps a client-supplied uuid on the transcript record,
// so the turn ID, the live events and the persisted history agree.
func turnIDForClientMessage(clientMessageID string) string {
	switch {
	case sessionIDPattern.MatchString(clientMessageID):
		return strings.ToLower(clientMessageID)
	case hexUUIDPattern.MatchString(clientMessageID):
		value := strings.ToLower(clientMessageID)
		return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
	case clientMessageID != "":
		sum := sha256.Sum256([]byte("work-anywhere-client-message:" + clientMessageID))
		value := hex.EncodeToString(sum[:16])
		return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
	default:
		return newUUID()
	}
}

func newUUID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	text := hex.EncodeToString(value[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32]
}

func randomHex(size int) string {
	value := make([]byte, size)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func configureChild(cmd *exec.Cmd) { platformprocess.ConfigureManagedChild(cmd) }
