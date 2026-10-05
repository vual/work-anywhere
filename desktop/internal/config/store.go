package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/platform/secrets"
)

const (
	fileVersion           = 1
	threadSettingPrefix   = "thread-collaboration:"
	maxThreadSettingCount = 2000
)

type storedValue struct {
	Protected string    `json:"protected"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type fileState struct {
	Version  int                    `json:"version"`
	Settings map[string]storedValue `json:"settings,omitempty"`
}

// Store persists only small configuration values. Message bodies, timeline
// events, actions, attachments, and runtime projections must never be put here.
type Store struct {
	path  string
	mu    sync.Mutex
	state fileState
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, state: fileState{Version: fileVersion, Settings: make(map[string]storedValue)}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err = json.Unmarshal(data, &s.state); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if s.state.Version != fileVersion {
		return nil, fmt.Errorf("unsupported config version %d", s.state.Version)
	}
	if s.state.Settings == nil {
		s.state.Settings = make(map[string]storedValue)
	}
	return s, nil
}

func (s *Store) Close() error { return nil }

func (s *Store) PutSetting(key string, value any) error {
	if key == "" {
		return errors.New("setting key is required")
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	protected, err := secrets.ProtectForCurrentUser(plain)
	if err != nil {
		return fmt.Errorf("protect setting %q: %w", key, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneState(s.state)
	next.Settings[key] = storedValue{Protected: base64.StdEncoding.EncodeToString(protected), UpdatedAt: time.Now().UTC()}
	trimThreadSettings(&next)
	if err = s.writeLocked(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) GetSetting(key string, target any) (bool, error) {
	s.mu.Lock()
	value, found := s.state.Settings[key]
	s.mu.Unlock()
	if !found {
		return false, nil
	}
	protected, err := base64.StdEncoding.DecodeString(value.Protected)
	if err != nil {
		return false, fmt.Errorf("decode setting %q: %w", key, err)
	}
	plain, err := secrets.UnprotectForCurrentUser(protected)
	if err != nil {
		return false, fmt.Errorf("unprotect setting %q: %w", key, err)
	}
	if err = json.Unmarshal(plain, target); err != nil {
		return false, fmt.Errorf("parse setting %q: %w", key, err)
	}
	return true, nil
}

func (s *Store) DeleteSetting(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.state.Settings[key]; !found {
		return nil
	}
	next := cloneState(s.state)
	delete(next.Settings, key)
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) writeLocked(next fileState) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	directory := filepath.Dir(s.path)
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = replaceFile(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func cloneState(value fileState) fileState {
	next := fileState{Version: fileVersion, Settings: make(map[string]storedValue, len(value.Settings))}
	for key, item := range value.Settings {
		next.Settings[key] = item
	}
	return next
}

func trimThreadSettings(state *fileState) {
	type candidate struct {
		key       string
		updatedAt time.Time
	}
	items := make([]candidate, 0)
	for key, value := range state.Settings {
		if strings.HasPrefix(key, threadSettingPrefix) {
			items = append(items, candidate{key: key, updatedAt: value.UpdatedAt})
		}
	}
	if len(items) <= maxThreadSettingCount {
		return
	}
	sort.Slice(items, func(a, b int) bool { return items[a].updatedAt.Before(items[b].updatedAt) })
	for _, item := range items[:len(items)-maxThreadSettingCount] {
		delete(state.Settings, item.key)
	}
}
