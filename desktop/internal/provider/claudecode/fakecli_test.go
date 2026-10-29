package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake Claude Code CLI. When WA_FAKE_CLAUDE is
// set it speaks the recorded stream-json protocol on stdio and appends the
// transcript records the real CLI writes, so the backend is exercised through
// a real child process without contacting a model.
func TestMain(m *testing.M) {
	if os.Getenv("WA_FAKE_CLAUDE") != "" {
		os.Exit(runFakeClaude())
	}
	os.Exit(m.Run())
}

type fakeCLI struct {
	out        *bufio.Writer
	in         *bufio.Scanner
	log        *os.File
	transcript string
	sessionID  string
	cwd        string
	turnID     string
}

func runFakeClaude() int {
	args := os.Args[1:]
	cli := &fakeCLI{out: bufio.NewWriter(os.Stdout), in: bufio.NewScanner(os.Stdin)}
	cli.in.Buffer(make([]byte, 1<<20), 16<<20)
	cli.cwd, _ = os.Getwd()
	for index, arg := range args {
		if (arg == "--session-id" || arg == "--resume") && index+1 < len(args) {
			cli.sessionID = args[index+1]
		}
	}
	if path := os.Getenv("WA_FAKE_LOG"); path != "" {
		cli.log, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		cli.record(map[string]any{"args": args, "env": os.Environ()})
	}
	cli.transcript = filepath.Join(os.Getenv("WA_FAKE_PROJECTS"), cli.sessionID+".jsonl")
	for {
		message, ok := cli.next()
		if !ok {
			return 0
		}
		switch message["type"] {
		case "control_request":
			request, _ := message["request"].(map[string]any)
			cli.send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": message["request_id"], "response": map[string]any{}}})
			if request["subtype"] == "interrupt" {
				cli.record(map[string]any{"interrupt": true})
			}
		case "user":
			cli.turnID, _ = message["uuid"].(string)
			text := userText(message)
			cli.appendTranscript(map[string]any{"type": "user", "uuid": cli.turnID, "sessionId": cli.sessionID, "cwd": cli.cwd, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": text}})
			cli.send(map[string]any{"type": "user", "uuid": cli.turnID, "isReplay": true, "message": message["message"]})
			if code := cli.turn(text); code >= 0 {
				return code
			}
		}
	}
}

// turn plays one scripted turn selected by the prompt. A non-negative
// return value ends the process with that exit code.
func (c *fakeCLI) turn(text string) int {
	c.send(map[string]any{"type": "system", "subtype": "init", "session_id": c.sessionID})
	switch {
	case strings.Contains(text, "crash"):
		return 3
	case strings.Contains(text, "approve"):
		response := c.permission("Bash", "toolu_bash", map[string]any{"command": "go test ./...", "description": "Run tests"})
		c.reply("msg_approve", "decision:"+response.behavior)
	case strings.Contains(text, "ask"):
		response := c.permission("AskUserQuestion", "toolu_ask", map[string]any{"questions": []any{map[string]any{"question": "Which color?", "header": "Color", "multiSelect": false, "options": []any{map[string]any{"label": "Red", "description": "warm"}, map[string]any{"label": "Blue"}}}}})
		answers, _ := json.Marshal(response.input["answers"])
		c.reply("msg_ask", "answers:"+string(answers))
	case strings.Contains(text, "plan"):
		response := c.permission("ExitPlanMode", "toolu_plan", map[string]any{"plan": "1. Do the work", "planFilePath": "plan.md"})
		c.reply("msg_plan", "plan:"+response.behavior+":"+response.message)
	case strings.Contains(text, "slow"):
		c.delta("msg_slow", "Counting")
		for {
			message, ok := c.next()
			if !ok {
				return 0
			}
			if request, _ := message["request"].(map[string]any); message["type"] == "control_request" && request["subtype"] == "interrupt" {
				c.send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": message["request_id"], "response": map[string]any{"still_queued": []any{}}}})
				c.send(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true})
				return -1
			}
		}
	default:
		c.reply("msg_hello", "Hello")
	}
	return -1
}

type fakePermission struct {
	behavior string
	message  string
	input    map[string]any
}

func (c *fakeCLI) permission(tool, toolUseID string, input map[string]any) fakePermission {
	c.appendTranscript(map[string]any{"type": "assistant", "uuid": "a-" + toolUseID, "sessionId": c.sessionID, "message": map[string]any{"id": "msg_" + toolUseID, "role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": toolUseID, "name": tool, "input": input}}}, "apiBlockIndex": 0})
	c.send(map[string]any{"type": "control_request", "request_id": "req-" + toolUseID, "request": map[string]any{"subtype": "can_use_tool", "tool_name": tool, "input": input, "tool_use_id": toolUseID}})
	for {
		message, ok := c.next()
		if !ok {
			os.Exit(0)
		}
		if message["type"] != "control_response" {
			continue
		}
		c.record(map[string]any{"permission": message})
		response, _ := message["response"].(map[string]any)
		inner, _ := response["response"].(map[string]any)
		result := fakePermission{}
		result.behavior, _ = inner["behavior"].(string)
		result.message, _ = inner["message"].(string)
		result.input, _ = inner["updatedInput"].(map[string]any)
		// The real CLI records the outcome as a tool_result; a denial is an
		// error result carrying the host's message.
		content := "ok"
		if result.behavior != "allow" {
			content = result.message
		}
		c.appendTranscript(map[string]any{"type": "user", "uuid": "r-" + toolUseID, "sessionId": c.sessionID, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": content, "is_error": result.behavior != "allow"}}}})
		return result
	}
}

func (c *fakeCLI) delta(messageID, text string) {
	c.send(map[string]any{"type": "stream_event", "api_message_id": messageID, "parent_tool_use_id": nil, "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}}})
}

func (c *fakeCLI) reply(messageID, text string) {
	half := len(text) / 2
	c.delta(messageID, text[:half])
	c.delta(messageID, text[half:])
	record := map[string]any{"type": "assistant", "uuid": "a-" + messageID, "sessionId": c.sessionID, "message": map[string]any{"id": messageID, "role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}, "apiBlockIndex": 0}
	c.appendTranscript(record)
	c.send(record)
	c.send(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text})
}

func (c *fakeCLI) next() (map[string]any, bool) {
	for c.in.Scan() {
		var message map[string]any
		if json.Unmarshal(c.in.Bytes(), &message) == nil {
			c.record(map[string]any{"stdin": message})
			return message, true
		}
	}
	return nil, false
}

func (c *fakeCLI) send(value any) {
	data, _ := json.Marshal(value)
	_, _ = c.out.Write(append(data, '\n'))
	_ = c.out.Flush()
}

func (c *fakeCLI) record(value any) {
	if c.log == nil {
		return
	}
	data, _ := json.Marshal(value)
	_, _ = c.log.Write(append(data, '\n'))
}

func (c *fakeCLI) appendTranscript(value any) {
	file, err := os.OpenFile(c.transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer file.Close()
	data, _ := json.Marshal(value)
	_, _ = file.Write(append(data, '\n'))
}

func userText(message map[string]any) string {
	inner, _ := message["message"].(map[string]any)
	blocks, _ := inner["content"].([]any)
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if value, ok := block.(map[string]any); ok && value["type"] == "text" {
			text, _ := value["text"].(string)
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
