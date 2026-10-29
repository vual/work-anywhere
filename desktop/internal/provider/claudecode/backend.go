// Package claudecode serves local Claude Code sessions through the Mobile
// contract. History is read from the CLI's transcript store; turns run in
// headless Claude Code processes owned by the daemon. Sessions that are open
// in another Claude Code process are exposed read-only.
package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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
	defaultIdleTimeout   = 10 * time.Minute
	defaultListLimit     = 20
	maxListLimit         = 100
	maxCachedTranscripts = 8
	maxClientMessages    = 1024
	createdTTL           = 24 * time.Hour
	maxCreated           = 256
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Options struct {
	// ConfigDir is the Claude Code configuration directory. Empty means
	// CLAUDE_CONFIG_DIR, then ~/.claude.
	ConfigDir string
	// Executable overrides Claude Code executable discovery.
	Executable   string
	Name         string
	PollInterval time.Duration
	// IdleTimeout stops a session process that has had no turn or pending
	// request for this long. The conversation resumes on the next turn.
	IdleTimeout time.Duration
	// Attach places started processes into the daemon's process group.
	Attach func(*exec.Cmd) error
	// ProcessDiagnostic receives metadata only, never stderr or credentials.
	ProcessDiagnostic func(ProcessDiagnostic)
	// Alive and Resolve override process liveness and runtime discovery in
	// tests.
	Alive   func(pid int, startTime string) bool
	Resolve func(ctx context.Context) (Runtime, error)
}

type fileEntry struct {
	path    string
	scan    scanState
	summary summary
}

type cachedTranscript struct {
	mu       sync.Mutex
	path     string
	scan     scanState
	value    *transcript
	lastUsed time.Time
}

type registryEntry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	ProcStart string `json:"procStart"`
}

// createdConversation is a conversation created from Mobile whose first turn
// has not produced a transcript yet.
type createdConversation struct {
	projectID string
	cwd       string
	createdAt int64 // Unix seconds
}

type Backend struct {
	agentID             string
	name                string
	configDir           string
	executable          string
	poll                time.Duration
	idleTimeout         time.Duration
	alive               func(int, string) bool
	resolve             func(context.Context) (Runtime, error)
	attach              func(*exec.Cmd) error
	onProcessDiagnostic func(ProcessDiagnostic)
	lastProcessError    string

	refreshMu      sync.Mutex
	mu             sync.Mutex
	files          map[string]*fileEntry
	transcripts    map[string]*cachedTranscript
	live           map[string]bool
	created        map[string]*createdConversation
	settings       map[string]provider.ConversationSettings
	processes      map[string]*sessionProcess
	clientMessages map[string]string
	runtime        Runtime
	runtimeError   string
	runtimeReady   bool
	notify         provider.Notifier
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
	b := &Backend{
		agentID:             AgentID(configDir),
		name:                options.Name,
		configDir:           configDir,
		executable:          options.Executable,
		poll:                options.PollInterval,
		idleTimeout:         options.IdleTimeout,
		alive:               options.Alive,
		resolve:             options.Resolve,
		attach:              options.Attach,
		onProcessDiagnostic: options.ProcessDiagnostic,
		files:               make(map[string]*fileEntry),
		transcripts:         make(map[string]*cachedTranscript),
		live:                make(map[string]bool),
		created:             make(map[string]*createdConversation),
		settings:            make(map[string]provider.ConversationSettings),
		processes:           make(map[string]*sessionProcess),
		clientMessages:      make(map[string]string),
	}
	if b.name == "" {
		b.name = "Claude Code"
	}
	if b.poll <= 0 {
		b.poll = defaultPollInterval
	}
	if b.idleTimeout <= 0 {
		b.idleTimeout = defaultIdleTimeout
	}
	if b.alive == nil {
		b.alive = processinfo.Alive
	}
	if b.resolve == nil {
		b.resolve = func(ctx context.Context) (Runtime, error) { return ResolveRuntime(ctx, b.executable) }
	}
	return b, nil
}

