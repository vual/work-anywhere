package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

const interactiveSession = "22222222-3333-4444-8555-666666666666"

type interactiveHarness struct {
	backend   *Backend
	projectID string
	workDir   string
	projects  string
	logPath   string
	events    chan provider.Event
	processes *fakeProcesses
}

func newInteractiveHarness(t *testing.T) *interactiveHarness {
	t.Helper()
	configDir := t.TempDir()
	workDir := t.TempDir()
	projects := filepath.Join(configDir, "projects", "work")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(workDir)
	seed := `{"type":"user","uuid":"seed-user","sessionId":"` + interactiveSession + `","cwd":` + string(cwd) + `,"timestamp":"2026-10-27T01:00:00.000Z","message":{"role":"user","content":"earlier prompt"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, interactiveSession+".jsonl"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.log")
	t.Setenv("WA_FAKE_CLAUDE", "1")
	t.Setenv("WA_FAKE_LOG", logPath)
	t.Setenv("WA_FAKE_PROJECTS", projects)
	// A host-injected variable that must not reach the child.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "host-session")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processes := &fakeProcesses{alive: map[int]bool{}}
	backend, err := New(Options{ConfigDir: configDir, PollInterval: time.Hour, Alive: processes.Alive, Resolve: func(context.Context) (Runtime, error) {
		return Runtime{Executable: executable, Version: MinimumVersion}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := &interactiveHarness{backend: backend, workDir: workDir, projects: projects, logPath: logPath, events: make(chan provider.Event, 256), processes: processes}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = backend.Close()
	})
	if err = backend.Start(ctx, func(event provider.Event) { h.events <- event }); err != nil {
		t.Fatal(err)
	}
	backend.ensureRuntime(ctx)
	h.projectID = ProjectID(workDir)
	return h
}

// waitFor drains events until one matches kind, returning everything seen.
func (h *interactiveHarness) waitFor(t *testing.T, kind provider.EventKind) []provider.Event {
	t.Helper()
	seen := make([]provider.Event, 0)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-h.events:
			seen = append(seen, event)
			if event.Kind == kind {
				return seen
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %d; saw %#v", kind, seen)
		}
	}
}

func (h *interactiveHarness) start(t *testing.T, text string) string {
	t.Helper()
	turnID, err := h.backend.StartTurn(context.Background(), provider.TurnRequest{ConversationID: interactiveSession, ClientMessageID: "0123456789abcdef0123456789abcdef", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return turnID
}

func (h *interactiveHarness) log(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (h *interactiveHarness) permissionResponse(t *testing.T) map[string]any {
	t.Helper()
	for _, entry := range h.log(t) {
		if permission, ok := entry["permission"].(map[string]any); ok {
			response, _ := permission["response"].(map[string]any)
			inner, _ := response["response"].(map[string]any)
			return inner
		}
	}
	t.Fatal("fake CLI received no permission response")
	return nil
}

func TestTurnStreamsDeltasAndPersistsWithStableIDs(t *testing.T) {
	h := newInteractiveHarness(t)
	turnID := h.start(t, "hello")
	if turnID != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("turn id = %q", turnID)
	}
	events := h.waitFor(t, provider.EventTurnCompleted)
	var deltas []string
	started := false
	for _, event := range events {
		started = started || event.Kind == provider.EventTurnStarted
		if event.Kind == provider.EventDelta {
			if !started {
				t.Fatal("delta before turn start")
			}
			if event.ItemID != "msg_hello:0" || event.TurnID != turnID || event.DeltaKind != provider.DeltaAssistantText {
				t.Fatalf("delta = %#v", event)
			}
			deltas = append(deltas, event.Delta)
		}
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Fatalf("events = %#v", events)
	}

	// The persisted transcript uses the same turn and item identities.
	if err := h.backend.refresh(); err != nil {
		t.Fatal(err)
	}
	page, err := h.backend.Timeline(context.Background(), interactiveSession, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != "msg_hello:0" || page.Items[0].TurnID != turnID || page.Items[1].ID != turnID || page.Items[1].ClientMessageID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("timeline = %#v", page.Items)
	}

	start := h.log(t)[0]
	args := toStrings(start["args"])
	for _, want := range []string{"--resume", interactiveSession, "--permission-prompt-tool", "stdio", "--permission-prompts", "host", "--include-partial-messages", "--replay-user-messages"} {
		if !slices.Contains(args, want) {
			t.Fatalf("args %v missing %q", args, want)
		}
	}
	env := toStrings(start["env"])
	if slices.Contains(env, "CLAUDE_CODE_SESSION_ID=host-session") || !slices.Contains(env, "CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL=1") {
		t.Fatal("child environment was not sanitized")
	}
	stdin := h.log(t)[1]["stdin"].(map[string]any)
	if request, _ := stdin["request"].(map[string]any); stdin["type"] != "control_request" || request["subtype"] != "initialize" {
		t.Fatalf("first stdin message = %#v", stdin)
	}

	// The same process serves the next turn.
	h.start(t, "hello again")
	h.waitFor(t, provider.EventTurnCompleted)
	starts := 0
	for _, entry := range h.log(t) {
		if entry["args"] != nil {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("process started %d times", starts)
	}
}

func TestApprovalIsListedAndResolvedOnce(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "approve")
	h.waitFor(t, provider.EventActionRequested)
	actions := h.backend.Actions()
	if len(actions) != 1 || actions[0].Kind != actionKindApproval || actions[0].Detail == nil || actions[0].Detail.Command != "go test ./..." || actions[0].ConversationID != interactiveSession {
		t.Fatalf("actions = %#v", actions)
	}
	metadata, _ := h.backend.ReadConversation(context.Background(), interactiveSession)
	if metadata.Status != "waitingOnApproval" || !metadata.Running {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, err := h.backend.StartTurn(context.Background(), provider.TurnRequest{ConversationID: interactiveSession, Text: "more"}); err == nil {
		t.Fatal("second turn accepted while waiting for approval")
	} else if code, _ := provider.ErrorCode(err); code != "conversation_busy" {
		t.Fatalf("busy code = %q", code)
	}
	var id string
	_ = json.Unmarshal(actions[0].ID, &id)
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"decision":"accept"}`)); err != nil {
		t.Fatal(err)
	}
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"decision":"decline"}`)); err == nil {
		t.Fatal("second resolution accepted")
	} else if code, _ := provider.ErrorCode(err); code != "already_resolved" {
		t.Fatalf("second resolution code = %q", code)
	}
	h.waitFor(t, provider.EventTurnCompleted)
	if response := h.permissionResponse(t); response["behavior"] != "allow" {
		t.Fatalf("permission response = %#v", response)
	}
	if len(h.backend.Actions()) != 0 {
		t.Fatal("resolved action still listed")
	}
}

func TestDeclinedApprovalReturnsMessage(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "approve")
	h.waitFor(t, provider.EventActionRequested)
	var id string
	_ = json.Unmarshal(h.backend.Actions()[0].ID, &id)
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"decision":"maybe"}`)); err == nil {
		t.Fatal("invalid decision accepted")
	}
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"decision":"decline"}`)); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, provider.EventTurnCompleted)
	if response := h.permissionResponse(t); response["behavior"] != "deny" || response["message"] != declinedMessage {
		t.Fatalf("permission response = %#v", response)
	}
}

func TestQuestionAnswersAreKeyedByQuestionText(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "ask")
	h.waitFor(t, provider.EventActionRequested)
	actions := h.backend.Actions()
	if len(actions) != 1 || actions[0].Kind != actionKindUserInput || len(actions[0].Questions) != 1 || actions[0].Questions[0].ID != "q0" || actions[0].Questions[0].Options[0].Label != "Red" {
		t.Fatalf("actions = %#v", actions)
	}
	var id string
	_ = json.Unmarshal(actions[0].ID, &id)
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"answers":{"unknown":{"answers":["x"]}}}`)); err == nil {
		t.Fatal("unmatched answers accepted")
	}
	if err := h.backend.ResolveAction(context.Background(), id, json.RawMessage(`{"answers":{"q0":{"answers":["Red"]}}}`)); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, provider.EventTurnCompleted)
	response := h.permissionResponse(t)
	input, _ := response["updatedInput"].(map[string]any)
	answers, _ := input["answers"].(map[string]any)
	if response["behavior"] != "allow" || answers["Which color?"] != "Red" || input["questions"] == nil {
		t.Fatalf("permission response = %#v", response)
	}
}

