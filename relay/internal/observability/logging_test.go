package observability

import (
	"context"
	"log/slog"
	"testing"
)

type captureHandler struct{ attrs []slog.Attr }

func (c *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c *captureHandler) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attribute slog.Attr) bool { c.attrs = append(c.attrs, attribute); return true })
	return nil
}
func (c *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *captureHandler) WithGroup(string) slog.Handler      { return c }

func TestLoggerScrubsSensitiveAttributes(t *testing.T) {
	capture := &captureHandler{}
	logger := slog.New(&scrubHandler{next: capture})
	logger.Info("test", "device_id", "safe", "provider_token", "secret")
	if len(capture.attrs) != 2 || capture.attrs[0].Value.String() != "safe" || capture.attrs[1].Value.String() != "[REDACTED]" {
		t.Fatalf("unexpected scrub result: %+v", capture.attrs)
	}
}
