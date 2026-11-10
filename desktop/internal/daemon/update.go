package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/platform/tray"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/update"
)

const updateChangedEvent = "system.update.changed"

// startUpdates creates the self-update service; it never fails the daemon.
func (m *Manager) startUpdates(ctx context.Context, dataDir string) {
	m.update = update.New(update.Options{
		CurrentVersion: m.version,
		Dir:            filepath.Join(dataDir, "updates"),
		Store:          m.settings,
		Log:            m.log,
		Platform:       update.SystemPlatform{},
		Preflight:      m.updatePreflight,
		Launched: func(interrupt bool) {
			time.Sleep(100 * time.Millisecond)
			m.gracefulShutdown(interrupt)
		},
		OnChange: m.updateChanged,
	})
	go m.update.Run(ctx)
}

func (m *Manager) updateChanged(status update.Status) {
	m.publish(protocol.Event{Type: "event", SyncEpoch: m.syncEpoch, Event: updateChangedEvent, Payload: status})
	m.notifyUpdate(status)
}

// activeTurnCount counts running turns across bridged and backend agents.
func (m *Manager) activeTurnCount() int {
	active := m.backendActiveTurns()
	for _, item := range m.instanceSnapshots() {
		if item.ActiveTurn {
			active++
		}
	}
	return active
}

// updatePreflight blocks the installer while Codex Desktop holds the bridge
// EXE open, or while turns run without confirmation.
func (m *Manager) updatePreflight(confirmInterrupt bool) error {
	for _, item := range m.instanceSnapshots() {
		if item.DesktopConnections > 0 {
			return update.NewError(update.CodeBlockedByBridge, "close Codex Desktop before updating; its bridge process uses the Work Anywhere executable")
		}
	}
	if active := m.activeTurnCount(); active > 0 && !confirmInterrupt {
		return update.NewError(update.CodeActiveTurns, fmt.Sprintf("%d turn(s) are active; confirm to interrupt them", active))
	}
	return nil
}

func isUpdateMethod(method string) bool {
	return strings.HasPrefix(method, "system.update.")
}

func isMutatingUpdateMethod(method string) bool {
	return method == "system.update.apply" || method == "system.update.skip" || method == "system.update.settings"
}

// updateControl serves system.update.* for the local management contract.
func (m *Manager) updateControl(request protocol.ControlRequest, response *protocol.ControlResponse) {
	if request.Contract == protocol.MobileControlContract {
		response.Error = &protocol.ControlError{Code: "forbidden", Message: "desktop updates can only be managed locally"}
		return
	}
	if m.update == nil {
		response.Error = &protocol.ControlError{Code: "unavailable", Message: "update service is not running"}
		return
	}
	var params struct {
		Version          string  `json:"version"`
		ConfirmInterrupt bool    `json:"confirmInterrupt"`
		AutoCheck        *bool   `json:"autoCheck"`
		MirrorBase       *string `json:"mirrorBase"`
	}
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			response.Error = &protocol.ControlError{Code: "invalid_params", Message: err.Error()}
			return
		}
	}
	var (
		status update.Status
		err    error
	)
	switch request.Method {
	case "system.update.status":
		status = m.update.Status()
	case "system.update.check":
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		status, err = m.update.Check(ctx)
		cancel()
	case "system.update.apply":
		status, err = m.update.Apply(params.Version, params.ConfirmInterrupt)
	case "system.update.cancel":
		status = m.update.Cancel()
	case "system.update.skip":
		status, err = m.update.Skip(params.Version)
	case "system.update.settings":
		status, err = m.update.UpdateSettings(params.AutoCheck, params.MirrorBase)
	default:
		response.Error = &protocol.ControlError{Code: "method_not_found", Message: request.Method}
		return
	}
	if err != nil {
		code, message := update.ErrorCode(err, update.CodeManifestUnavailable)
		response.Error = &protocol.ControlError{Code: code, Message: message}
		return
	}
	response.Result = status
}

// notifyUpdate announces an available version once per version on the tray.
func (m *Manager) notifyUpdate(status update.Status) {
	if m.manualUpdateCheck.Load() || m.update == nil {
		return
	}
	if version, ok := m.update.ShouldNotify(); ok {
		tray.Notify(tray.Notice{Title: "Work Anywhere 有新版本", Message: fmt.Sprintf("新版本 %s 可用，点击打开管理页进行更新。", version)})
	}
}

// CheckForUpdates serves the tray "检查更新" item and always reports the result.
func (m *Manager) CheckForUpdates() {
	if m.update == nil {
		return
	}
	m.manualUpdateCheck.Store(true)
	defer m.manualUpdateCheck.Store(false)
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	status, err := m.update.Check(ctx)
	cancel()
	notice := tray.Notice{Title: "Work Anywhere 更新"}
	switch {
	case err != nil:
		_, message := update.ErrorCode(err, update.CodeManifestUnavailable)
		notice.Message = "检查更新失败：" + message
	case status.Latest != nil && status.State != update.StateUpToDate:
		m.update.ShouldNotify()
		notice.Message = fmt.Sprintf("新版本 %s 可用，点击打开管理页进行更新。", status.Latest.Version)
	default:
		notice.Message = fmt.Sprintf("当前版本 %s 已是最新。", status.CurrentVersion)
	}
	tray.Notify(notice)
}
