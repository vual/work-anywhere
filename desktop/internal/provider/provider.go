package provider

import (
	"context"
	"encoding/json"
)

type Capabilities struct {
	History            bool `json:"history"`
	LiveStream         bool `json:"liveStream"`
	TextInput          bool `json:"textInput"`
	ImageInput         bool `json:"imageInput"`
	Approval           bool `json:"approval"`
	Question           bool `json:"question"`
	Interrupt          bool `json:"interrupt"`
	Steer              bool `json:"steer"`
	ModelSelection     bool `json:"modelSelection"`
	ReasoningSelection bool `json:"reasoningSelection"`
	PermissionMode     bool `json:"permissionMode"`
	DesktopMirror      bool `json:"desktopMirror"`
}

type Driver interface {
	Capabilities() Capabilities
	ListSessions(context.Context, json.RawMessage) (json.RawMessage, error)
	ReadSession(context.Context, json.RawMessage) (json.RawMessage, error)
	StartTurn(context.Context, json.RawMessage) (json.RawMessage, error)
	InterruptTurn(context.Context, json.RawMessage) (json.RawMessage, error)
	ResolveAction(context.Context, json.RawMessage) error
	ListModels(context.Context) (json.RawMessage, error)
	ListPermissionProfiles(context.Context) (json.RawMessage, error)
	Close() error
}
