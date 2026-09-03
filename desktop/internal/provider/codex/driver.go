package codex

import (
	"context"
	"encoding/json"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

type Caller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
	ResolveAction(json.RawMessage, json.RawMessage) error
}

type Driver struct{ caller Caller }

func New(caller Caller) *Driver { return &Driver{caller: caller} }

func (*Driver) Capabilities() provider.Capabilities {
	return provider.Capabilities{History: true, LiveStream: true, TextInput: true, ImageInput: true, Approval: true, Question: true, Interrupt: true, Steer: true, ModelSelection: true, ReasoningSelection: true, PermissionMode: true, DesktopMirror: true}
}
func (d *Driver) ListSessions(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
	return d.caller.Call(ctx, "thread/list", p)
}
func (d *Driver) ReadSession(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
	return d.caller.Call(ctx, "thread/read", p)
}
func (d *Driver) StartTurn(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
	return d.caller.Call(ctx, "turn/start", p)
}
func (d *Driver) InterruptTurn(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
	return d.caller.Call(ctx, "turn/interrupt", p)
}
func (d *Driver) ResolveAction(_ context.Context, p json.RawMessage) error {
	var v struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(p, &v); err != nil {
		return err
	}
	return d.caller.ResolveAction(v.ID, v.Result)
}
func (d *Driver) ListModels(ctx context.Context) (json.RawMessage, error) {
	return d.caller.Call(ctx, "model/list", json.RawMessage(`{}`))
}
func (d *Driver) ListPermissionProfiles(ctx context.Context) (json.RawMessage, error) {
	return d.caller.Call(ctx, "permissionProfile/list", json.RawMessage(`{}`))
}
func (*Driver) Close() error { return nil }

var _ provider.Driver = (*Driver)(nil)
