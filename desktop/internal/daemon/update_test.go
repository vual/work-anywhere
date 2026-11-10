package daemon

import (
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/update"
)

func TestUpdateControlRejectsMobileContract(t *testing.T) {
	m := &Manager{update: update.New(update.Options{CurrentVersion: "1.0.0"})}
	response := m.control(protocol.ControlRequest{Type: "request", ID: "1", Contract: protocol.MobileControlContract, Method: "system.update.status"})
	if response.Error == nil || response.Error.Code != "forbidden" {
		t.Fatalf("mobile update control = %#v, want forbidden", response.Error)
	}
	response = m.control(protocol.ControlRequest{Type: "request", ID: "2", Method: "system.update.status"})
	if response.Error != nil {
		t.Fatalf("local update status error = %#v", response.Error)
	}
	if status, ok := response.Result.(update.Status); !ok || status.CurrentVersion != "1.0.0" {
		t.Fatalf("local update status = %#v", response.Result)
	}
}

func TestUpdateMutationsRespectControlPause(t *testing.T) {
	m := &Manager{update: update.New(update.Options{CurrentVersion: "1.0.0"})}
	m.controlPaused.Store(true)
	for _, method := range []string{"system.update.apply", "system.update.skip", "system.update.settings"} {
		response := m.control(protocol.ControlRequest{Type: "request", ID: method, Method: method})
		if response.Error == nil || response.Error.Code != "control_paused" {
			t.Fatalf("%s while paused = %#v", method, response.Error)
		}
	}
	if response := m.control(protocol.ControlRequest{Type: "request", ID: "s", Method: "system.update.status"}); response.Error != nil {
		t.Fatalf("status while paused = %#v", response.Error)
	}
}
