package remote

import (
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

// Provider backends publish deltas with a nested params map; the batcher must
// treat them exactly like App Server deltas.
func TestBackendDeltaPayloadIsRecognized(t *testing.T) {
	for name, kind := range map[string]string{"provider.item/agentMessage/delta": "assistantText", "provider.item/reasoning/textDelta": "reasoning"} {
		event := protocol.Event{Event: name, InstanceID: "claude-code:x", ConversationID: "c1", Sequence: 7, Payload: map[string]any{"params": map[string]any{"threadId": "c1", "turnId": "t1", "itemId": "msg:0", "delta": "Hi"}}}
		delta, ok := parseConversationDelta(event)
		if !ok || delta.kind != kind || delta.threadID != "c1" || delta.turnID != "t1" || delta.itemID != "msg:0" || delta.delta != "Hi" {
			t.Fatalf("%s parsed as %#v, %v", name, delta, ok)
		}
	}
}
