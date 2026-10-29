package daemon

import "github.com/hurf/work-anywhere/desktop/internal/platform/tray"

func (m *Manager) ManagementURL() string { return m.admin.OneTimeURL() }

func (m *Manager) ControlPaused() bool { return m.controlPaused.Load() }

func (m *Manager) ToggleControlPaused() {
	m.controlPaused.Store(!m.controlPaused.Load())
}

func (m *Manager) TrayInstances() []tray.InstanceStatus {
	snapshots := m.instanceSnapshots()
	result := make([]tray.InstanceStatus, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, tray.InstanceStatus{
			ID:             snapshot.ID,
			Name:           snapshot.Name,
			State:          snapshot.State,
			RuntimeVersion: snapshot.Runtime.Version,
			ActiveTurn:     snapshot.ActiveTurn,
		})
	}
	// Backend agents appear too so tray exit warns about their running turns.
	for _, backend := range m.backendList() {
		agent := backend.Agent()
		result = append(result, tray.InstanceStatus{ID: agent.ID, Name: agent.Name, State: agent.Status, ActiveTurn: backend.ActiveTurns() > 0})
	}
	return result
}

func (m *Manager) RestartErroredInstances() int { return m.restartErroredInstances() }

func (m *Manager) Shutdown(interrupt bool) { go m.gracefulShutdown(interrupt) }
