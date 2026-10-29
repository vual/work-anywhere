//go:build windows

package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

func TestClaudeManagedInteractiveLifecycle(t *testing.T) {
	h := newInteractiveHarness(t)
	group, err := platformprocess.NewGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	h.backend.attach = group.Add
	for range 2 {
		h.start(t, "hello")
		h.waitFor(t, provider.EventTurnCompleted)
	}
}

// Opt-in: copies only auth material and provider env settings into a temporary
// config. User hooks/MCP settings and existing transcripts are never executed.
func TestClaudeMobileLifecycleIntegration(t *testing.T) {
	exe, source := os.Getenv("WORK_ANYWHERE_TEST_CLAUDE"), os.Getenv("WORK_ANYWHERE_TEST_CLAUDE_CONFIG")
	if exe == "" || source == "" {
		t.Skip("set WORK_ANYWHERE_TEST_CLAUDE and WORK_ANYWHERE_TEST_CLAUDE_CONFIG for isolated live turns")
	}
	config, project := t.TempDir(), t.TempDir()
	if data, err := os.ReadFile(filepath.Join(source, ".credentials.json")); err == nil {
		if err = os.WriteFile(filepath.Join(config, ".credentials.json"), data, 0600); err != nil {
			t.Fatal("cannot prepare isolated authentication")
		}
	}
	if data, err := os.ReadFile(filepath.Join(source, "settings.json")); err == nil {
		var settings struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(data, &settings) != nil {
			t.Fatal("cannot parse provider environment settings")
		}
		data, _ = json.Marshal(settings)
		if err = os.WriteFile(filepath.Join(config, "settings.json"), data, 0600); err != nil {
			t.Fatal("cannot prepare isolated provider settings")
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	projects := filepath.Join(config, "projects", "seed")
	if err := os.MkdirAll(projects, 0700); err != nil {
		t.Fatal(err)
	}
	seed, _ := json.Marshal(map[string]any{"type": "user", "uuid": "seed", "sessionId": interactiveSession, "cwd": project, "timestamp": time.Now().UTC().Format(time.RFC3339), "message": map[string]any{"role": "user", "content": "project index seed"}})
	if err := os.WriteFile(filepath.Join(projects, interactiveSession+".jsonl"), append(seed, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	group, err := platformprocess.NewGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	b, err := New(Options{ConfigDir: config, Executable: exe, Attach: group.Add, PollInterval: time.Hour, IdleTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	events := make(chan provider.Event, 256)
	if err = b.Start(ctx, func(e provider.Event) { events <- e }); err != nil {
		t.Fatal(err)
	}
	conversation, err := b.CreateConversation(ctx, ProjectID(project))
	if err != nil {
		t.Fatal(err)
	}
	deltas := 0
	for turn := 0; turn < 3; turn++ {
		if _, err = b.StartTurn(ctx, provider.TurnRequest{ConversationID: conversation.ID, Text: "Reply only WAW_CLAUDE_SMOKE_OK. Do not use tools."}); err != nil {
			t.Fatal(err)
		}
		timeout := time.NewTimer(90 * time.Second)
		finished := false
		for !finished {
			select {
			case e := <-events:
				if e.ConversationID != conversation.ID {
					continue
				}
				if e.Kind == provider.EventDelta {
					deltas++
				}
				if e.Kind == provider.EventTurnFailed {
					t.Fatal("isolated Claude turn failed")
				}
				finished = e.Kind == provider.EventTurnCompleted
			case <-timeout.C:
				t.Fatal("isolated Claude turn timed out")
			}
		}
		timeout.Stop()
		if err = b.refresh(); err != nil {
			t.Fatal(err)
		}
		timeline, err := b.Timeline(ctx, conversation.ID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range timeline.Items {
			for _, block := range item.Blocks {
				if item.Kind == "agentMessage" && strings.Contains(block.Text, "WAW_CLAUDE_SMOKE_OK") {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("model response missing from authoritative transcript")
		}
		if turn == 1 {
			b.mu.Lock()
			p := b.processes[conversation.ID]
			b.mu.Unlock()
			b.stopIdleProcesses(time.Now().Add(time.Minute))
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
				t.Fatal("idle process not reaped")
			}
		}
	}
	if deltas == 0 {
		t.Fatal("no live delta observed")
	}
	t.Log("isolated Claude create, three turns, streaming, history and idle resume verified")
}

// Opt-in, no model request and no user sessions/configuration are modified.
func TestClaudeManagedInitializeIntegration(t *testing.T) {
	exe := os.Getenv("WORK_ANYWHERE_TEST_CLAUDE")
	if exe == "" {
		t.Skip("set WORK_ANYWHERE_TEST_CLAUDE to run real managed initialization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	version, err := executableVersion(ctx, exe)
	if err != nil {
		t.Fatal(err)
	}
	group, err := platformprocess.NewGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	cmd := exec.CommandContext(ctx, exe, sessionArgs(newUUID(), false, provider.ConversationSettings{})...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(childEnvironment(os.Environ()), "CLAUDE_CONFIG_DIR="+t.TempDir())
	configureChild(cmd)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &diagnosticTail{}
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = group.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err = group.Add(cmd); err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(stdin, "{\"type\":\"control_request\",\"request_id\":\"waw-managed-init\",\"request\":{\"subtype\":\"initialize\"}}\n")
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var msg struct {
			Type     string
			Response struct {
				Subtype   string
				RequestID string `json:"request_id"`
			}
		}
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.Type == "control_response" && msg.Response.RequestID == "waw-managed-init" {
			if msg.Response.Subtype != "success" {
				t.Fatal("CLI rejected initialization")
			}
			t.Logf("Claude %s managed initialize succeeded", version)
			return
		}
	}
	t.Fatalf("no initialize response: %v (%s)", scanner.Err(), stderr.String())
}
