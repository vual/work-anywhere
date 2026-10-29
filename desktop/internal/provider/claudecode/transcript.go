package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

// record is the subset of a Claude Code transcript line used by the Mobile
// projection. Unknown record types and fields are ignored so new CLI versions
// degrade to missing detail rather than failing the conversation.
type record struct {
	Type                      string          `json:"type"`
	Subtype                   string          `json:"subtype"`
	UUID                      string          `json:"uuid"`
	IsSidechain               bool            `json:"isSidechain"`
	IsMeta                    bool            `json:"isMeta"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	IsAPIErrorMessage         bool            `json:"isApiErrorMessage"`
	CWD                       string          `json:"cwd"`
	SessionID                 string          `json:"sessionId"`
	Timestamp                 string          `json:"timestamp"`
	CustomTitle               string          `json:"customTitle"`
	AITitle                   string          `json:"aiTitle"`
	Message                   *messageRecord  `json:"message"`
	ToolUseResult             json.RawMessage `json:"toolUseResult"`
}

type messageRecord struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// summary is the cheap per-file state used for project and conversation
// lists. It is maintained incrementally as the transcript grows.
type summary struct {
	SessionID   string
	CWD         string
	FirstPrompt string
	CustomTitle string
	AITitle     string
	CreatedAt   int64
	LastAt      int64
	HasPrompt   bool
}

func (s *summary) title() string {
	for _, value := range []string{s.CustomTitle, s.AITitle, s.FirstPrompt} {
		if value = firstLine(value); value != "" {
			return value
		}
	}
	return "Untitled conversation"
}

func (s *summary) observe(r *record) {
	if r.SessionID != "" && s.SessionID == "" {
		s.SessionID = r.SessionID
	}
	if r.CWD != "" && s.CWD == "" && !r.IsSidechain {
		s.CWD = r.CWD
	}
	if at := parseTimestamp(r.Timestamp); at > 0 {
		if s.CreatedAt == 0 || at < s.CreatedAt {
			s.CreatedAt = at
		}
		if at > s.LastAt {
			s.LastAt = at
		}
	}
	switch r.Type {
	case "custom-title":
		s.CustomTitle = strings.TrimSpace(r.CustomTitle)
	case "ai-title":
		s.AITitle = strings.TrimSpace(r.AITitle)
	case "user":
		if !s.HasPrompt {
			if text, ok := humanPrompt(r); ok {
				s.HasPrompt = true
				s.FirstPrompt = text
			}
		}
	}
}

// transcript is the full timeline projection of one session file.
type transcript struct {
	summary
	items       []provider.TimelineItem
	itemIndex   map[string]int
	toolItems   map[string]int
	currentTurn string
}

func newTranscript() *transcript {
	return &transcript{itemIndex: make(map[string]int), toolItems: make(map[string]int)}
}

func (t *transcript) add(item provider.TimelineItem) {
	base := item.ID
	for suffix := 2; ; suffix++ {
		if _, exists := t.itemIndex[item.ID]; !exists {
			break
		}
		item.ID = fmt.Sprintf("%s#%d", base, suffix)
	}
	t.itemIndex[item.ID] = len(t.items)
	t.items = append(t.items, item)
}

func (t *transcript) observe(r *record) {
	t.summary.observe(r)
	if r.IsSidechain {
		return
	}
	switch r.Type {
	case "user":
		t.observeUser(r)
	case "assistant":
		t.observeAssistant(r)
	case "system":
		if r.Subtype == "compact_boundary" {
			t.add(summaryItem(r.UUID, t.currentTurn, "contextCompaction", "contextCompaction", ""))
		}
	}
}

func (t *transcript) observeUser(r *record) {
	if r.Message == nil || r.IsMeta || r.IsVisibleInTranscriptOnly {
		return
	}
	if r.IsCompactSummary {
		t.add(summaryItem(r.UUID, t.currentTurn, "contextCompaction", "contextCompaction", ""))
		return
	}
	blocks := decodeContent(r.Message.Content)
	results := false
	for _, block := range blocks {
		if block.Type == "tool_result" {
			results = true
			t.applyToolResult(block)
		}
	}
	if results {
		return
	}
	text, ok := humanPrompt(r)
	if !ok {
		return
	}
	t.currentTurn = r.UUID
	t.add(provider.TimelineItem{ID: r.UUID, TurnID: r.UUID, Kind: "userMessage", Status: "completed", Blocks: []provider.TimelineBlock{{Kind: "text", Text: text}}})
}

func (t *transcript) observeAssistant(r *record) {
	if r.Message == nil {
		return
	}
	for index, block := range decodeContent(r.Message.Content) {
		id := r.UUID
		if index > 0 {
			id = fmt.Sprintf("%s:%d", r.UUID, index)
		}
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			kind := "text"
			if r.IsAPIErrorMessage {
				kind = "error"
			}
			t.add(provider.TimelineItem{ID: id, TurnID: t.currentTurn, Kind: "agentMessage", Status: "completed", Blocks: []provider.TimelineBlock{{Kind: kind, Text: block.Text}}})
		case "thinking":
			if strings.TrimSpace(block.Thinking) == "" {
				continue
			}
			t.add(provider.TimelineItem{ID: id, TurnID: t.currentTurn, Kind: "reasoning", Status: "completed", Blocks: []provider.TimelineBlock{{Kind: "reasoning", Text: block.Thinking}}})
		case "tool_use":
			if block.ID != "" {
				id = block.ID
			}
			item := toolItem(id, t.currentTurn, block.Name, block.Input)
			t.add(item)
			t.toolItems[block.ID] = len(t.items) - 1
		}
	}
}

func (t *transcript) applyToolResult(block contentBlock) {
	index, ok := t.toolItems[block.ToolUseID]
	if !ok {
		return
	}
	item := &t.items[index]
	item.Status = "completed"
	if block.IsError {
		item.Status = "failed"
	}
	if item.Kind != "commandExecution" {
		return
	}
	if output := strings.Join(readableText(block.Content), "\n"); strings.TrimSpace(output) != "" {
		item.Blocks = append(item.Blocks, provider.TimelineBlock{Kind: "output", Text: output})
	}
}

func summaryItem(id, turnID, kind, summaryKind, value string) provider.TimelineItem {
	text := value
	if text == "" {
		text = summaryKind
	}
	return provider.TimelineItem{ID: id, TurnID: turnID, Kind: kind, Status: "completed", Blocks: []provider.TimelineBlock{{Kind: "summary", Text: text, SummaryKind: summaryKind, SummaryValue: value}}}
}

// toolItem maps Claude Code tools onto the timeline kinds Mobile already
// renders: shell tools become command executions, file editors become file
// changes, and everything else becomes a one-line tool summary.
func toolItem(id, turnID, name string, rawInput json.RawMessage) provider.TimelineItem {
	var input map[string]any
	_ = json.Unmarshal(rawInput, &input)
	str := func(key string) string {
		value, _ := input[key].(string)
		return value
	}
	item := provider.TimelineItem{ID: id, TurnID: turnID, Status: "inProgress"}
	switch name {
	case "Bash", "PowerShell":
		item.Kind = "commandExecution"
		item.Blocks = []provider.TimelineBlock{{Kind: "command", Text: str("command")}}
		return item
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		item.Kind = "fileChange"
		item.Blocks = []provider.TimelineBlock{{Kind: "diff", Text: fileChangeText(name, input)}}
		return item
	}
	kind, summaryKind, value := "dynamicToolCall", "dynamicToolCall", name
	switch {
	case strings.HasPrefix(name, "mcp__"):
		parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
		kind, summaryKind, value = "mcpToolCall", "mcpToolCall", strings.Join(parts, "/")
	case name == "WebSearch":
		kind, summaryKind, value = "webSearch", "webSearch", oneLine(str("query"))
	case name == "Task" || name == "Agent":
		target := str("subagent_type")
		if description := oneLine(str("description")); description != "" {
			target = strings.TrimSpace(target + " " + description)
		}
		kind, summaryKind, value = "collabAgentToolCall", "collabAgentToolCall", target
	default:
		for _, key := range []string{"file_path", "path", "pattern", "url", "query", "command", "skill"} {
			if detail := oneLine(str(key)); detail != "" {
				if key == "file_path" || key == "path" {
					detail = baseName(detail)
				}
				value = name + " " + detail
				break
			}
		}
	}
	summaryItem := summaryItem(id, turnID, kind, summaryKind, summaryValue(value))
	summaryItem.Status = item.Status
	return summaryItem
}

func fileChangeText(name string, input map[string]any) string {
	path, _ := input["file_path"].(string)
	if path == "" {
		path, _ = input["notebook_path"].(string)
	}
	var builder strings.Builder
	builder.WriteString(path)
	builder.WriteByte('\n')
	writeLines := func(prefix, text string) {
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			builder.WriteString(prefix)
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	edit := func(values map[string]any) {
		oldText, _ := values["old_string"].(string)
		newText, _ := values["new_string"].(string)
		if oldText != "" {
			writeLines("-", oldText)
		}
		if newText != "" {
			writeLines("+", newText)
		}
	}
	switch name {
	case "Write":
		content, _ := input["content"].(string)
		writeLines("+", content)
	case "MultiEdit":
		edits, _ := input["edits"].([]any)
		for _, value := range edits {
			if values, ok := value.(map[string]any); ok {
				edit(values)
			}
		}
	case "NotebookEdit":
		source, _ := input["new_source"].(string)
		writeLines("+", source)
	default:
		edit(input)
	}
	return strings.TrimRight(builder.String(), "\n")
}

var commandNamePattern = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
var commandArgsPattern = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)

// humanPrompt returns the visible text of a user-authored prompt. Tool
// results, synthetic meta records and local command output are excluded.
func humanPrompt(r *record) (string, bool) {
	if r.Type != "user" || r.Message == nil || r.IsMeta || r.IsCompactSummary || r.IsSidechain {
		return "", false
	}
	parts := make([]string, 0, 1)
	images := 0
	for _, block := range decodeContent(r.Message.Content) {
		switch block.Type {
		case "tool_result":
			return "", false
		case "text":
			parts = append(parts, block.Text)
		case "image":
			images++
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if strings.HasPrefix(text, "<local-command-") || strings.HasPrefix(text, "<system-reminder>") {
		return "", false
	}
	if match := commandNamePattern.FindStringSubmatch(text); match != nil {
		command := strings.TrimSpace(match[1])
		if args := commandArgsPattern.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
			command += " " + strings.TrimSpace(args[1])
		}
		text = command
	}
	for range images {
		if text != "" {
			text += "\n"
		}
		text += "[image]"
	}
	return text, text != ""
}

// decodeContent accepts both the string and block-array message shapes.
func decodeContent(raw json.RawMessage) []contentBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return []contentBlock{{Type: "text", Text: text}}
		}
		return nil
	}
	var blocks []contentBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

func readableText(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return []string{text}
		}
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	result := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			result = append(result, block.Text)
		}
	}
	return result
}

// scanState lets a transcript be extended in place when the session file
// grows, which is the common case while a session is live.
type scanState struct {
	size    int64
	modTime time.Time
	offset  int64
}

var errTranscriptReplaced = errors.New("transcript was replaced")

// scanFile feeds complete lines after state.offset to observe. A trailing
// partial line is left for the next scan.
func scanFile(path string, state *scanState, observe func(*record)) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < state.offset || (info.Size() == state.size && !info.ModTime().Equal(state.modTime) && state.offset > 0) {
		return errTranscriptReplaced
	}
	if info.Size() == state.size && info.ModTime().Equal(state.modTime) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Seek(state.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 256<<10)
	offset := state.offset
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		offset += int64(len(line))
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r record
		if json.Unmarshal(line, &r) == nil {
			observe(&r)
		}
	}
	state.offset = offset
	state.size = info.Size()
	state.modTime = info.ModTime()
	return nil
}

func parseTimestamp(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	const maxTitle = 80
	if runes := []rune(value); len(runes) > maxTitle {
		return string(runes[:maxTitle])
	}
	return value
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func summaryValue(value string) string {
	const maximum = 512
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && (value[end]&0xC0) == 0x80 {
		end--
	}
	return value[:end]
}
