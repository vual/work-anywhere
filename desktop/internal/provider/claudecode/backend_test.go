package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

const fixtureSession = "11111111-2222-4333-8444-555555555555"

type fakeProcesses struct {
	mu    sync.Mutex
	alive map[int]bool
}

func (f *fakeProcesses) Alive(pid int, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[pid]
}

func newFixtureBackend(t *testing.T) (*Backend, string, *fakeProcesses) {
	t.Helper()
	configDir := t.TempDir()
	projectDir := filepath.Join(configDir, "projects", "C--work-demo-project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "claude-code", "transcripts", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, fixtureSession+".jsonl")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Files that are not session transcripts must be ignored.
	if err = os.WriteFile(filepath.Join(projectDir, "notes.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	processes := &fakeProcesses{alive: map[int]bool{}}
	backend, err := New(Options{ConfigDir: configDir, Alive: processes.Alive})
	if err != nil {
		t.Fatal(err)
	}
	if err = backend.refresh(); err != nil {
		t.Fatal(err)
	}
	return backend, path, processes
}

func TestProjectsAndConversationsFromTranscripts(t *testing.T) {
	backend, _, _ := newFixtureBackend(t)
	ctx := context.Background()
	projects, err := backend.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %#v, %v", projects, err)
	}
	if projects[0].Name != "demo-project" || projects[0].ID != ProjectID(`C:\work\demo-project`) || projects[0].AgentID != backend.Agent().ID {
		t.Fatalf("project = %#v", projects[0])
	}
	if root, ok := backend.ProjectRoot(projects[0].ID, ""); !ok || root != `C:\work\demo-project` {
		t.Fatalf("project root = %q, %v", root, ok)
	}
	page, err := backend.ListConversations(ctx, provider.ConversationQuery{ProjectID: projects[0].ID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("conversations = %#v, %v", page, err)
	}
	conversation := page.Items[0]
	// The latest custom title wins over the AI title and the first prompt.
	if conversation.ID != fixtureSession || conversation.Title != "Auth fixes" || conversation.ProjectName != "demo-project" || conversation.ExternallyActive {
		t.Fatalf("conversation = %#v", conversation)
	}
	if conversation.CreatedAt == 0 || conversation.UpdatedAt == 0 {
		t.Fatalf("timestamps missing: %#v", conversation)
	}
	empty, err := backend.ListConversations(ctx, provider.ConversationQuery{Query: "no such title"})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("query filter = %#v, %v", empty, err)
	}
	if _, err = backend.ReadConversation(ctx, "missing"); err == nil {
		t.Fatal("missing conversation was readable")
	} else if code, _ := provider.ErrorCode(err); code != "not_found" {
		t.Fatalf("error code = %q", code)
	}
}

func TestTimelineProjection(t *testing.T) {
	backend, _, _ := newFixtureBackend(t)
	page, err := backend.Timeline(context.Background(), fixtureSession, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	type want struct{ kind, status, block, text string }
	// Newest first; sidechain, meta and bookkeeping records are hidden.
	expected := []want{
		{"agentMessage", "completed", "text", "Review complete."},
		{"contextCompaction", "completed", "summary", "contextCompaction"},
		{"userMessage", "completed", "text", "/review auth.go"},
		{"agentMessage", "completed", "text", "Fixed the login bug."},
		{"mcpToolCall", "inProgress", "summary", "github/create_issue"},
		{"fileChange", "completed", "diff", "C:\\work\\demo-project\\auth.go\n-return nil\n+return err"},
		{"commandExecution", "failed", "command", "go test ./..."},
		{"dynamicToolCall", "completed", "summary", "Read auth.go"},
		{"agentMessage", "completed", "text", "I'll inspect the code."},
		{"reasoning", "completed", "reasoning", "Look at auth.go first."},
		{"userMessage", "completed", "text", "Fix the login bug\nand add a test"},
	}
	if len(page.Items) != len(expected) || page.NextCursor != "" {
		t.Fatalf("got %d items (next %q): %#v", len(page.Items), page.NextCursor, page.Items)
	}
	for index, item := range page.Items {
		if item.Kind != expected[index].kind || item.Status != expected[index].status || item.Blocks[0].Kind != expected[index].block || item.Blocks[0].Text != expected[index].text {
			t.Fatalf("item %d = %#v, want %#v", index, item, expected[index])
		}
	}
	command := page.Items[6]
	if len(command.Blocks) != 2 || command.Blocks[1].Kind != "output" || command.Blocks[1].Text != "FAIL auth" {
		t.Fatalf("command output = %#v", command.Blocks)
	}
	if page.Items[0].TurnID != "u2" || page.Items[3].TurnID != "u1" || page.Items[2].TurnID != "u2" {
		t.Fatalf("turns = %q %q %q", page.Items[0].TurnID, page.Items[3].TurnID, page.Items[2].TurnID)
	}
	text, err := backend.TimelineBlock(context.Background(), fixtureSession, "u1", "toolu_bash", 1)
	if err != nil || text != "FAIL auth" {
		t.Fatalf("block = %q, %v", text, err)
	}
	if _, err = backend.TimelineBlock(context.Background(), fixtureSession, "", "toolu_mcp", 0); err == nil {
		t.Fatal("summary block was returned as content")
	}
}

func TestTimelinePagingUsesStableCursors(t *testing.T) {
	backend, path, _ := newFixtureBackend(t)
	ctx := context.Background()
	first, err := backend.Timeline(ctx, fixtureSession, "", 4)
	if err != nil || len(first.Items) != 4 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	// New lines appended by a live session must not shift older pages.
	appendLine(t, path, `{"type":"assistant","uuid":"a9","sessionId":"`+fixtureSession+`","cwd":"C:\\work\\demo-project","timestamp":"2026-10-27T01:06:00.000Z","message":{"id":"msg7","role":"assistant","content":[{"type":"text","text":"Another reply."}]}}`)
	second, err := backend.Timeline(ctx, fixtureSession, first.NextCursor, 4)
	if err != nil || len(second.Items) != 4 {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	if second.Items[0].Kind != "mcpToolCall" {
		t.Fatalf("second page starts at %#v", second.Items[0])
	}
	all, err := backend.Timeline(ctx, fixtureSession, "", 50)
	if err != nil || all.Items[0].Blocks[0].Text != "Another reply." {
		t.Fatalf("appended item missing: %#v, %v", all.Items[0], err)
	}
	if _, err = backend.Timeline(ctx, fixtureSession, "bm90LWFuLWl0ZW0", 4); err == nil {
		t.Fatal("unknown cursor accepted")
	}
}

func TestRefreshReportsChangesAndLiveSessions(t *testing.T) {
	backend, path, processes := newFixtureBackend(t)
	var mu sync.Mutex
	notified := map[string]int{}
	backend.notify = func(event provider.Event) {
		if event.Kind != provider.EventChanged {
			return
		}
		mu.Lock()
		notified[event.ConversationID]++
		mu.Unlock()
	}
	snapshot := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		result := make(map[string]int, len(notified))
		for key, value := range notified {
			result[key] = value
		}
		clear(notified)
		return result
	}
	if err := backend.refresh(); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("unchanged refresh notified %#v", got)
	}

	appendLine(t, path, `{"type":"custom-title","customTitle":"Renamed","sessionId":"`+fixtureSession+`"}`)
	_ = backend.refresh()
	if got := snapshot(); got[fixtureSession] != 1 || got[""] != 1 {
		t.Fatalf("append notified %#v", got)
	}
	page, _ := backend.ListConversations(context.Background(), provider.ConversationQuery{})
	if page.Items[0].Title != "Renamed" {
		t.Fatalf("title = %q", page.Items[0].Title)
	}

	sessions := filepath.Join(backend.configDir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "4242.json"), []byte(`{"pid":4242,"sessionId":"`+fixtureSession+`","procStart":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	processes.mu.Lock()
	processes.alive[4242] = true
	processes.mu.Unlock()
	_ = backend.refresh()
	if got := snapshot(); got[fixtureSession] != 1 {
		t.Fatalf("live change notified %#v", got)
	}
	metadata, err := backend.ReadConversation(context.Background(), fixtureSession)
	if err != nil || !metadata.ExternallyActive {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}

	// A registry file left behind by an exited process does not lock the
	// conversation.
	processes.mu.Lock()
	processes.alive[4242] = false
	processes.mu.Unlock()
	_ = backend.refresh()
	metadata, _ = backend.ReadConversation(context.Background(), fixtureSession)
	if metadata.ExternallyActive {
		t.Fatal("dead process still marks the conversation active")
	}
	if got := snapshot(); got[fixtureSession] != 1 {
		t.Fatalf("exit notified %#v", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_ = backend.refresh()
	if got := snapshot(); got[""] != 1 {
		t.Fatalf("removal notified %#v", got)
	}
	if projects, _ := backend.ListProjects(context.Background()); len(projects) != 0 {
		t.Fatalf("removed transcript still listed: %#v", projects)
	}
}

func TestPartialTrailingLineIsDeferred(t *testing.T) {
	backend, path, _ := newFixtureBackend(t)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","uuid":"a10","sessionId":"` + fixtureSession + `","message":{"id":"m","role":"assistant","content":[{"type":"text","text":"half"}]}}`
	_, _ = file.WriteString(line[:40])
	_ = file.Close()
	page, err := backend.Timeline(context.Background(), fixtureSession, "", 1)
	if err != nil || page.Items[0].Blocks[0].Text != "Review complete." {
		t.Fatalf("partial line was parsed: %#v, %v", page, err)
	}
	appendLine(t, path, line[40:])
	page, err = backend.Timeline(context.Background(), fixtureSession, "", 1)
	if err != nil || page.Items[0].Blocks[0].Text != "half" {
		t.Fatalf("completed line missing: %#v, %v", page, err)
	}
}

func TestHumanPromptFiltering(t *testing.T) {
	cases := []struct {
		content string
		want    string
		ok      bool
	}{
		{`"plain prompt"`, "plain prompt", true},
		{`[{"type":"text","text":"look"},{"type":"image","source":{}}]`, "look\n[image]", true},
		{`"<local-command-stdout>done</local-command-stdout>"`, "", false},
		{`[{"type":"tool_result","tool_use_id":"x","content":"y"}]`, "", false},
		{`"<command-name>/clear</command-name>"`, "/clear", true},
	}
	for _, test := range cases {
		r := &record{Type: "user", Message: &messageRecord{Role: "user", Content: []byte(test.content)}}
		got, ok := humanPrompt(r)
		if got != test.want || ok != test.ok {
			t.Fatalf("humanPrompt(%s) = %q, %v", test.content, got, ok)
		}
	}
}

func TestBaseNameHandlesBothSeparators(t *testing.T) {
	for input, want := range map[string]string{`C:\work\demo`: "demo", "/home/u/repo/": "repo", `C:\`: "", "plain": "plain"} {
		if got := baseName(input); got != want {
			t.Fatalf("baseName(%q) = %q, want %q", input, got, want)
		}
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}
