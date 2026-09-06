package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

func NewLogger(levelName string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(levelName) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(&scrubHandler{next: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})})
}

type scrubHandler struct{ next slog.Handler }

func (h *scrubHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *scrubHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		clean.AddAttrs(scrubAttribute(attribute))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *scrubHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		clean = append(clean, scrubAttribute(attribute))
	}
	return &scrubHandler{next: h.next.WithAttrs(clean)}
}

func (h *scrubHandler) WithGroup(name string) slog.Handler {
	return &scrubHandler{next: h.next.WithGroup(name)}
}

func scrubAttribute(attribute slog.Attr) slog.Attr {
	lower := strings.ToLower(attribute.Key)
	for _, secret := range []string{"authorization", "ciphertext", "content", "database", "environment", "key", "password", "payload", "secret", "signature", "token"} {
		if strings.Contains(lower, secret) {
			return slog.String(attribute.Key, "[REDACTED]")
		}
	}
	if attribute.Value.Kind() == slog.KindGroup {
		group := attribute.Value.Group()
		for index := range group {
			group[index] = scrubAttribute(group[index])
		}
		return slog.Group(attribute.Key, attrsToAny(group)...)
	}
	return attribute
}

func attrsToAny(attributes []slog.Attr) []any {
	result := make([]any, len(attributes))
	for index := range attributes {
		result[index] = attributes[index]
	}
	return result
}
