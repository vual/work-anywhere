package remote

import (
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func TestUpdateEventsStayLocal(t *testing.T) {
	if !isLocalOnlyEvent(protocol.Event{Event: "system.update.changed"}) {
		t.Fatal("system.update.changed must not be forwarded to Mobile")
	}
	if isLocalOnlyEvent(protocol.Event{Event: "turn.completed"}) {
		t.Fatal("conversation events must still be forwarded")
	}
}
