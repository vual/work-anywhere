package traffic

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Recorder struct {
	directory string
	mu        sync.Mutex
	enabled   map[string]bool
	maxBytes  int64
}

func New(directory string) *Recorder {
	return &Recorder{directory: directory, enabled: make(map[string]bool), maxBytes: 10 << 20}
}

func (r *Recorder) Set(instanceID string, enabled bool) {
	r.mu.Lock()
	r.enabled[instanceID] = enabled
	r.mu.Unlock()
}

func (r *Recorder) Enabled(instanceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled[instanceID]
}

func (r *Recorder) Record(instanceID, direction string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled[instanceID] {
		return
	}
	dir := filepath.Join(r.directory, instanceID)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path := filepath.Join(dir, "traffic.ndjson")
	if info, err := os.Stat(path); err == nil && info.Size() >= r.maxBytes {
		_ = os.Remove(path + ".3")
		_ = os.Rename(path+".2", path+".3")
		_ = os.Rename(path+".1", path+".2")
		_ = os.Rename(path, path+".1")
	}
	record := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "direction": direction}
	if json.Valid(payload) {
		record["payload"] = json.RawMessage(payload)
	} else {
		record["payloadBase64"] = base64.StdEncoding.EncodeToString(payload)
	}
	line, _ := json.Marshal(record)
	line = append(line, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = file.Write(line)
		_ = file.Close()
	}
}
