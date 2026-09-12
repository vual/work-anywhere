package devmobile

import (
	"encoding/json"
	"fmt"
	"testing"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
)

func TestRouteAckCorrelatesByOuterMessageID(t *testing.T) {
	client := New()
	waiter := make(chan routeResult, 1)
	client.pendingRoutes["message-1"] = waiter
	if !client.handleRouteAck(&relayv1.RouteAck{RoutedMessageId: []byte("message-1"), Accepted: false, ErrorCode: "queue_full"}) {
		t.Fatal("known route ack was not handled")
	}
	result := <-waiter
	if result.accepted || result.code != "queue_full" || len(client.pendingRoutes) != 0 {
		t.Fatalf("unexpected route result: %#v", result)
	}
	if client.handleRouteAck(&relayv1.RouteAck{RoutedMessageId: []byte("late"), Accepted: true}) {
		t.Fatal("late or unknown ACK must not be correlated")
	}
}

func TestEventSinkReceivesOnlyEventsAndCurrentTransportState(t *testing.T) {
	client := New()
	received := make([]string, 0, 2)
	client.SetEventSink(func(value string) { received = append(received, value) })
	client.dispatchControlPayload(json.RawMessage(`{"type":"response","id":"late","result":{"secret":true}}`))
	client.dispatchControlPayload(json.RawMessage(`{"type":"unknown","value":"ignored"}`))
	client.dispatchControlPayload(json.RawMessage(`{"type":"event","sequence":7,"event":"turn.completed"}`))
	if len(received) != 2 || received[0] != `{"state":"disconnected","type":"transport"}` || received[1] != `{"type":"event","sequence":7,"event":"turn.completed"}` {
		t.Fatalf("unexpected event stream: %#v", received)
	}
}

func TestBufferedEventsAreBoundedAndDrainedInOrder(t *testing.T) {
	client := New()
	for index := 0; index < 1002; index++ {
		client.dispatchControlPayload(json.RawMessage(fmt.Sprintf(`{"type":"event","sequence":%d}`, index)))
	}
	received := make([]string, 0, 1001)
	client.SetEventSink(func(value string) { received = append(received, value) })
	if len(received) != 1001 {
		t.Fatalf("received %d envelopes, want transport plus 1000 events", len(received))
	}
	if received[1] != `{"type":"event","sequence":2}` || received[len(received)-1] != `{"type":"event","sequence":1001}` {
		t.Fatalf("buffer did not retain the newest events in order: first=%s last=%s", received[1], received[len(received)-1])
	}
	if len(client.Status().Events) != 0 {
		t.Fatal("buffer must be empty after attaching a sink")
	}
}

func TestKnownResponseIsCorrelatedAndNotEmitted(t *testing.T) {
	client := New()
	waiter := make(chan json.RawMessage, 1)
	client.pending["request-1"] = waiter
	client.dispatchControlPayload(json.RawMessage(`{"type":"response","id":"request-1","result":{"ok":true}}`))
	select {
	case response := <-waiter:
		if string(response) != `{"type":"response","id":"request-1","result":{"ok":true}}` {
			t.Fatalf("unexpected response: %s", response)
		}
	default:
		t.Fatal("known response was not correlated")
	}
	if len(client.Status().Events) != 0 {
		t.Fatal("responses must never enter the event buffer")
	}
}
