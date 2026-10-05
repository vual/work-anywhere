package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

func (i *Instance) controlThreadLock(threadID string) *sync.Mutex {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.controlWrites == nil {
		i.controlWrites = make(map[string]*sync.Mutex)
	}
	if i.controlWrites[threadID] == nil {
		i.controlWrites[threadID] = &sync.Mutex{}
	}
	return i.controlWrites[threadID]
}

// Called with i.mu held. Projections are intentionally scoped to this daemon process.
func (i *Instance) projectionLocked(threadID string) *conversationProjection {
	if p := i.conversations[threadID]; p != nil {
		p.LastAccess = time.Now()
		return p
	}
	i.pruneProjectionsLocked()
	p := &conversationProjection{ID: threadID, Status: "idle", LastAccess: time.Now()}
	i.conversations[threadID] = p
	return p
}

func (i *Instance) projectionPinnedLocked(threadID string) bool {
	if i.activeTurns[threadID] != "" || i.pendingTurnStarts[threadID] || i.pendingTurnSettings[threadID].Mode != "" {
		return true
	}
	for _, action := range i.actions {
		if action.ConversationID == threadID {
			return true
		}
	}
	return false
}

func (i *Instance) pruneProjectionsLocked() {
	const maxInactive = 256
	for {
		inactive := 0
		for id := range i.conversations {
			if !i.projectionPinnedLocked(id) {
				inactive++
			}
		}
		if inactive < maxInactive {
			return
		}
		oldestID := ""
		var oldest time.Time
		for id, projection := range i.conversations {
			if i.projectionPinnedLocked(id) {
				continue
			}
			if oldestID != "" && !projection.LastAccess.Before(oldest) {
				continue
			}
			oldestID, oldest = id, projection.LastAccess
		}
		if oldestID == "" {
			return
		}
		projection := i.conversations[oldestID]
		for _, size := range projection.EventBytes {
			if size > 0 {
				i.manager.eventPayloadBytes.Add(-int64(size))
			}
		}
		delete(i.conversations, oldestID)
	}
}

func revisionEvent(event string) bool {
	switch strings.TrimPrefix(event, "provider.") {
	case "frontend.turn.start", "frontend.turn.failed", "frontend.thread.state.reset", "frontend.thread.settings.updated", "frontend.thread.queue.updated",
		"action.created", "action.resolved", "serverRequest/resolved",
		"turn/started", "turn/completed", "turn/failed", "turn/interrupted",
		"thread/started", "thread/status/changed", "thread/settings/updated", "thread/queue/updated",
		"thread/archived", "thread/unarchived", "thread/closed", "thread/rollback", "thread/history/updated":
		return true
	}
	return false
}

func (i *Instance) canStartTurnLocked(threadID string) bool {
	if i.stdin == nil || threadID == "" || i.state == "starting" || i.state == "waiting_initialize" || i.state == "stopped" || i.state == "error" || i.state == "upgrade_pending" {
		return false
	}
	if _, ok := i.activeTurns[threadID]; ok {
		return false
	}
	if _, ok := i.pendingTurnSettings[threadID]; ok {
		return false
	}
	if i.pendingTurnStarts[threadID] {
		return false
	}
	for _, a := range i.actions {
		if a.ConversationID == "" || a.ConversationID == threadID {
			return false
		}
	}
	return true
}

func (i *Instance) reserveTurn(params json.RawMessage) error {
	var p struct {
		ThreadID         string `json:"threadId"`
		ExpectedRevision *int64 `json:"expectedRevision"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.canStartTurnLocked(p.ThreadID) {
		return &mobileContractError{code: "conversation_busy", message: "a turn is already active or waiting for an action"}
	}
	current := i.projectionLocked(p.ThreadID).Revision
	if p.ExpectedRevision != nil && current != *p.ExpectedRevision {
		return &mobileContractError{code: "revision_conflict", message: fmt.Sprintf("revision conflict: expected %d, current %d", *p.ExpectedRevision, current)}
	}
	if i.pendingTurnStarts == nil {
		i.pendingTurnStarts = make(map[string]bool)
	}
	i.pendingTurnStarts[p.ThreadID] = true
	return nil
}

func (i *Instance) rememberCreatedThread(threadID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.createdThreads == nil {
		i.createdThreads = make(map[string]bool)
	}
	i.createdThreads[threadID] = true
}

func (i *Instance) emptyCreatedThread(threadID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.createdThreads[threadID] && !i.pendingTurnStarts[threadID] && i.activeTurns[threadID] == "" && i.pendingTurnSettings[threadID].Mode == ""
}