// AgentID is stable for one Claude Code configuration directory.
func AgentID(configDir string) string {
	return Kind + ":" + shortHash(normalizePath(configDir))
}

func (b *Backend) Agent() provider.AgentSummary {
	b.mu.Lock()
	defer b.mu.Unlock()
	summary := provider.AgentSummary{ID: b.agentID, Name: b.name, Kind: Kind, Status: "ready"}
	if !Available(b.configDir) {
		summary.Status = "unavailable"
	}
	capabilities := provider.Capabilities{History: true, DesktopMirror: true}
	if b.runtime.Executable != "" {
		capabilities = provider.Capabilities{History: true, LiveStream: true, TextInput: true, ImageInput: true, Approval: true, Question: true, Interrupt: true, Plan: true, CreateConversation: true, ModelSelection: true, ReasoningSelection: true, PermissionMode: true, DesktopMirror: true}
	}
	summary.Capabilities = &capabilities
	for _, process := range b.processes {
		summary.ActiveTurn = summary.ActiveTurn || process.turnActive
		summary.PendingActions += len(process.requests)
	}
	return summary
}

func (b *Backend) Start(ctx context.Context, notify provider.Notifier) error {
	b.mu.Lock()
	b.notify = notify
	b.mu.Unlock()
	go b.resolveRuntime(ctx)
	if err := b.refresh(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	go b.watch(ctx)
	return nil
}

func (b *Backend) resolveRuntime(ctx context.Context) {
	resolved, err := b.resolve(ctx)
	b.mu.Lock()
	b.runtimeReady = true
	if err != nil {
		b.runtimeError = err.Error()
	} else {
		b.runtime, b.runtimeError = resolved, ""
	}
	b.mu.Unlock()
	// Capabilities change once the CLI is known; let Mobile refresh.
	b.emit(provider.Event{Kind: provider.EventChanged})
}

// Close stops every session process. The daemon's process group also kills
// them if the daemon exits without calling Close.
func (b *Backend) Close() error {
	b.mu.Lock()
	processes := make([]*sessionProcess, 0, len(b.processes))
	for _, process := range b.processes {
		processes = append(processes, process)
	}
	b.mu.Unlock()
	for _, process := range processes {
		process.kill()
	}
	return nil
}

func (b *Backend) emit(event provider.Event) {
	b.mu.Lock()
	notify := b.notify
	b.mu.Unlock()
	if notify != nil {
		notify(event)
	}
}

func (b *Backend) watch(ctx context.Context) {
	ticker := time.NewTicker(b.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = b.Close()
			return
		case <-ticker.C:
			_ = b.refresh()
			b.stopIdleProcesses(time.Now())
		}
	}
}

func (b *Backend) stopIdleProcesses(now time.Time) {
	b.mu.Lock()
	b.expireCreatedLocked(now)
	idle := make([]*sessionProcess, 0)
	for id, process := range b.processes {
		if !process.turnActive && len(process.requests) == 0 && now.Sub(process.lastUsed) >= b.idleTimeout {
			delete(b.processes, id)
			idle = append(idle, process)
		}
	}
	b.mu.Unlock()
	for _, process := range idle {
		process.stop()
	}
}

// expireCreatedLocked forgets conversations created from Mobile that never
// started a turn, so abandoned drafts do not keep project roots resolvable.
func (b *Backend) expireCreatedLocked(now time.Time) {
	for id, created := range b.created {
		if b.processes[id] == nil && now.Sub(time.Unix(created.createdAt, 0)) >= createdTTL {
			delete(b.created, id)
		}
	}
}

