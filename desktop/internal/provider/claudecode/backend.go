// Package claudecode serves local Claude Code sessions through the Mobile
// contract. Conversations are read from the CLI's transcript store; sessions
// that are open in another Claude Code process are exposed read-only.
package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/platform/processinfo"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

const (
	Kind                 = "claude-code"
	defaultPollInterval  = 2 * time.Second
	defaultListLimit     = 20
	maxListLimit         = 100
	maxCachedTranscripts = 8
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Options struct {
	// ConfigDir is the Claude Code configuration directory. Empty means
	// CLAUDE_CONFIG_DIR, then ~/.claude.
	ConfigDir    string
	Name         string
	PollInterval time.Duration
	// Alive overrides process liveness checks in tests.
	Alive func(pid int, startTime string) bool
}

type fileEntry struct {
	path    string
	scan    scanState
	summary summary
}

type cachedTranscript struct {
	path     string
	scan     scanState
	value    *transcript
	lastUsed time.Time
}

type liveSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	ProcStart string `json:"procStart"`
}

type Backend struct {
	agentID   string
	name      string
	configDir string
	poll      time.Duration
	alive     func(int, string) bool

	mu          sync.Mutex
	files       map[string]*fileEntry
	transcripts map[string]*cachedTranscript
	live        map[string]bool
	notify      provider.Notifier
}

