package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	path     string
	mu       sync.Mutex
	maxBytes int64
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Logger{path: path, maxBytes: 5 << 20}, nil
}

func (l *Logger) Event(name string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if info, err := os.Stat(l.path); err == nil && info.Size() >= l.maxBytes {
		_ = os.Remove(l.path + ".2")
		_ = os.Rename(l.path+".1", l.path+".2")
		_ = os.Rename(l.path, l.path+".1")
	}
	record := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "event": name}
	for key, value := range fields {
		record[key] = redact(key, value)
	}
	line, _ := json.Marshal(record)
	line = append(line, '\n')
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = file.Write(line)
		_ = file.Close()
	}
}

func redact(key string, value any) any {
	lower := strings.ToLower(key)
	for _, word := range []string{"token", "secret", "password", "authorization", "environment", "payload", "content"} {
		if strings.Contains(lower, word) {
			return "[REDACTED]"
		}
	}
	if object, ok := value.(map[string]any); ok {
		copy := make(map[string]any, len(object))
		for nested, nestedValue := range object {
			copy[nested] = redact(nested, nestedValue)
		}
		return copy
	}
	return value
}
