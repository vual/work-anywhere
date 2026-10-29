package claudecode

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

func TestProcessDiagnosticBoundedAndExitPreserved(t *testing.T) {
	h := newInteractiveHarness(t)
	h.start(t, "crash diagnostic")
	h.waitFor(t, provider.EventTurnFailed)
	h.backend.mu.Lock()
	detail := h.backend.lastProcessError
	h.backend.mu.Unlock()
	if !strings.Contains(detail, "exitCode=23") || !strings.Contains(detail, "CLAUDE_DIAGNOSTIC_END") || len(detail) > maxProcessDiagnostic+256 {
		t.Fatalf("unbounded or incomplete exit diagnostic (%d bytes)", len(detail))
	}
	h.start(t, "hello")
	h.waitFor(t, provider.EventTurnCompleted)
	h.backend.mu.Lock()
	detail = h.backend.lastProcessError
	h.backend.mu.Unlock()
	if detail != "" {
		t.Fatal("old process diagnostic contaminated replacement")
	}
}

func TestAttachFailureDoesNotSendOrRegisterProcess(t *testing.T) {
	h := newInteractiveHarness(t)
	var diagnostic ProcessDiagnostic
	h.backend.onProcessDiagnostic = func(d ProcessDiagnostic) { diagnostic = d }
	h.backend.attach = func(*exec.Cmd) error {
		return &platformprocess.JobError{Stage: "assign process to job", Code: 5, Err: errors.New("access denied")}
	}
	_, err := h.backend.StartTurn(context.Background(), provider.TurnRequest{ConversationID: interactiveSession, Text: "must never be sent"})
	if err == nil || !strings.Contains(err.Error(), "win32=5") {
		t.Fatal("attach failure lost")
	}
	if diagnostic.Stage != "assign process to job" || diagnostic.Win32Code == nil || *diagnostic.Win32Code != 5 {
		t.Fatalf("diagnostic metadata: %+v", diagnostic)
	}
	if len(h.backend.processes) != 0 || h.backend.ActiveTurns() != 0 {
		t.Fatal("failed process registered or turn started")
	}
}

func TestDiagnosticTailDrainsHugeWrites(t *testing.T) {
	d := &diagnosticTail{}
	p := []byte(strings.Repeat("x", 10*maxProcessDiagnostic))
	if n, err := d.Write(p); n != len(p) || err != nil {
		t.Fatal("stderr not drained")
	}
	_, _ = d.Write([]byte("tail"))
	if len(d.data) != maxProcessDiagnostic || cap(d.data) != maxProcessDiagnostic || !strings.HasSuffix(d.String(), "tail") {
		t.Fatal("tail is not bounded")
	}
}