func ResolveConfigDir(configured string) (string, error) {
	if configured != "" {
		return filepath.Clean(configured), nil
	}
	if value := os.Getenv("CLAUDE_CONFIG_DIR"); value != "" {
		return filepath.Clean(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// Available reports whether the configuration directory contains a
// transcript store, i.e. Claude Code has been used by this OS user.
func Available(configDir string) bool {
	info, err := os.Stat(filepath.Join(configDir, "projects"))
	return err == nil && info.IsDir()
}

func New(options Options) (*Backend, error) {
	configDir, err := ResolveConfigDir(options.ConfigDir)
	if err != nil {
		return nil, err
	}
	name := options.Name
	if name == "" {
		name = "Claude Code"
	}
	poll := options.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}
	alive := options.Alive
	if alive == nil {
		alive = processinfo.Alive
	}
	return &Backend{
		agentID:     AgentID(configDir),
		name:        name,
		configDir:   configDir,
		poll:        poll,
		alive:       alive,
		files:       make(map[string]*fileEntry),
		transcripts: make(map[string]*cachedTranscript),
		live:        make(map[string]bool),
	}, nil
}

// AgentID is stable for one Claude Code configuration directory.
func AgentID(configDir string) string {
	return Kind + ":" + shortHash(normalizePath(configDir))
}

func (b *Backend) Agent() provider.AgentSummary {
	status := "ready"
	if !Available(b.configDir) {
		status = "unavailable"
	}
	return provider.AgentSummary{ID: b.agentID, Name: b.name, Kind: Kind, Status: status, Capabilities: &provider.Capabilities{History: true, DesktopMirror: true}}
}

func (b *Backend) Start(ctx context.Context, notify provider.Notifier) error {
	b.mu.Lock()
	b.notify = notify
	b.mu.Unlock()
	if err := b.refresh(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	go b.watch(ctx)
	return nil
}

func (b *Backend) Close() error { return nil }

func (b *Backend) watch(ctx context.Context) {
	ticker := time.NewTicker(b.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = b.refresh()
		}
	}
}

// refresh rescans the transcript and live-session stores and reports every
// change through the notifier. It is the only writer of the summary index.
func (b *Backend) refresh() error {
	changed, listChanged, err := b.refreshIndex()
	liveChanged := b.refreshLive()
	for id := range liveChanged {
		changed[id] = true
	}
	b.mu.Lock()
	notify := b.notify
	b.mu.Unlock()
	if notify != nil {
		if listChanged || len(changed) > 0 {
			notify("")
		}
		for id := range changed {
			notify(id)
		}
	}
	return err
}

func (b *Backend) refreshIndex() (map[string]bool, bool, error) {
	changed := make(map[string]bool)
	paths, err := filepath.Glob(filepath.Join(b.configDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return changed, false, err
	}
	seen := make(map[string]bool, len(paths))
	listChanged := false
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !sessionIDPattern.MatchString(id) {
			continue
		}
		seen[id] = true
		entry := b.files[id]
		if entry == nil || entry.path != path {
			entry = &fileEntry{path: path}
			b.files[id] = entry
			listChanged = true
		}
		before := entry.scan
		scanErr := scanFile(path, &entry.scan, entry.summary.observe)
		if errors.Is(scanErr, errTranscriptReplaced) {
			entry.scan, entry.summary = scanState{}, summary{}
			scanErr = scanFile(path, &entry.scan, entry.summary.observe)
		}
		if scanErr != nil {
			continue
		}
		if entry.scan != before {
			changed[id] = true
		}
	}
	for id := range b.files {
		if !seen[id] {
			delete(b.files, id)
			delete(b.transcripts, id)
			listChanged = true
		}
	}
	return changed, listChanged, nil
}

// refreshLive reads the CLI's live-session registry. Each running Claude
// Code process writes sessions/<pid>.json naming the session it owns.
func (b *Backend) refreshLive() map[string]bool {
	live := make(map[string]bool)
	paths, _ := filepath.Glob(filepath.Join(b.configDir, "sessions", "*.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var session liveSession
		if json.Unmarshal(data, &session) != nil || session.SessionID == "" {
			continue
		}
		if b.alive(session.PID, session.ProcStart) {
			live[session.SessionID] = true
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := make(map[string]bool)
	for id := range live {
		if !b.live[id] {
			changed[id] = true
		}
	}
	for id := range b.live {
		if !live[id] {
			changed[id] = true
		}
	}
	b.live = live
	return changed
}

func (b *Backend) ListProjects(context.Context) ([]provider.ProjectSummary, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	byID := make(map[string]*provider.ProjectSummary)
	for _, entry := range b.files {
		if !entry.summary.HasPrompt || entry.summary.CWD == "" {
			continue
		}
		id := ProjectID(entry.summary.CWD)
		updated := entry.updatedAt()
		project := byID[id]
		if project == nil {
			project = &provider.ProjectSummary{ID: id, AgentID: b.agentID, Name: projectName(entry.summary.CWD)}
			byID[id] = project
		}
		if updated > project.UpdatedAt {
			project.UpdatedAt = updated
		}
	}
	result := make([]provider.ProjectSummary, 0, len(byID))
	for _, project := range byID {
		result = append(result, *project)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt != result[j].UpdatedAt {
			return result[i].UpdatedAt > result[j].UpdatedAt
		}
		return result[i].ID > result[j].ID
	})
	return result, nil
}

// ProjectRoot returns the working directory recorded for projectID. Mobile
// never supplies paths; every cwd is taken from the transcript index.
func (b *Backend) ProjectRoot(projectID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, entry := range b.files {
		if entry.summary.CWD != "" && ProjectID(entry.summary.CWD) == projectID {
			return entry.summary.CWD, true
		}
	}
	return "", false
}

func (b *Backend) ListConversations(_ context.Context, query provider.ConversationQuery) (provider.Page[provider.ConversationSummary], error) {
	limit := query.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	b.mu.Lock()
	items := make([]provider.ConversationSummary, 0, len(b.files))
	for id, entry := range b.files {
		if !entry.summary.HasPrompt {
			continue
		}
		summary := b.conversationSummaryLocked(id, entry)
		if query.ProjectID != "" && summary.ProjectID != query.ProjectID {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(summary.Title), needle) {
			continue
		}
		items = append(items, summary)
	}
	b.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt != items[j].UpdatedAt {
			return items[i].UpdatedAt > items[j].UpdatedAt
		}
		return items[i].ID > items[j].ID
	})
	start := 0
	if query.Cursor != "" {
		updatedAt, id, err := decodeListCursor(query.Cursor)
		if err != nil {
			return provider.Page[provider.ConversationSummary]{}, provider.NewError("invalid_params", "invalid conversation cursor")
		}
		for start < len(items) && !(items[start].UpdatedAt < updatedAt || (items[start].UpdatedAt == updatedAt && items[start].ID < id)) {
			start++
		}
	}
	end := min(start+limit, len(items))
	page := provider.Page[provider.ConversationSummary]{Items: append([]provider.ConversationSummary{}, items[start:end]...)}
	if end < len(items) && end > 0 {
		page.NextCursor = encodeListCursor(items[end-1].UpdatedAt, items[end-1].ID)
	}
	return page, nil
}

func (b *Backend) conversationSummaryLocked(id string, entry *fileEntry) provider.ConversationSummary {
	result := provider.ConversationSummary{ID: id, AgentID: b.agentID, Title: entry.summary.title(), Status: "idle", ExternallyActive: b.live[id], CreatedAt: entry.summary.CreatedAt, UpdatedAt: entry.updatedAt()}
	if entry.summary.CWD != "" {
		result.ProjectID = ProjectID(entry.summary.CWD)
		result.ProjectName = projectName(entry.summary.CWD)
	}
	return result
}

func (b *Backend) ReadConversation(_ context.Context, conversationID string) (provider.ConversationMetadata, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.files[conversationID]
	if entry == nil {
		return provider.ConversationMetadata{}, provider.NewError("not_found", "conversation not found")
	}
	summary := b.conversationSummaryLocked(conversationID, entry)
	return provider.ConversationMetadata{ID: summary.ID, AgentID: summary.AgentID, ProjectID: summary.ProjectID, Title: summary.Title, Preview: firstLine(entry.summary.FirstPrompt), Status: summary.Status, Running: summary.Running, ExternallyActive: summary.ExternallyActive, CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt}, nil
}

func (b *Backend) Timeline(_ context.Context, conversationID, cursor string, limit int) (provider.TimelinePage, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	items, err := b.timelineItems(conversationID)
	if err != nil {
		return provider.TimelinePage{}, err
	}
	// items are chronological; pages walk backwards from the newest item.
	end := len(items)
	if cursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(cursor)
		if decodeErr != nil {
			return provider.TimelinePage{}, provider.NewError("invalid_params", "invalid timeline cursor")
		}
		end = -1
		for index := range items {
			if items[index].ID == string(decoded) {
				end = index
				break
			}
		}
		if end < 0 {
			return provider.TimelinePage{}, provider.NewError("invalid_params", "timeline cursor is no longer valid")
		}
	}
	start := max(end-limit, 0)
	page := provider.TimelinePage{Items: make([]provider.TimelineItem, 0, end-start)}
	for index := end - 1; index >= start; index-- {
		item := items[index]
		item.Blocks = append([]provider.TimelineBlock(nil), item.Blocks...)
		page.Items = append(page.Items, item)
	}
	if start > 0 {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(items[start].ID))
	}
	return page, nil
}

