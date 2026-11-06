package daemon

import (
	"sort"

	"github.com/hurf/work-anywhere/desktop/internal/platform/autostart"
	"github.com/hurf/work-anywhere/desktop/internal/platform/installation"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
	"github.com/hurf/work-anywhere/desktop/internal/provider/claudecode"
)

// Reasons a backend agent is not running, shown on the local administration page.
const (
	claudeCodeDisabled    = "disabled"
	claudeCodeNotFound    = "not_found"
	claudeCodeStartFailed = "start_failed"
)

type inactiveBackend struct {
	Reason    string
	ConfigDir string
	Error     string
}

// adminAgentSnapshot is the local administration view of a backend agent. It
// carries local paths and is never returned to Mobile callers.
type adminAgentSnapshot struct {
	ID             string                  `json:"id,omitempty"`
	Name           string                  `json:"name"`
	Kind           string                  `json:"kind"`
	Status         string                  `json:"status"`
	ActiveTurn     bool                    `json:"activeTurn"`
	PendingActions int                     `json:"pendingActions"`
	Reason         string                  `json:"reason,omitempty"`
	Error          string                  `json:"error,omitempty"`
	ConfigDir      string                  `json:"configDir,omitempty"`
	Claude         *claudecode.Diagnostics `json:"claude,omitempty"`
}

func (m *Manager) setClaudeCodeInactive(reason, configDir, message string) {
	m.backendMu.Lock()
	m.claudeCodeInactive = &inactiveBackend{Reason: reason, ConfigDir: configDir, Error: message}
	m.backendMu.Unlock()
}

// agentSnapshots lists registered backend agents plus a placeholder for a
// supported backend that is not running, so the page can explain why.
func (m *Manager) agentSnapshots() []adminAgentSnapshot {
	m.backendMu.Lock()
	backends := make([]provider.Backend, 0, len(m.backends))
	for _, backend := range m.backends {
		backends = append(backends, backend)
	}
	inactive := m.claudeCodeInactive
	m.backendMu.Unlock()

	result := make([]adminAgentSnapshot, 0, len(backends)+1)
	hasClaude := false
	for _, backend := range backends {
		agent := backend.Agent()
		snapshot := adminAgentSnapshot{ID: agent.ID, Name: agent.Name, Kind: agent.Kind, Status: agent.Status, ActiveTurn: agent.ActiveTurn, PendingActions: agent.PendingActions}
		if reporter, ok := backend.(interface{ Diagnostics() claudecode.Diagnostics }); ok {
			diagnostics := reporter.Diagnostics()
			snapshot.Claude = &diagnostics
			snapshot.ConfigDir = diagnostics.ConfigDir
		}
		if agent.Kind == claudecode.Kind {
			hasClaude = true
			if inactive != nil && inactive.Reason == claudeCodeStartFailed {
				snapshot.Reason, snapshot.Error = inactive.Reason, inactive.Error
			}
		}
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if !hasClaude && inactive != nil {
		result = append(result, adminAgentSnapshot{Name: "Claude Code", Kind: claudecode.Kind, Status: "inactive", Reason: inactive.Reason, Error: inactive.Error, ConfigDir: inactive.ConfigDir})
	}
	return result
}

type startupStatus struct {
	Autostart        autostart.Status          `json:"autostart"`
	AutostartError   string                    `json:"autostartError,omitempty"`
	CodexIntegration installation.BridgeStatus `json:"codexIntegration"`
}

func startupSnapshot() startupStatus {
	var status startupStatus
	var err error
	if status.Autostart, err = autostart.Get(); err != nil {
		status.AutostartError = err.Error()
	}
	status.CodexIntegration, _ = installation.Status()
	return status
}
