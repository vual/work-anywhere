package devmobile

import (
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