func TestPlanApprovalUsesPlanConfirmation(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "plan")
	h.waitFor(t, provider.EventActionRequested)
	if actions := h.backend.Actions(); len(actions) != 0 {
		t.Fatalf("plan listed as action: %#v", actions)
	}
	confirmation := h.backend.PlanConfirmation(interactiveSession)
	if confirmation == nil || confirmation.PlanItemID != "toolu_plan" {
		t.Fatalf("confirmation = %#v", confirmation)
	}
	if err := h.backend.RespondPlan(context.Background(), interactiveSession, "other", "revise", "x"); err == nil {
		t.Fatal("stale plan accepted")
	} else if code, _ := provider.ErrorCode(err); code != "stale_plan" {
		t.Fatalf("stale code = %q", code)
	}
	if err := h.backend.RespondPlan(context.Background(), interactiveSession, "toolu_plan", "revise", "Make it shorter"); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, provider.EventTurnCompleted)
	if response := h.permissionResponse(t); response["behavior"] != "deny" || response["message"] != "Make it shorter" {
		t.Fatalf("permission response = %#v", response)
	}
	if h.backend.PlanConfirmation(interactiveSession) != nil {
		t.Fatal("plan still pending")
	}
	_ = h.backend.refresh()
	page, _ := h.backend.Timeline(context.Background(), interactiveSession, "", 5)
	found := false
	for _, item := range page.Items {
		found = found || (item.ID == "toolu_plan" && item.Kind == "plan" && item.Status != "failed" && item.Blocks[0].Text == "1. Do the work")
	}
	if !found {
		t.Fatalf("plan item missing: %#v", page.Items)
	}
}