func (b *Backend) TimelineBlock(_ context.Context, conversationID, turnID, itemID string, blockIndex int) (string, error) {
	items, err := b.timelineItems(conversationID)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.ID != itemID || (turnID != "" && item.TurnID != "" && item.TurnID != turnID) {
			continue
		}
		if blockIndex < 0 || blockIndex >= len(item.Blocks) || item.Blocks[blockIndex].Kind == "summary" || item.Blocks[blockIndex].Text == "" {
			break
		}
		return item.Blocks[blockIndex].Text, nil
	}
	return "", provider.NewError("content_not_found", "referenced content is not available")
}

// timelineItems returns a snapshot of the parsed transcript, extending the
// cached parse with any lines appended since the last read.
func (b *Backend) timelineItems(conversationID string) ([]provider.TimelineItem, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.files[conversationID]
	if entry == nil {
		return nil, provider.NewError("not_found", "conversation not found")
	}
	cached := b.transcripts[conversationID]
	if cached == nil || cached.path != entry.path {
		cached = &cachedTranscript{path: entry.path, value: newTranscript()}
		b.transcripts[conversationID] = cached
	}
	err := scanFile(entry.path, &cached.scan, cached.value.observe)
	if errors.Is(err, errTranscriptReplaced) {
		cached.scan, cached.value = scanState{}, newTranscript()
		err = scanFile(entry.path, &cached.scan, cached.value.observe)
	}
	if err != nil {
		delete(b.transcripts, conversationID)
		return nil, provider.NewError("provider_error", "read transcript: "+err.Error())
	}
	cached.lastUsed = time.Now()
	b.evictTranscriptsLocked()
	return append([]provider.TimelineItem(nil), cached.value.items...), nil
}

func (b *Backend) evictTranscriptsLocked() {
	for len(b.transcripts) > maxCachedTranscripts {
		oldest := ""
		for id, cached := range b.transcripts {
			if oldest == "" || cached.lastUsed.Before(b.transcripts[oldest].lastUsed) {
				oldest = id
			}
		}
		delete(b.transcripts, oldest)
	}
}

func (e *fileEntry) updatedAt() int64 {
	if !e.scan.modTime.IsZero() {
		return e.scan.modTime.UnixMilli()
	}
	return e.summary.LastAt
}

// ProjectID is stable for one working directory.
func ProjectID(cwd string) string {
	return "cc_" + shortHash(normalizePath(cwd))
}

func normalizePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func projectName(cwd string) string {
	if name := baseName(cwd); name != "" {
		return name
	}
	return cwd
}

// baseName accepts both separators because transcripts keep the path style
// of the machine that wrote them.
func baseName(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), `/\`)
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		path = path[index+1:]
	}
	if strings.HasSuffix(path, ":") {
		return ""
	}
	return path
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func encodeListCursor(updatedAt int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(updatedAt, 10) + ":" + id))
}

func decodeListCursor(value string) (int64, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, "", err
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", errors.New("invalid cursor")
	}
	updatedAt, err := strconv.ParseInt(parts[0], 10, 64)
	return updatedAt, parts[1], err
}

var _ provider.Backend = (*Backend)(nil)