// refresh rescans the transcript and live-session stores and reports every
// change through the notifier. refreshMu makes it the single writer of the
// summary index; file I/O happens outside b.mu so session readers and
// requests are never blocked behind a scan.
func (b *Backend) refresh() error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	changed, listChanged, err := b.refreshIndex()
	liveChanged := b.refreshLive()
	if len(liveChanged) > 0 {
		listChanged = true
	}
	for id := range liveChanged {
		changed[id] = true
	}
	// A turn streamed by this backend's own process is reported through its
	// stdout events. Polling invalidations would advance the sequence between
	// deltas and force Mobile to drop the live text for a snapshot.
	b.mu.Lock()
	for id := range changed {
		if process := b.processes[id]; process != nil && process.turnActive && !liveChanged[id] {
			delete(changed, id)
		}
	}
	b.mu.Unlock()
	if listChanged {
		b.emit(provider.Event{Kind: provider.EventChanged})
	}
	for id := range changed {
		b.emit(provider.Event{Kind: provider.EventChanged, ConversationID: id})
	}
	return err
}

// refreshIndex scans copies of the index entries without holding b.mu and
// swaps changed entries in afterwards. listChanged reports changes visible
// in conversation lists beyond recency: added or removed conversations and
// changed titles.
func (b *Backend) refreshIndex() (map[string]bool, bool, error) {
	changed := make(map[string]bool)
	paths, err := filepath.Glob(filepath.Join(b.configDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return changed, false, err
	}
	b.mu.Lock()
	current := make(map[string]*fileEntry, len(b.files))
	for id, entry := range b.files {
		current[id] = entry
	}
	b.mu.Unlock()
	seen := make(map[string]bool, len(paths))
	updates := make(map[string]*fileEntry)
	listChanged := false
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !sessionIDPattern.MatchString(id) {
			continue
		}
		seen[id] = true
		old := current[id]
		next := &fileEntry{path: path}
		if old != nil && old.path == path {
			*next = *old
		}
		before := next.scan
		scanErr := scanFile(path, &next.scan, next.summary.observe)
		if errors.Is(scanErr, errTranscriptReplaced) {
			next.scan, next.summary = scanState{}, summary{}
			scanErr = scanFile(path, &next.scan, next.summary.observe)
		}
		if scanErr != nil {
			continue
		}
		if old == nil || old.path != path {
			updates[id] = next
			listChanged = true
			continue
		}
		if next.scan != before {
			updates[id] = next
			changed[id] = true
			if next.summary.title() != old.summary.title() || next.summary.HasPrompt != old.summary.HasPrompt {
				listChanged = true
			}
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, entry := range updates {
		b.files[id] = entry
		if entry.summary.HasPrompt {
			delete(b.created, id)
		}
	}
	for id, entry := range current {
		if !seen[id] && b.files[id] == entry {
			delete(b.files, id)
			delete(b.transcripts, id)
			listChanged = true
		}
	}
	return changed, listChanged, nil
}

// refreshLive reads the CLI's live-session registry. Each running Claude
// Code process writes sessions/<pid>.json naming the session it owns.
// Sessions this backend is running are never external owners, whichever
// PID the registry records for them (a configured wrapper may differ from
// the process that registers).
func (b *Backend) refreshLive() map[string]bool {
	live := make(map[string]bool)
	paths, _ := filepath.Glob(filepath.Join(b.configDir, "sessions", "*.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var session registryEntry
		if json.Unmarshal(data, &session) != nil || session.SessionID == "" {
			continue
		}
		if b.alive(session.PID, session.ProcStart) {
			live[session.SessionID] = true
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range live {
		if b.processes[id] != nil {
			delete(live, id)
		}
	}
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

// ProjectRoot returns the working directory of a project, or of the project
// that owns conversationID. Every cwd comes from the transcript index or from
// a conversation created against an indexed project.
func (b *Backend) ProjectRoot(projectID, conversationID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.projectRootLocked(projectID, conversationID)
}

func (b *Backend) projectRootLocked(projectID, conversationID string) (string, bool) {
	if conversationID != "" {
		if entry := b.files[conversationID]; entry != nil && entry.summary.CWD != "" {
			return entry.summary.CWD, true
		}
		if created := b.created[conversationID]; created != nil {
			return created.cwd, true
		}
		if projectID == "" {
			return "", false
		}
	}
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
	result := provider.ConversationSummary{ID: id, AgentID: b.agentID, Title: entry.summary.title(), ExternallyActive: b.live[id], CreatedAt: entry.summary.CreatedAt / 1000, UpdatedAt: entry.updatedAt()}
	if entry.summary.CWD != "" {
		result.ProjectID = ProjectID(entry.summary.CWD)
		result.ProjectName = projectName(entry.summary.CWD)
	}
	result.Status, result.Running, result.NeedsAttention = b.statusLocked(id)
	return result
}

// statusLocked derives the Mobile status from this backend's own process.
// Conversations open elsewhere report idle with ExternallyActive set.
func (b *Backend) statusLocked(id string) (string, bool, bool) {
	process := b.processes[id]
	if process == nil {
		return "idle", false, false
	}
	for _, request := range process.requests {
		switch request.kind {
		case actionKindUserInput:
			return "waitingOnUserInput", true, true
		default:
			return "waitingOnApproval", true, true
		}
	}
	if process.turnActive {
		return "running", true, false
	}
	return "idle", false, false
}

func (b *Backend) ReadConversation(_ context.Context, conversationID string) (provider.ConversationMetadata, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.files[conversationID]
	if entry == nil || !entry.summary.HasPrompt {
		if created := b.created[conversationID]; created != nil {
			status, running, _ := b.statusLocked(conversationID)
			return provider.ConversationMetadata{ID: conversationID, AgentID: b.agentID, ProjectID: created.projectID, Title: "New conversation", Status: status, Running: running, CreatedAt: created.createdAt, UpdatedAt: created.createdAt}, nil
		}
	}
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
	b.mu.Lock()
	for index := end - 1; index >= start; index-- {
		item := items[index]
		item.Blocks = append([]provider.TimelineBlock(nil), item.Blocks...)
		if item.Kind == "userMessage" {
			item.ClientMessageID = b.clientMessages[item.ID]
		}
		page.Items = append(page.Items, item)
	}
	b.mu.Unlock()
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
// cached parse with any lines appended since the last read. Parsing holds
// only the transcript's own lock.
func (b *Backend) timelineItems(conversationID string) ([]provider.TimelineItem, error) {
	b.mu.Lock()
	entry := b.files[conversationID]
	if entry == nil {
		created := b.created[conversationID] != nil
		b.mu.Unlock()
		if created {
			return nil, nil
		}
		return nil, provider.NewError("not_found", "conversation not found")
	}
	cached := b.transcripts[conversationID]
	if cached == nil || cached.path != entry.path {
		cached = &cachedTranscript{path: entry.path, value: newTranscript()}
		b.transcripts[conversationID] = cached
	}
	cached.lastUsed = time.Now()
	b.evictTranscriptsLocked()
	b.mu.Unlock()

	cached.mu.Lock()
	err := scanFile(cached.path, &cached.scan, cached.value.observe)
	if errors.Is(err, errTranscriptReplaced) {
		cached.scan, cached.value = scanState{}, newTranscript()
		err = scanFile(cached.path, &cached.scan, cached.value.observe)
	}
	var items []provider.TimelineItem
	if err == nil {
		items = append([]provider.TimelineItem(nil), cached.value.items...)
	}
	cached.mu.Unlock()
	if err != nil {
		b.mu.Lock()
		if b.transcripts[conversationID] == cached {
			delete(b.transcripts, conversationID)
		}
		b.mu.Unlock()
		return nil, provider.NewError("provider_error", "read transcript: "+err.Error())
	}
	return items, nil
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

// updatedAt is in Unix seconds, the unit Mobile and the Codex DTOs use.
func (e *fileEntry) updatedAt() int64 {
	if !e.scan.modTime.IsZero() {
		return e.scan.modTime.Unix()
	}
	return e.summary.LastAt / 1000
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
