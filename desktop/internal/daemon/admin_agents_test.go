package daemon

import (
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/provider/claudecode"
)

func TestAgentSnapshotsIncludesRegisteredBackends(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	snapshots := manager.agentSnapshots()
	if len(snapshots) != 1 || snapshots[0].ID != "fake:agent" || snapshots[0].Status != "ready" || snapshots[0].Claude != nil {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestAgentSnapshotsExplainsInactiveClaudeCode(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	manager.setClaudeCodeInactive(claudeCodeNotFound, `C:\Users\test\.claude`, "")
	snapshots := manager.agentSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	placeholder := snapshots[1]
	if placeholder.Kind != claudecode.Kind || placeholder.Status != "inactive" || placeholder.Reason != claudeCodeNotFound || placeholder.ConfigDir != `C:\Users\test\.claude` || placeholder.ID != "" {
		t.Fatalf("placeholder = %#v", placeholder)
	}
}

func TestAgentSnapshotsWithoutClaudeReason(t *testing.T) {
	manager, _ := newBackendTestManager(t)
	for _, snapshot := range manager.agentSnapshots() {
		if snapshot.Kind == claudecode.Kind {
			t.Fatalf("unexpected Claude Code placeholder: %#v", snapshot)
		}
	}
}
