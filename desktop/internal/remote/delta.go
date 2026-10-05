package remote

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

const (
	maxDeltaStreams     = 128
	maxDeltaBufferBytes = 8 << 20
	maxDeltaBatchBytes  = 32 << 10
)

type conversationDeltaPayload struct {
	Version      int    `json:"version"`
	Kind         string `json:"kind"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	ContentIndex *int64 `json:"contentIndex,omitempty"`
	SummaryIndex *int64 `json:"summaryIndex,omitempty"`
	Stream       string `json:"stream,omitempty"`
	FromSequence int64  `json:"fromSequence"`
	ToSequence   int64  `json:"toSequence"`
	Delta        string `json:"delta"`
}

type parsedDelta struct {
	kind                       string
	threadID, turnID, itemID   string
	stream                     string
	contentIndex, summaryIndex *int64
	delta                      string
}

func (d parsedDelta) key() string {
	content, summary := int64(-1), int64(-1)
	if d.contentIndex != nil {
		content = *d.contentIndex
	}
	if d.summaryIndex != nil {
		summary = *d.summaryIndex
	}
	return d.threadID + "\x00" + d.turnID + "\x00" + d.itemID + "\x00" + d.kind + "\x00" + d.stream + "\x00" + jsonNumber(content) + "\x00" + jsonNumber(summary)
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type deltaBatch struct {
	key       string
	event     protocol.Event
	payload   conversationDeltaPayload
	createdAt time.Time
	deadline  time.Time
}

type deltaBatcher struct {
	byConversation map[string]*deltaBatch
	totalBytes     int
}

func newDeltaBatcher() *deltaBatcher {
	return &deltaBatcher{byConversation: make(map[string]*deltaBatch)}
}

func (b *deltaBatcher) add(event protocol.Event, delta parsedDelta, now time.Time) []protocol.Event {
	var flushed []protocol.Event
	conversationID := event.ConversationID
	if conversationID == "" {
		conversationID = delta.threadID
	}
	scopeKey := deltaConversationKey(event.InstanceID, conversationID)
	streamKey := event.InstanceID + "\x00" + delta.key()
	if current := b.byConversation[scopeKey]; current != nil && current.key != streamKey {
		flushed = append(flushed, b.flushKey(scopeKey)...)
	}
	if len(delta.delta) > maxDeltaBatchBytes {
		flushed = append(flushed, b.flushKey(scopeKey)...)
		event.Event = "conversation.invalidated"
		event.Payload = nil
		return append(flushed, event)
	}
	if current := b.byConversation[scopeKey]; current != nil && len(current.payload.Delta)+len(delta.delta) > deltaSizeLimit(delta.kind) {
		flushed = append(flushed, b.flushKey(scopeKey)...)
	}
	if b.byConversation[scopeKey] == nil {
		for len(b.byConversation) >= maxDeltaStreams || b.totalBytes+len(delta.delta) > maxDeltaBufferBytes {
			oldest := b.oldestConversation()
			if oldest == "" {
				break
			}
			flushed = append(flushed, b.flushKey(oldest)...)
		}
		b.byConversation[scopeKey] = &deltaBatch{
			key: streamKey, event: event, createdAt: now, deadline: now.Add(deltaDelay(delta.kind)),
			payload: conversationDeltaPayload{Version: 1, Kind: delta.kind, TurnID: delta.turnID, ItemID: delta.itemID, ContentIndex: delta.contentIndex, SummaryIndex: delta.summaryIndex, Stream: delta.stream, FromSequence: event.Sequence, ToSequence: event.Sequence},
		}
	}
	batch := b.byConversation[scopeKey]
	batch.payload.Delta += delta.delta
	batch.payload.ToSequence = event.Sequence
	batch.event.Sequence = event.Sequence
	b.totalBytes += len(delta.delta)
	if shouldFlushDelta(batch.payload.Kind, batch.payload.Delta) {
		flushed = append(flushed, b.flushKey(scopeKey)...)
	}
	return flushed
}

func deltaConversationKey(instanceID, conversationID string) string {
	return instanceID + "\x00" + conversationID
}

func (b *deltaBatcher) flush(instanceID, conversationID string) []protocol.Event {
	return b.flushKey(deltaConversationKey(instanceID, conversationID))
}

func (b *deltaBatcher) flushKey(scopeKey string) []protocol.Event {
	batch := b.byConversation[scopeKey]
	if batch == nil {
		return nil
	}
	delete(b.byConversation, scopeKey)
	b.totalBytes -= len(batch.payload.Delta)
	batch.event.Event = "conversation.delta"
	batch.event.Payload = batch.payload
	return []protocol.Event{batch.event}
}

func (b *deltaBatcher) flushDue(now time.Time) []protocol.Event {
	var result []protocol.Event
	for scopeKey, batch := range b.byConversation {
		if !now.Before(batch.deadline) {
			result = append(result, b.flushKey(scopeKey)...)
		}
	}
	return result
}

func (b *deltaBatcher) flushAll() []protocol.Event {
	var result []protocol.Event
	for scopeKey := range b.byConversation {
		result = append(result, b.flushKey(scopeKey)...)
	}
	return result
}

func (b *deltaBatcher) oldestConversation() string {
	oldestID := ""
	var oldest time.Time
	for scopeKey, batch := range b.byConversation {
		if oldestID == "" || batch.createdAt.Before(oldest) {
			oldestID, oldest = scopeKey, batch.createdAt
		}
	}
	return oldestID
}

func parseConversationDelta(event protocol.Event) (parsedDelta, bool) {
	lower := strings.ToLower(strings.TrimPrefix(event.Event, "provider."))
	kind := ""
	switch lower {
	case "item/agentmessage/delta":
		kind = "assistantText"
	case "item/plan/delta":
		kind = "plan"
	case "item/reasoning/textdelta":
		kind = "reasoning"
	case "item/reasoning/summarytextdelta":
		kind = "reasoningSummary"
	case "item/commandexecution/outputdelta":
		kind = "commandOutput"
	case "item/filechange/outputdelta":
		kind = "fileChangeOutput"
	default:
		return parsedDelta{}, false
	}
	var params json.RawMessage
	switch payload := event.Payload.(type) {
	case map[string]any:
		switch value := payload["params"].(type) {
		case json.RawMessage:
			params = value
		case []byte:
			params = value
		default:
			params, _ = json.Marshal(value)
		}
	case json.RawMessage:
		params = payload
	default:
		encoded, _ := json.Marshal(payload)
		var envelope struct {
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(encoded, &envelope)
		params = envelope.Params
	}
	var value struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		ItemID       string `json:"itemId"`
		Delta        string `json:"delta"`
		Stream       string `json:"stream"`
		ContentIndex *int64 `json:"contentIndex"`
		SummaryIndex *int64 `json:"summaryIndex"`
	}
	if json.Unmarshal(params, &value) != nil || value.ThreadID == "" || value.TurnID == "" || value.ItemID == "" || value.Delta == "" {
		return parsedDelta{}, false
	}
	return parsedDelta{kind: kind, threadID: value.ThreadID, turnID: value.TurnID, itemID: value.ItemID, delta: value.Delta, stream: value.Stream, contentIndex: value.ContentIndex, summaryIndex: value.SummaryIndex}, true
}

func deltaDelay(kind string) time.Duration {
	if kind == "commandOutput" || kind == "fileChangeOutput" {
		return 200 * time.Millisecond
	}
	return 250 * time.Millisecond
}

func deltaSizeLimit(kind string) int {
	switch kind {
	case "assistantText":
		return 2 << 10
	case "commandOutput", "fileChangeOutput":
		return 8 << 10
	default:
		return 4 << 10
	}
}

func shouldFlushDelta(kind, value string) bool {
	if len(value) >= maxDeltaBatchBytes || len(value) >= deltaSizeLimit(kind) {
		return true
	}
	if kind == "commandOutput" || kind == "fileChangeOutput" {
		return len(value) >= 2<<10 && strings.HasSuffix(value, "\n")
	}
	if strings.Contains(value, "\n\n") {
		return true
	}
	if len(value) < 256 {
		return false
	}
	trimmed := strings.TrimSpace(value)
	return strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, "!") || strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "。") || strings.HasSuffix(trimmed, "！") || strings.HasSuffix(trimmed, "？")
}
