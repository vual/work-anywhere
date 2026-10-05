package remote

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func deltaEvent(sequence int64, thread, item, method, delta string) protocol.Event {
	params, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": "turn-1", "itemId": item, "delta": delta})
	return protocol.Event{Type: "event", Event: "provider." + method, InstanceID: "agent-1", ConversationID: thread, Sequence: sequence, SyncEpoch: "epoch-1", Payload: map[string]any{"method": method, "params": json.RawMessage(params)}}
}

func TestDeltaBatcherMergesTextAndFlushesOnKindChange(t *testing.T) {
	batcher := newDeltaBatcher()
	now := time.Unix(1, 0)
	first := deltaEvent(1, "thread-1", "item-1", "item/agentMessage/delta", "Hello ")
	second := deltaEvent(2, "thread-1", "item-1", "item/agentMessage/delta", "world")
	for _, event := range []protocol.Event{first, second} {
		delta, ok := parseConversationDelta(event)
		if !ok || len(batcher.add(event, delta, now)) != 0 {
			t.Fatal("short text flushed before its deadline")
		}
	}
	command := deltaEvent(3, "thread-1", "command-1", "item/commandExecution/outputDelta", "ok\n")
	delta, ok := parseConversationDelta(command)
	if !ok {
		t.Fatal("command delta was not recognized")
	}
	flushed := batcher.add(command, delta, now)
	if len(flushed) != 1 || flushed[0].Event != "conversation.delta" {
		t.Fatalf("kind change flush = %#v", flushed)
	}
	payload := flushed[0].Payload.(conversationDeltaPayload)
	if payload.Delta != "Hello world" || payload.FromSequence != 1 || payload.ToSequence != 2 || payload.Kind != "assistantText" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestDeltaBatcherUsesSemanticAndTimeBoundaries(t *testing.T) {
	batcher := newDeltaBatcher()
	now := time.Unix(1, 0)
	paragraph := deltaEvent(1, "thread-1", "item-1", "item/agentMessage/delta", "First paragraph.\n\n")
	delta, _ := parseConversationDelta(paragraph)
	if flushed := batcher.add(paragraph, delta, now); len(flushed) != 1 {
		t.Fatalf("paragraph boundary did not flush: %#v", flushed)
	}
	short := deltaEvent(2, "thread-1", "item-2", "item/plan/delta", "step")
	delta, _ = parseConversationDelta(short)
	if flushed := batcher.add(short, delta, now); len(flushed) != 0 {
		t.Fatal("short plan flushed immediately")
	}
	if flushed := batcher.flushDue(now.Add(249 * time.Millisecond)); len(flushed) != 0 {
		t.Fatal("plan flushed before deadline")
	}
	if flushed := batcher.flushDue(now.Add(250 * time.Millisecond)); len(flushed) != 1 {
		t.Fatal("plan did not flush at deadline")
	}
}

func TestDeltaBatcherScopesIdenticalConversationIDsByInstance(t *testing.T) {
	batcher := newDeltaBatcher()
	now := time.Unix(1, 0)
	first := deltaEvent(1, "thread-1", "item-1", "item/agentMessage/delta", "one")
	second := deltaEvent(1, "thread-1", "item-1", "item/agentMessage/delta", "two")
	second.InstanceID = "agent-2"
	for _, event := range []protocol.Event{first, second} {
		delta, _ := parseConversationDelta(event)
		if flushed := batcher.add(event, delta, now); len(flushed) != 0 {
			t.Fatalf("another instance flushed this stream: %#v", flushed)
		}
	}
	flushed := batcher.flushAll()
	if len(flushed) != 2 {
		t.Fatalf("scoped batches = %d, want 2", len(flushed))
	}
	seen := map[string]string{}
	for _, event := range flushed {
		seen[event.InstanceID] = event.Payload.(conversationDeltaPayload).Delta
	}
	if seen["agent-1"] != "one" || seen["agent-2"] != "two" {
		t.Fatalf("cross-instance delta mix: %#v", seen)
	}
}

func TestDeltaBatcherBoundsConcurrentStreamsAndVolume(t *testing.T) {
	batcher := newDeltaBatcher()
	now := time.Unix(1, 0)
	flushed := 0
	for index := 0; index < 100000; index++ {
		thread := "thread-" + string(rune('a'+index%4))
		event := deltaEvent(int64(index/4+1), thread, "item-1", "item/agentMessage/delta", "token ")
		delta, _ := parseConversationDelta(event)
		flushed += len(batcher.add(event, delta, now))
		if batcher.totalBytes > maxDeltaBufferBytes || len(batcher.byConversation) > maxDeltaStreams {
			t.Fatalf("batcher exceeded bounds: bytes=%d streams=%d", batcher.totalBytes, len(batcher.byConversation))
		}
	}
	flushed += len(batcher.flushAll())
	if flushed >= 5000 {
		t.Fatalf("100000 deltas produced too many encrypted batches: %d", flushed)
	}
}
