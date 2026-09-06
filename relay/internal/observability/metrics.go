package observability

import (
	"fmt"
	"io"
	"sync/atomic"
)

type Metrics struct {
	AuthSuccess         atomic.Uint64
	AuthFailure         atomic.Uint64
	EnrollmentSuccess   atomic.Uint64
	EnrollmentFailure   atomic.Uint64
	PairingSuccess      atomic.Uint64
	PairingFailure      atomic.Uint64
	PairingRevoked      atomic.Uint64
	FramesRouted        atomic.Uint64
	RouteFailure        atomic.Uint64
	NotificationsQueued atomic.Uint64
	NotificationFailure atomic.Uint64
}

func (m *Metrics) WritePrometheus(writer io.Writer, activeConnections int64) {
	values := []struct {
		name  string
		value uint64
	}{
		{"work_anywhere_relay_auth_success_total", m.AuthSuccess.Load()},
		{"work_anywhere_relay_auth_failure_total", m.AuthFailure.Load()},
		{"work_anywhere_relay_enrollment_success_total", m.EnrollmentSuccess.Load()},
		{"work_anywhere_relay_enrollment_failure_total", m.EnrollmentFailure.Load()},
		{"work_anywhere_relay_pairing_success_total", m.PairingSuccess.Load()},
		{"work_anywhere_relay_pairing_failure_total", m.PairingFailure.Load()},
		{"work_anywhere_relay_pairing_revoked_total", m.PairingRevoked.Load()},
		{"work_anywhere_relay_frames_routed_total", m.FramesRouted.Load()},
		{"work_anywhere_relay_route_failure_total", m.RouteFailure.Load()},
		{"work_anywhere_relay_notifications_queued_total", m.NotificationsQueued.Load()},
		{"work_anywhere_relay_notification_failure_total", m.NotificationFailure.Load()},
	}
	_, _ = fmt.Fprintf(writer, "# TYPE work_anywhere_relay_active_connections gauge\nwork_anywhere_relay_active_connections %d\n", activeConnections)
	for _, value := range values {
		_, _ = fmt.Fprintf(writer, "# TYPE %s counter\n%s %d\n", value.name, value.name, value.value)
	}
}
