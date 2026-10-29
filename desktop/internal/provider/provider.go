// Package provider defines the Mobile-facing conversation model shared by the
// daemon and every agent backend. Codex is still served by the daemon's
// app-server projection; other agents implement Backend directly and return
// these provider-neutral DTOs.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"
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

type ActionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ActionQuestion struct {
	Header   string         `json:"header"`
	ID       string         `json:"id"`
	Question string         `json:"question"`
	IsOther  bool           `json:"isOther,omitempty"`
	IsSecret bool           `json:"isSecret,omitempty"`
	Options  []ActionOption `json:"options"`
}

// ActionDetail describes what an approval would allow. Fields are optional
// one-line or bounded values; providers that cannot describe a request omit it.
type ActionDetail struct {
	ToolName    string `json:"toolName,omitempty"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	FilePath    string `json:"filePath,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type Action struct {
	ID             json.RawMessage  `json:"id"`
	Kind           string           `json:"kind"`
	Method         string           `json:"method"`
	ConversationID string           `json:"conversationId,omitempty"`
	TurnID         string           `json:"turnId,omitempty"`
	CreatedAt      time.Time        `json:"createdAt"`
	Questions      []ActionQuestion `json:"questions,omitempty"`
	Detail         *ActionDetail    `json:"detail,omitempty"`
}

type ReasoningEffort struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

type ModelCatalogItem struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	Description            string            `json:"description,omitempty"`
	Default                bool              `json:"default"`
	Selectable             bool              `json:"selectable"`
	DefaultReasoningEffort string            `json:"defaultReasoningEffort"`
	ReasoningEfforts       []ReasoningEffort `json:"reasoningEfforts"`
}

type ModelCatalog struct {
	Items []ModelCatalogItem `json:"items"`
}

// PermissionMode is one of the Mobile permission presets (default,
// autoReview, readOnly, fullAccess) with its availability for an agent.
type PermissionMode struct {
	ID        string
	Available bool
	Reason    string
	Dangerous bool
}

type ConversationSettings struct {
	Mode             string `json:"mode"`
	ModelID          string `json:"modelId"`
	ReasoningEffort  string `json:"reasoningEffort"`
	PermissionModeID string `json:"permissionModeId"`
}

type PlanConfirmation struct {
	PlanItemID string `json:"planItemId"`
	TurnID     string `json:"turnId"`
	Revision   int64  `json:"revision"`
}

type Image struct {
	MimeType string
	Data     []byte
}

type TurnRequest struct {
	ConversationID  string
	ClientMessageID string
	Text            string
	Images          []Image
	// Settings, when set, replaces the conversation settings before the turn.
	Settings *ConversationSettings
}

type EventKind int

const (
	// EventChanged invalidates a conversation, or the conversation list when
	// ConversationID is empty.
	EventChanged EventKind = iota
	EventDelta
	EventActionRequested
	EventActionResolved
	EventTurnStarted
	EventTurnCompleted
	EventTurnFailed
	EventTurnInterrupted
)

// Delta kinds match the Mobile conversation.delta kinds.
const (
	DeltaAssistantText = "assistantText"
	DeltaReasoning     = "reasoning"
)

type Event struct {
	Kind           EventKind
	ConversationID string
	TurnID         string
	ItemID         string
	DeltaKind      string
	Delta          string
}

type Notifier func(Event)

// Backend serves one agent through the Mobile contract. Methods an agent
// does not support return an *Error with code unsupported_capability.
type Backend interface {
	Agent() AgentSummary
	Start(ctx context.Context, notify Notifier) error
	ListProjects(ctx context.Context) ([]ProjectSummary, error)
	ListConversations(ctx context.Context, query ConversationQuery) (Page[ConversationSummary], error)
	ReadConversation(ctx context.Context, conversationID string) (ConversationMetadata, error)
	Timeline(ctx context.Context, conversationID, cursor string, limit int) (TimelinePage, error)
	TimelineBlock(ctx context.Context, conversationID, turnID, itemID string, blockIndex int) (string, error)

	// ProjectRoot returns the trusted working directory of a project or of
	// the project that owns a conversation; Mobile never supplies paths.
	ProjectRoot(projectID, conversationID string) (string, bool)
	CreateConversation(ctx context.Context, projectID string) (ConversationMetadata, error)
	StartTurn(ctx context.Context, request TurnRequest) (string, error)
	InterruptTurn(ctx context.Context, conversationID string) error
	// ActiveTurns counts running turns; InterruptAll asks each to stop. The
	// daemon uses them to guard and drain shutdown.
	ActiveTurns() int
	InterruptAll(ctx context.Context)
	Actions() []Action
	ResolveAction(ctx context.Context, id string, result json.RawMessage) error
	// PlanConfirmation returns the pending plan approval, without revision.
	PlanConfirmation(conversationID string) *PlanConfirmation
	RespondPlan(ctx context.Context, conversationID, planItemID, decision, text string) error
	Settings(conversationID string) ConversationSettings
	UpdateSettings(ctx context.Context, conversationID string, settings ConversationSettings) (ConversationSettings, error)
	Models(ctx context.Context) (ModelCatalog, error)
	PermissionModes(ctx context.Context) ([]PermissionMode, error)
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
