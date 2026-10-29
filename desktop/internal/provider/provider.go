// Package provider defines the Mobile-facing conversation model shared by the
// daemon and every agent backend. Codex is still served by the daemon's
// app-server projection; other agents implement Backend directly and return
// these provider-neutral DTOs.
package provider

import (
	"context"
	"errors"
)

// Capabilities tells Mobile which conversation features an agent supports so
// unsupported controls can be hidden instead of failing after user input.
type Capabilities struct {
	History            bool `json:"history"`
	LiveStream         bool `json:"liveStream"`
	TextInput          bool `json:"textInput"`
	ImageInput         bool `json:"imageInput"`
	Approval           bool `json:"approval"`
	Question           bool `json:"question"`
	Interrupt          bool `json:"interrupt"`
	Steer              bool `json:"steer"`
	Queue              bool `json:"queue"`
	Plan               bool `json:"plan"`
	CreateConversation bool `json:"createConversation"`
	ModelSelection     bool `json:"modelSelection"`
	ReasoningSelection bool `json:"reasoningSelection"`
	PermissionMode     bool `json:"permissionMode"`
	DesktopMirror      bool `json:"desktopMirror"`
}

type AgentSummary struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Kind           string        `json:"kind"`
	Status         string        `json:"status"`
	ActiveTurn     bool          `json:"activeTurn"`
	PendingActions int           `json:"pendingActions"`
	Capabilities   *Capabilities `json:"capabilities,omitempty"`
}

type ProjectSummary struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updatedAt"`
}

type ConversationSummary struct {
	ID             string `json:"id"`
	AgentID        string `json:"agentId"`
	ProjectID      string `json:"projectId,omitempty"`
	ProjectName    string `json:"projectName,omitempty"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Running        bool   `json:"running"`
	NeedsAttention bool   `json:"needsAttention"`
	// ExternallyActive marks a conversation owned by another process on the
	// computer. Mobile may read it but must not start turns in it.
	ExternallyActive bool  `json:"externallyActive,omitempty"`
	CreatedAt        int64 `json:"createdAt"`
	UpdatedAt        int64 `json:"updatedAt"`
}

type ConversationMetadata struct {
	ID               string `json:"id"`
	AgentID          string `json:"agentId"`
	ProjectID        string `json:"projectId,omitempty"`
	Title            string `json:"title"`
	Preview          string `json:"preview,omitempty"`
	Status           string `json:"status"`
	Running          bool   `json:"running"`
	ExternallyActive bool   `json:"externallyActive,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

type TimelineBlock struct {
	Kind         string `json:"kind"`
	Text         string `json:"text"`
	SummaryKind  string `json:"summaryKind,omitempty"`
	SummaryValue string `json:"summaryValue,omitempty"`
	Truncated    bool   `json:"truncated"`
	ContentRef   string `json:"contentRef,omitempty"`
	TotalBytes   int    `json:"totalBytes,omitempty"`
}

type TimelineItem struct {
	ID              string          `json:"id"`
	ClientMessageID string          `json:"clientMessageId,omitempty"`
	TurnID          string          `json:"turnId,omitempty"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status,omitempty"`
	Blocks          []TimelineBlock `json:"blocks"`
}

// TimelinePage lists items newest first; NextCursor addresses older items.
type TimelinePage struct {
	Items      []TimelineItem `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ConversationQuery struct {
	ProjectID string
	Query     string
	Cursor    string
	Limit     int
}

// Notifier reports that a conversation changed. An empty conversation ID
// means the conversation or project list changed.
type Notifier func(conversationID string)

// Backend serves one agent through the Mobile contract.
type Backend interface {
	Agent() AgentSummary
	Start(ctx context.Context, notify Notifier) error
	ListProjects(ctx context.Context) ([]ProjectSummary, error)
	ListConversations(ctx context.Context, query ConversationQuery) (Page[ConversationSummary], error)
	ReadConversation(ctx context.Context, conversationID string) (ConversationMetadata, error)
	Timeline(ctx context.Context, conversationID, cursor string, limit int) (TimelinePage, error)
	TimelineBlock(ctx context.Context, conversationID, turnID, itemID string, blockIndex int) (string, error)
	Close() error
}

// Error carries a stable Mobile contract error code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func NewError(code, message string) error { return &Error{Code: code, Message: message} }

func ErrorCode(err error) (string, bool) {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code, true
	}
	return "", false
}
