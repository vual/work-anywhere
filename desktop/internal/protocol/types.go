package protocol

import (
	"encoding/json"
	"time"
)

const APIRevision = "v1"

type Hello struct {
	Role        string             `json:"role"`
	Version     string             `json:"version"`
	Instance    InstanceDescriptor `json:"instance,omitempty"`
	Runtime     RuntimeDescriptor  `json:"runtime,omitempty"`
	Args        []string           `json:"args,omitempty"`
	CWD         string             `json:"cwd,omitempty"`
	Environment []string           `json:"environment,omitempty"`
}

type HelloAck struct {
	ConnectionID  string `json:"connectionId"`
	InstanceID    string `json:"instanceId,omitempty"`
	DaemonVersion string `json:"daemonVersion"`
}

type InstanceDescriptor struct {
	ID          string `json:"id"`
	CodexHome   string `json:"codexHome"`
	UserDataDir string `json:"userDataDir"`
	Name        string `json:"name,omitempty"`
	WindowsSID  string `json:"windowsSid"`
}

type RuntimeDescriptor struct {
	RealExecutable string    `json:"realExecutable"`
	Version        string    `json:"version,omitempty"`
	SHA256         string    `json:"sha256,omitempty"`
	ResourcesDir   string    `json:"resourcesDir,omitempty"`
	DiscoveredBy   string    `json:"discoveredBy,omitempty"`
	DiscoveredAt   time.Time `json:"discoveredAt"`
}

type ControlRequest struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ControlResponse struct {
	Type   string        `json:"type"`
	ID     string        `json:"id"`
	Result any           `json:"result,omitempty"`
	Error  *ControlError `json:"error,omitempty"`
}

type ControlError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Event struct {
	Type           string `json:"type"`
	Sequence       int64  `json:"sequence"`
	Event          string `json:"event"`
	InstanceID     string `json:"instanceId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	Payload        any    `json:"payload,omitempty"`
}