func TestInterruptEndsTurn(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "slow")
	h.waitFor(t, provider.EventDelta)
	if err := h.backend.InterruptTurn(context.Background(), interactiveSession); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, provider.EventTurnInterrupted)
	metadata, _ := h.backend.ReadConversation(context.Background(), interactiveSession)
	if metadata.Running {
		t.Fatal("conversation still running after interrupt")
	}
}

func TestProcessExitFailsTurn(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "crash")
	h.waitFor(t, provider.EventTurnFailed)
	h.backend.mu.Lock()
	remaining := len(h.backend.processes) + len(h.backend.ownPIDs)
	h.backend.mu.Unlock()
	if remaining != 0 {
		t.Fatal("exited process still registered")
	}
	// The next turn starts a fresh process.
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
}

func TestExternallyActiveConversationIsReadOnly(t *testing.T) {
	h := newInteractiveHarness(t)
	sessions := filepath.Join(h.backend.configDir, "sessions")
	_ = os.MkdirAll(sessions, 0o700)
	if err := os.WriteFile(filepath.Join(sessions, "77.json"), []byte(`{"pid":77,"sessionId":"`+interactiveSession+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.processes.mu.Lock()
	h.processes.alive[77] = true
	h.processes.mu.Unlock()
	_ = h.backend.refresh()
	_, err := h.backend.StartTurn(context.Background(), provider.TurnRequest{ConversationID: interactiveSession, Text: "hello"})
	if code, _ := provider.ErrorCode(err); code != "conversation_busy_external" {
		t.Fatalf("error = %v", err)
	}
}

func TestOwnProcessIsNotExternal(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
	h.backend.mu.Lock()
	var pid int
	for value := range h.backend.ownPIDs {
		pid = value
	}
	h.backend.mu.Unlock()
	sessions := filepath.Join(h.backend.configDir, "sessions")
	_ = os.MkdirAll(sessions, 0o700)
	_ = os.WriteFile(filepath.Join(sessions, "own.json"), []byte(`{"pid":`+jsonNumberString(pid)+`,"sessionId":"`+interactiveSession+`"}`), 0o600)
	h.processes.mu.Lock()
	h.processes.alive[pid] = true
	h.processes.mu.Unlock()
	_ = h.backend.refresh()
	if metadata, _ := h.backend.ReadConversation(context.Background(), interactiveSession); metadata.ExternallyActive {
		t.Fatal("daemon-owned process reported as external")
	}
}

func TestCreatedConversationStartsNewSession(t *testing.T) {
	h := newInteractiveHarness(t)
	if _, err := h.backend.CreateConversation(context.Background(), "missing"); err == nil {
		t.Fatal("unknown project accepted")
	}
	metadata, err := h.backend.CreateConversation(context.Background(), h.projectID)
	if err != nil || !sessionIDPattern.MatchString(metadata.ID) || metadata.ProjectID != h.projectID {
		t.Fatalf("created = %#v, %v", metadata, err)
	}
	read, err := h.backend.ReadConversation(context.Background(), metadata.ID)
	if err != nil || read.Title != "New conversation" {
		t.Fatalf("read = %#v, %v", read, err)
	}
	if page, err := h.backend.Timeline(context.Background(), metadata.ID, "", 5); err != nil || len(page.Items) != 0 {
		t.Fatalf("timeline = %#v, %v", page, err)
	}
	if root, ok := h.backend.ProjectRoot("", metadata.ID); !ok || root != h.workDir {
		t.Fatalf("root = %q", root)
	}
	if _, err = h.backend.StartTurn(context.Background(), provider.TurnRequest{ConversationID: metadata.ID, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, provider.EventTurnCompleted)
	args := toStrings(h.log(t)[0]["args"])
	if index := slices.Index(args, "--session-id"); index < 0 || args[index+1] != metadata.ID {
		t.Fatalf("args = %v", args)
	}
	_ = h.backend.refresh()
	h.backend.mu.Lock()
	_, pending := h.backend.created[metadata.ID]
	h.backend.mu.Unlock()
	if pending {
		t.Fatal("created placeholder kept after the transcript appeared")
	}
}

func TestSettingsApplyToLaunchFlags(t *testing.T) {
	h := newInteractiveHarness(t)
	if _, err := h.backend.UpdateSettings(context.Background(), interactiveSession, provider.ConversationSettings{PermissionModeID: "readOnly"}); err == nil {
		t.Fatal("unavailable permission mode accepted")
	}
	settings, err := h.backend.UpdateSettings(context.Background(), interactiveSession, provider.ConversationSettings{Mode: "default", ModelID: "opus", ReasoningEffort: "high", PermissionModeID: "autoReview"})
	if err != nil || settings.ModelID != "opus" {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
	args := toStrings(h.log(t)[0]["args"])
	for _, pair := range [][2]string{{"--model", "opus"}, {"--effort", "high"}, {"--permission-mode", "auto"}} {
		if index := slices.Index(args, pair[0]); index < 0 || args[index+1] != pair[1] {
			t.Fatalf("args %v missing %v", args, pair)
		}
	}
	// A changed setting restarts the idle process before the next turn.
	if _, err = h.backend.UpdateSettings(context.Background(), interactiveSession, provider.ConversationSettings{Mode: "plan"}); err != nil {
		t.Fatal(err)
	}
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
	var last []string
	for _, entry := range h.log(t) {
		if entry["args"] != nil {
			last = toStrings(entry["args"])
		}
	}
	if index := slices.Index(last, "--permission-mode"); index < 0 || last[index+1] != "plan" {
		t.Fatalf("restarted args = %v", last)
	}
}

func TestIdleProcessIsStopped(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
	h.backend.stopIdleProcesses(time.Now().Add(time.Hour))
	h.backend.mu.Lock()
	running := len(h.backend.processes)
	h.backend.mu.Unlock()
	if running != 0 {
		t.Fatal("idle process kept")
	}
}

func TestSessionArgsAndHelpers(t *testing.T) {
	args := sessionArgs("id", false, provider.ConversationSettings{PermissionModeID: "fullAccess", ModelID: defaultModelID})
	if !slices.Contains(args, "--session-id") || !slices.Contains(args, "bypassPermissions") || !slices.Contains(args, "--allow-dangerously-skip-permissions") || slices.Contains(args, "--model") {
		t.Fatalf("args = %v", args)
	}
	if got := turnIDForClientMessage("22222222-3333-4444-8555-666666666666"); got != "22222222-3333-4444-8555-666666666666" {
		t.Fatalf("uuid passthrough = %q", got)
	}
	if got := turnIDForClientMessage("not-hex"); !sessionIDPattern.MatchString(got) || got != turnIDForClientMessage("not-hex") {
		t.Fatalf("derived id = %q", got)
	}
	if compareVersions("2.1.310", MinimumVersion) <= 0 || compareVersions("2.0.999", MinimumVersion) >= 0 || compareVersions(MinimumVersion, MinimumVersion) != 0 {
		t.Fatal("version comparison")
	}
	env := childEnvironment([]string{"PATH=x", "CLAUDECODE=1", "claude_code_entrypoint=sdk", "ANTHROPIC_BASE_URL=y"})
	if !slices.Equal(env, []string{"PATH=x", "ANTHROPIC_BASE_URL=y", "CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL=1"}) {
		t.Fatalf("env = %v", env)
	}
}

func TestModelCatalogShowsMappedModels(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"custom-opus"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "")
	catalog := modelCatalog(configDir)
	if len(catalog.Items) != 4 || !catalog.Items[0].Default || catalog.Items[1].ID != "opus" || catalog.Items[1].Description != "custom-opus" {
		t.Fatalf("catalog = %#v", catalog)
	}
}

func toStrings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		result = append(result, text)
	}
	return result
}

func jsonNumberString(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}
