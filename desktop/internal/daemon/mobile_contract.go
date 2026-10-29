package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

const (
	mobileDefaultTimelineLimit     = 20
	mobileMaxTimelineLimit         = 50
	mobileControlResponseBytes     = 512 << 10
	mobileTimelinePageBytes        = 500 << 10
	mobileLargeBlockBytes          = 64 << 10
	mobileInlinePreviewBytes       = 16 << 10
	mobileContentChunkBytes        = 128 << 10
	mobileContentRefTTL            = 15 * time.Minute
	mobileMaxContentRefs           = 2048
	mobileMaxTimelineProviderPages = 8
	mobileProjectProviderPageLimit = 1000
	mobileMaxProjectProviderPages  = 128
	mobileSummaryValueBytes        = 512
)

type mobileAgentSummary = provider.AgentSummary

type mobileProjectSummary = provider.ProjectSummary

type mobileConversationSummary = provider.ConversationSummary

type mobileActionOption = provider.ActionOption

type mobileActionQuestion = provider.ActionQuestion

type mobileAction = provider.Action

type mobilePage[T any] = provider.Page[T]

type mobileReasoningEffort = provider.ReasoningEffort

type mobileModelCatalogItem = provider.ModelCatalogItem

type mobileModelCatalog = provider.ModelCatalog

type mobilePermissionMode struct {
	ID          string `json:"id"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	Dangerous   bool   `json:"dangerous,omitempty"`
	Profile     string `json:"-"`
	SandboxMode string `json:"-"`
	Approval    string `json:"-"`
	Reviewer    string `json:"-"`
}

type mobilePermissionCatalog struct {
	Items []mobilePermissionMode `json:"items"`
}

type mobileConversationSettings = provider.ConversationSettings

func normalizeMobileModelCatalog(raw json.RawMessage) (mobileModelCatalog, error) {
	var source struct {
		Data []struct {
			ID                     string `json:"id"`
			Model                  string `json:"model"`
			DisplayName            string `json:"displayName"`
			Description            string `json:"description"`
			Hidden                 bool   `json:"hidden"`
			IsDefault              bool   `json:"isDefault"`
			DefaultReasoningEffort string `json:"defaultReasoningEffort"`
			Supported              []struct {
				ReasoningEffort string `json:"reasoningEffort"`
				Description     string `json:"description"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileModelCatalog{}, fmt.Errorf("decode model/list: %w", err)
	}
	result := mobileModelCatalog{Items: make([]mobileModelCatalogItem, 0, len(source.Data))}
	for _, item := range source.Data {
		id := firstNonempty(item.ID, item.Model)
		if id == "" {
			return mobileModelCatalog{}, errors.New("model/list item has no id")
		}
		name := firstNonempty(item.DisplayName, item.Model, item.ID)
		efforts := make([]mobileReasoningEffort, 0, len(item.Supported))
		for _, effort := range item.Supported {
			if effort.ReasoningEffort != "" {
				efforts = append(efforts, mobileReasoningEffort{ID: effort.ReasoningEffort, Description: effort.Description})
			}
		}
		result.Items = append(result.Items, mobileModelCatalogItem{
			ID: id, Name: name, Description: item.Description, Default: item.IsDefault,
			Selectable: !item.Hidden, DefaultReasoningEffort: item.DefaultReasoningEffort, ReasoningEfforts: efforts,
		})
	}
	return result, nil
}

func permissionModeDefinitions() []mobilePermissionMode {
	return []mobilePermissionMode{
		{ID: "default", Profile: ":workspace", SandboxMode: "workspace-write", Approval: "on-request", Reviewer: "user"},
		{ID: "autoReview", Profile: ":workspace", SandboxMode: "workspace-write", Approval: "on-request", Reviewer: "auto_review"},
		{ID: "readOnly", Profile: ":read-only", SandboxMode: "read-only", Approval: "on-request", Reviewer: "user"},
		{ID: "fullAccess", Profile: ":danger-full-access", SandboxMode: "danger-full-access", Approval: "never", Reviewer: "user", Dangerous: true},
	}
}

func containsString(values []string, wanted string) bool {
	if values == nil {
		return true
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func normalizeMobilePermissionCatalog(profileRaw, requirementsRaw json.RawMessage, modelIDs ...string) (mobilePermissionCatalog, error) {
	var profiles struct {
		Data []struct {
			ID      string `json:"id"`
			Allowed bool   `json:"allowed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(profileRaw, &profiles); err != nil {
		return mobilePermissionCatalog{}, fmt.Errorf("decode permissionProfile/list: %w", err)
	}
	profileAllowed := make(map[string]bool, len(profiles.Data))
	for _, profile := range profiles.Data {
		profileAllowed[profile.ID] = profile.Allowed
	}
	var requirements struct {
		Requirements *struct {
			AllowedApprovalPolicies   []string        `json:"allowedApprovalPolicies"`
			AllowedApprovalsReviewers []string        `json:"allowedApprovalsReviewers"`
			AllowedPermissionProfiles map[string]bool `json:"allowedPermissionProfiles"`
			AllowedSandboxModes       []string        `json:"allowedSandboxModes"`
			AutoReview                *struct {
				RequiredOnModels []string `json:"requiredOnModels"`
			} `json:"autoReview"`
		} `json:"requirements"`
	}
	if len(requirementsRaw) > 0 && json.Unmarshal(requirementsRaw, &requirements) != nil {
		return mobilePermissionCatalog{}, errors.New("decode configRequirements/read")
	}
	items := permissionModeDefinitions()
	modelID := ""
	if len(modelIDs) > 0 {
		modelID = modelIDs[0]
	}
	for index := range items {
		item := &items[index]
		available, known := profileAllowed[item.Profile]
		if !known || !available {
			item.Available = false
			item.Reason = "permission_profile_unavailable"
			continue
		}
		item.Available = true
		if requirements.Requirements == nil {
			continue
		}
		req := requirements.Requirements
		autoReviewRequired := false
		if req.AutoReview != nil && req.AutoReview.RequiredOnModels != nil {
			autoReviewRequired = modelID != "" && containsString(req.AutoReview.RequiredOnModels, modelID)
		}
		if autoReviewRequired && item.ID != "autoReview" {
			item.Available, item.Reason = false, "auto_review_required"
		} else if req.AllowedPermissionProfiles != nil && !req.AllowedPermissionProfiles[item.Profile] {
			item.Available, item.Reason = false, "permission_profile_restricted"
		} else if !containsString(req.AllowedApprovalPolicies, item.Approval) {
			item.Available, item.Reason = false, "approval_policy_restricted"
		} else if !containsString(req.AllowedApprovalsReviewers, item.Reviewer) {
			item.Available, item.Reason = false, "reviewer_restricted"
		} else if !containsString(req.AllowedSandboxModes, item.SandboxMode) {
			item.Available, item.Reason = false, "sandbox_mode_restricted"
		}
	}
	return mobilePermissionCatalog{Items: items}, nil
}

func (m *Manager) mobileAgents() []mobileAgentSummary {
	snapshots := m.instanceSnapshots()
	result := make([]mobileAgentSummary, 0, len(snapshots))
	for _, item := range snapshots {
		name := item.Name
		if name == "" {
			name = "Codex"
		}
		result = append(result, mobileAgentSummary{ID: item.ID, Name: name, Kind: "codex", Status: item.State, ActiveTurn: item.ActiveTurn, PendingActions: item.PendingActions})
	}
	return append(result, m.backendAgents()...)
}

// mobileControlTarget accepts agentId at the public boundary while preserving
// instanceId for backwards-compatible Desktop callers. It also translates the
// Mobile search spelling to the official Codex app-server parameter.
func (m *Manager) mobileControlTarget(raw json.RawMessage) (*Instance, json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, nil, err
	}
	if _, exists := object["instanceId"]; !exists {
		if agentID, exists := object["agentId"]; exists {
			object["instanceId"] = agentID
		}
	}
	delete(object, "agentId")
	if query, exists := object["query"]; exists {
		object["searchTerm"] = query
		delete(object, "query")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, nil, err
	}
	return m.controlTarget(normalized)
}

func normalizeProjectList(agentID string, raw json.RawMessage) (mobilePage[mobileProjectSummary], error) {
	var source struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			UpdatedAt int64  `json:"updatedAt"`
			Roots     []struct {
				Path string `json:"path"`
			} `json:"roots"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobilePage[mobileProjectSummary]{}, fmt.Errorf("decode project/list: %w", err)
	}
	page := mobilePage[mobileProjectSummary]{Items: make([]mobileProjectSummary, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, item := range source.Data {
		if item.ID == "" {
			return mobilePage[mobileProjectSummary]{}, errors.New("project/list item has no id")
		}
		page.Items = append(page.Items, mobileProjectSummary{ID: item.ID, AgentID: agentID, Name: item.Name, UpdatedAt: item.UpdatedAt})
	}
	return page, nil
}

type mobileProjectCursor struct {
	UpdatedAt int64
	ProjectID string
}

type mobileProviderProject struct {
	ID    string
	Name  string
	Roots []string
}

type mobileProviderEnvironment struct {
	CWD                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots"`
}

type mobileProviderConversation struct {
	ID           string                      `json:"id"`
	ProjectID    string                      `json:"projectId"`
	CWD          string                      `json:"cwd"`
	Environments []mobileProviderEnvironment `json:"environments"`
	UpdatedAt    int64                       `json:"updatedAt"`
}

// mobileProjectList derives project recency from the latest conversation rather
// than the provider's project metadata timestamp. The latter changes when a
// project is edited, but is not guaranteed to change when a thread is updated.
func (m *Manager) mobileProjectList(instance *Instance, params json.RawMessage) (mobilePage[mobileProjectSummary], error) {
	return m.mobileProjectListWithCaller(instance, instance, params)
}

func (m *Manager) mobileProjectListWithCaller(instance *Instance, caller mobileProviderCaller, params json.RawMessage) (mobilePage[mobileProjectSummary], error) {
	var request struct {
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return mobilePage[mobileProjectSummary]{}, fmt.Errorf("decode project list params: %w", err)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > mobileProjectProviderPageLimit {
		limit = mobileProjectProviderPageLimit
	}

	projects, err := fetchMobileProjects(m.ctx, caller)
	if err != nil {
		return mobilePage[mobileProjectSummary]{}, err
	}
	conversations, err := fetchMobileProjectConversations(m.ctx, caller)
	if err != nil {
		return mobilePage[mobileProjectSummary]{}, err
	}

	projectIDs := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		projectIDs[project.ID] = struct{}{}
	}
	latestByProject := make(map[string]int64, len(projects))
	for _, conversation := range conversations {
		projectID := conversation.ProjectID
		if _, exists := projectIDs[projectID]; !exists {
			projectID = mobileConversationProjectID(conversation, projects)
		}
		if projectID == "" {
			continue
		}
		if latest, exists := latestByProject[projectID]; !exists || conversation.UpdatedAt > latest {
			latestByProject[projectID] = conversation.UpdatedAt
		}
	}

	items := make([]mobileProjectSummary, 0, len(projects))
	for _, project := range projects {
		updatedAt, hasConversation := latestByProject[project.ID]
		if !hasConversation {
			continue
		}
		items = append(items, mobileProjectSummary{ID: project.ID, AgentID: instance.descriptor.ID, Name: project.Name, UpdatedAt: updatedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt != items[j].UpdatedAt {
			return items[i].UpdatedAt > items[j].UpdatedAt
		}
		return items[i].ID > items[j].ID
	})

	start := 0
	if request.Cursor != "" {
		cursor, err := decodeMobileProjectCursor(request.Cursor)
		if err != nil {
			return mobilePage[mobileProjectSummary]{}, err
		}
		for start < len(items) && !mobileProjectAfterCursor(items[start], cursor) {
			start++
		}
	}
	if start >= len(items) {
		return mobilePage[mobileProjectSummary]{Items: []mobileProjectSummary{}}, nil
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := mobilePage[mobileProjectSummary]{Items: items[start:end]}
	if end < len(items) {
		page.NextCursor = encodeMobileProjectCursor(mobileProjectCursor{UpdatedAt: items[end-1].UpdatedAt, ProjectID: items[end-1].ID})
	}
	return page, nil
}

func fetchMobileProjects(ctx context.Context, instance mobileProviderCaller) ([]mobileProviderProject, error) {
	projects := make([]mobileProviderProject, 0)
	cursor := ""
	seenCursors := make(map[string]struct{})
	for page := 0; page < mobileMaxProjectProviderPages; page++ {
		params := map[string]any{"limit": mobileProjectProviderPageLimit}
		if cursor != "" {
			params["cursor"] = cursor
		}
		rawParams, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw, err := instance.Call(ctx, "project/list", rawParams)
		if err != nil {
			return nil, err
		}
		var response struct {
			Data []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Roots []struct {
					Path string `json:"path"`
				} `json:"roots"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode project/list: %w", err)
		}
		for _, item := range response.Data {
			if item.ID == "" {
				return nil, errors.New("project/list item has no id")
			}
			roots := make([]string, 0, len(item.Roots))
			for _, root := range item.Roots {
				if strings.TrimSpace(root.Path) != "" {
					roots = append(roots, root.Path)
				}
			}
			projects = append(projects, mobileProviderProject{ID: item.ID, Name: item.Name, Roots: roots})
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			return projects, nil
		}
		cursor = *response.NextCursor
		if _, exists := seenCursors[cursor]; exists {
			return nil, errors.New("project/list returned a cursor loop")
		}
		seenCursors[cursor] = struct{}{}
	}
	return nil, errors.New("project/list exceeded pagination limit")
}

func fetchMobileProjectConversations(ctx context.Context, instance mobileProviderCaller) ([]mobileProviderConversation, error) {
	conversations := make([]mobileProviderConversation, 0)
	cursor := ""
	seenCursors := make(map[string]struct{})
	for page := 0; page < mobileMaxProjectProviderPages; page++ {
		params := map[string]any{
			"limit":         mobileProjectProviderPageLimit,
			"sortKey":       "updated_at",
			"sortDirection": "desc",
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		rawParams, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw, err := instance.Call(ctx, "thread/list", rawParams)
		if err != nil {
			return nil, err
		}
		var response struct {
			Data       []mobileProviderConversation `json:"data"`
			NextCursor *string                      `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode thread/list: %w", err)
		}
		conversations = append(conversations, response.Data...)
		if response.NextCursor == nil || *response.NextCursor == "" {
			return conversations, nil
		}
		cursor = *response.NextCursor
		if _, exists := seenCursors[cursor]; exists {
			return nil, errors.New("thread/list returned a cursor loop")
		}
		seenCursors[cursor] = struct{}{}
	}
	return nil, errors.New("thread/list exceeded pagination limit")
}

func mobileConversationProjectID(conversation mobileProviderConversation, projects []mobileProviderProject) string {
	paths := make([]string, 0, 1+len(conversation.Environments))
	if strings.TrimSpace(conversation.CWD) != "" {
		paths = append(paths, conversation.CWD)
	}
	for _, environment := range conversation.Environments {
		if strings.TrimSpace(environment.CWD) != "" {
			paths = append(paths, environment.CWD)
		}
	}
	if projectID := mobileProjectIDForPaths(paths, projects); projectID != "" {
		return projectID
	}

	paths = paths[:0]
	for _, environment := range conversation.Environments {
		paths = append(paths, environment.RuntimeWorkspaceRoots...)
	}
	return mobileProjectIDForPaths(paths, projects)
}

func mobileProjectIDForPaths(paths []string, projects []mobileProviderProject) string {
	bestProjectID := ""
	bestRootLength := -1
	for _, project := range projects {
		root := mobileProjectMainRoot(project)
		if root == "" {
			continue
		}
		for _, candidate := range paths {
			if !mobilePathWithinRoot(candidate, root) {
				continue
			}
			rootLength := len(filepath.Clean(strings.TrimSpace(root)))
			if rootLength > bestRootLength {
				bestProjectID = project.ID
				bestRootLength = rootLength
			}
		}
	}
	return bestProjectID
}

// The provider Project DTO has no separate primary-root marker. Its roots are
// ordered, so the first non-empty root is the project's main directory; later
// roots are additional working roots and must not claim another project's cwd.
func mobileProjectMainRoot(project mobileProviderProject) string {
	for _, root := range project.Roots {
		if root = strings.TrimSpace(root); root != "" {
			return root
		}
	}
	return ""
}

func mobilePathWithinRoot(candidate, root string) bool {
	candidate = strings.TrimSpace(candidate)
	root = strings.TrimSpace(root)
	if candidate == "" || root == "" {
		return false
	}
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if strings.EqualFold(candidate, root) {
		return true
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func mobileProjectAfterCursor(item mobileProjectSummary, cursor mobileProjectCursor) bool {
	return item.UpdatedAt < cursor.UpdatedAt || (item.UpdatedAt == cursor.UpdatedAt && item.ID < cursor.ProjectID)
}

func encodeMobileProjectCursor(cursor mobileProjectCursor) string {
	value := strconv.FormatInt(cursor.UpdatedAt, 10) + ":" + cursor.ProjectID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeMobileProjectCursor(value string) (mobileProjectCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return mobileProjectCursor{}, fmt.Errorf("invalid project cursor: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return mobileProjectCursor{}, errors.New("invalid project cursor")
	}
	updatedAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return mobileProjectCursor{}, errors.New("invalid project cursor")
	}
	return mobileProjectCursor{UpdatedAt: updatedAt, ProjectID: parts[1]}, nil
}

// mobileConversationList keeps project navigation working for threads created
// before the provider migrated legacy project assignments to project IDs.
// Those threads can still be addressed reliably by their session cwd.
func (m *Manager) mobileConversationList(instance *Instance, params json.RawMessage) (json.RawMessage, error) {
	var request struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(params, &request); err != nil || request.ProjectID == "" {
		return instance.Call(m.ctx, "thread/list", params)
	}

	result, err := instance.Call(m.ctx, "thread/list", params)
	if err != nil || !emptyThreadList(result) {
		return result, err
	}

	root, err := m.projectRoot(instance, request.ProjectID)
	if err != nil || root == "" {
		return result, nil
	}
	var object map[string]any
	if err := json.Unmarshal(params, &object); err != nil {
		return result, nil
	}
	delete(object, "projectId")
	object["cwd"] = root
	fallbackParams, err := json.Marshal(object)
	if err != nil {
		return result, nil
	}
	return instance.Call(m.ctx, "thread/list", fallbackParams)
}

func emptyThreadList(raw json.RawMessage) bool {
	var source struct {
		Data []json.RawMessage `json:"data"`
	}
	return json.Unmarshal(raw, &source) == nil && len(source.Data) == 0
}

func (m *Manager) projectRoot(instance *Instance, projectID string) (string, error) {
	const params = `{"limit":1000}`
	var source struct {
		Data []struct {
			ID    string `json:"id"`
			Roots []struct {
				Path string `json:"path"`
			} `json:"roots"`
		} `json:"data"`
	}
	raw, err := instance.Call(m.ctx, "project/list", json.RawMessage(params))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return "", err
	}
	for _, item := range source.Data {
		if item.ID == projectID && len(item.Roots) > 0 {
			return item.Roots[0].Path, nil
		}
	}
	return "", nil
}

func normalizeConversationList(agentID string, raw json.RawMessage, projects []mobileProviderProject) (mobilePage[mobileConversationSummary], error) {
	var source struct {
		Data []struct {
			ID           string                      `json:"id"`
			ProjectID    string                      `json:"projectId"`
			CWD          string                      `json:"cwd"`
			Environments []mobileProviderEnvironment `json:"environments"`
			Name         string                      `json:"name"`
			Preview      string                      `json:"preview"`
			Status       json.RawMessage             `json:"status"`
			CreatedAt    int64                       `json:"createdAt"`
			UpdatedAt    int64                       `json:"updatedAt"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobilePage[mobileConversationSummary]{}, fmt.Errorf("decode thread/list: %w", err)
	}
	page := mobilePage[mobileConversationSummary]{Items: make([]mobileConversationSummary, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	projectNames := make(map[string]string, len(projects))
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	seen := make(map[string]struct{}, len(source.Data))
	for _, item := range source.Data {
		if item.ID == "" {
			return mobilePage[mobileConversationSummary]{}, errors.New("thread/list item has no id")
		}
		// A provider may repeat a thread while refreshing or crossing a
		// pagination boundary. The thread ID is the stable identity exposed to
		// Mobile, so never emit the same conversation twice in one page.
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		title := strings.TrimSpace(item.Name)
		if title == "" {
			title = firstLine(item.Preview)
		}
		if title == "" {
			title = "Untitled conversation"
		}
		projectID := item.ProjectID
		if _, exists := projectNames[projectID]; !exists {
			projectID = mobileConversationProjectID(mobileProviderConversation{
				ID:           item.ID,
				ProjectID:    item.ProjectID,
				CWD:          item.CWD,
				Environments: item.Environments,
				UpdatedAt:    item.UpdatedAt,
			}, projects)
		}
		status, running := normalizeThreadStatus(item.Status)
		page.Items = append(page.Items, mobileConversationSummary{ID: item.ID, AgentID: agentID, ProjectID: projectID, ProjectName: projectNames[projectID], Title: title, Status: status, Running: running, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	return page, nil
}

func applyConversationAttention(page *mobilePage[mobileConversationSummary], actions []pendingAction) {
	pending := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		if action.ConversationID != "" && !action.Resolved {
			pending[action.ConversationID] = struct{}{}
		}
	}
	for index := range page.Items {
		_, page.Items[index].NeedsAttention = pending[page.Items[index].ID]
	}
}

func normalizeMobileActions(actions []pendingAction) []mobileAction {
	result := make([]mobileAction, 0, len(actions))
	for _, action := range actions {
		if !requiresUserAction(&action) || action.Resolved {
			continue
		}
		item := mobileAction{
			ID:             append(json.RawMessage(nil), action.ID...),
			Kind:           "approval",
			Method:         action.Method,
			ConversationID: action.ConversationID,
			TurnID:         action.TurnID,
			CreatedAt:      action.CreatedAt,
		}
		if action.Method == "item/tool/requestUserInput" {
			item.Kind = "userInput"
			var params struct {
				Questions []mobileActionQuestion `json:"questions"`
			}
			if json.Unmarshal(action.Params, &params) == nil && len(params.Questions) > 0 {
				item.Questions = params.Questions
			}
		}
		result = append(result, item)
	}
	return result
}

type mobileConversationMetadata = provider.ConversationMetadata

func normalizeConversationCreate(raw json.RawMessage) (map[string]any, error) {
	var source struct {
		Thread struct {
			ID        string  `json:"id"`
			ProjectID *string `json:"projectId"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode thread/start: %w", err)
	}
	if source.Thread.ID == "" {
		return nil, errors.New("thread/start response has no thread id")
	}
	result := map[string]any{"id": source.Thread.ID}
	if source.Thread.ProjectID != nil && *source.Thread.ProjectID != "" {
		result["projectId"] = *source.Thread.ProjectID
	}
	return result, nil
}

func mobileConversationStartParams(raw json.RawMessage, projectRoot string) (json.RawMessage, error) {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode conversation.create params: %w", err)
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, errors.New("project root is required")
	}
	params["cwd"] = projectRoot
	return json.Marshal(params)
}

type mobileTimelineBlock = provider.TimelineBlock

type mobileTimelineItem = provider.TimelineItem

type mobileTimelinePage = provider.TimelinePage

type mobilePlanConfirmation = provider.PlanConfirmation

type mobileConversationSnapshot struct {
	Capabilities struct {
		Queue bool `json:"queue"`
	} `json:"capabilities"`
	Conversation     mobileConversationMetadata `json:"conversation"`
	Settings         mobileConversationSettings `json:"settings"`
	Revision         int64                      `json:"revision"`
	Sequence         int64                      `json:"sequence"`
	SyncEpoch        string                     `json:"syncEpoch,omitempty"`
	Timeline         mobileTimelinePage         `json:"timeline"`
	Queue            mobileQueuePage            `json:"queue"`
	PlanConfirmation *mobilePlanConfirmation    `json:"planConfirmation,omitempty"`
}

type mobileQueuedSubmission struct {
	ID              string `json:"id"`
	ClientMessageID string `json:"clientMessageId"`
	Text            string `json:"text"`
	AttachmentCount int    `json:"attachmentCount"`
}

type mobileQueuePage struct {
	Items      []mobileQueuedSubmission `json:"items"`
	NextCursor string                   `json:"nextCursor,omitempty"`
}

type providerQueuedSubmission struct {
	ID                  string            `json:"id"`
	ClientUserMessageID string            `json:"clientUserMessageId"`
	Input               []json.RawMessage `json:"input"`
}

func normalizeQueuedSubmission(source providerQueuedSubmission) (mobileQueuedSubmission, error) {
	if source.ID == "" {
		return mobileQueuedSubmission{}, errors.New("queued submission has no id")
	}
	textParts := make([]string, 0, 1)
	attachments := 0
	for _, rawInput := range source.Input {
		var input struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return mobileQueuedSubmission{}, fmt.Errorf("decode queued input: %w", err)
		}
		switch input.Type {
		case "text":
			if input.Text != "" {
				textParts = append(textParts, input.Text)
			}
		case "image", "localImage":
			attachments++
		}
	}
	return mobileQueuedSubmission{
		ID: source.ID, ClientMessageID: source.ClientUserMessageID,
		Text: strings.Join(textParts, "\n"), AttachmentCount: attachments,
	}, nil
}

func normalizeMobileQueuePage(raw json.RawMessage) (mobileQueuePage, error) {
	var source struct {
		Data       []providerQueuedSubmission `json:"data"`
		NextCursor *string                    `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileQueuePage{}, fmt.Errorf("decode thread/queue/list: %w", err)
	}
	page := mobileQueuePage{Items: make([]mobileQueuedSubmission, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, item := range source.Data {
		normalized, err := normalizeQueuedSubmission(item)
		if err != nil {
			return mobileQueuePage{}, err
		}
		page.Items = append(page.Items, normalized)
	}
	return page, nil
}

func normalizeQueuedMutation(raw json.RawMessage) (mobileQueuedSubmission, error) {
	var source struct {
		QueuedSubmission providerQueuedSubmission `json:"queuedSubmission"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileQueuedSubmission{}, fmt.Errorf("decode queued submission: %w", err)
	}
	return normalizeQueuedSubmission(source.QueuedSubmission)
}

func queuedInputsWithText(current []json.RawMessage, text string) ([]json.RawMessage, error) {
	inputs := make([]json.RawMessage, 0, len(current)+1)
	if text = strings.TrimSpace(text); text != "" {
		encoded, _ := json.Marshal(map[string]any{"type": "text", "text": text})
		inputs = append(inputs, encoded)
	}
	for _, rawInput := range current {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawInput, &kind); err != nil {
			return nil, fmt.Errorf("decode queued input: %w", err)
		}
		if kind.Type != "text" {
			inputs = append(inputs, rawInput)
		}
	}
	if len(inputs) == 0 {
		return nil, errors.New("queued input cannot be empty")
	}
	return inputs, nil
}

func projectedMobileSettings(settings threadCollaborationSettings) mobileConversationSettings {
	mode := settings.Mode
	if mode == "" {
		mode = "default"
	}
	permissionMode := settings.PermissionModeID
	if permissionMode == "" {
		permissionMode = "custom"
	}
	return mobileConversationSettings{
		Mode: mode, ModelID: settings.Model, ReasoningEffort: settings.ReasoningEffort, PermissionModeID: permissionMode,
	}
}

func (m *Manager) mobileCatalogModels(instance mobileProviderCaller) (mobileModelCatalog, error) {
	raw, err := instance.Call(m.ctx, "model/list", json.RawMessage(`{}`))
	if err != nil {
		return mobileModelCatalog{}, err
	}
	catalog, err := normalizeMobileModelCatalog(raw)
	if err != nil {
		return mobileModelCatalog{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	return catalog, nil
}

func (m *Manager) mobileCatalogPermissions(instance mobileProviderCaller, modelID string) (mobilePermissionCatalog, error) {
	profileRaw, err := instance.Call(m.ctx, "permissionProfile/list", json.RawMessage(`{}`))
	if err != nil {
		return mobilePermissionCatalog{}, err
	}
	requirementsRaw, err := instance.Call(m.ctx, "configRequirements/read", json.RawMessage(`{}`))
	if err != nil {
		return mobilePermissionCatalog{}, err
	}
	catalog, err := normalizeMobilePermissionCatalog(profileRaw, requirementsRaw, modelID)
	if err != nil {
		return mobilePermissionCatalog{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	return catalog, nil
}

func (m *Manager) providerThreadSettings(instance mobileProviderCaller, requested mobileConversationSettings, current threadCollaborationSettings, threadID string) (map[string]any, threadCollaborationSettings, error) {
	if requested.Mode != "default" && requested.Mode != "plan" {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "invalid_selection", message: "mode must be default or plan"}
	}
	models, err := m.mobileCatalogModels(instance)
	if err != nil {
		return nil, threadCollaborationSettings{}, err
	}
	var selectedModel *mobileModelCatalogItem
	for index := range models.Items {
		if models.Items[index].ID == requested.ModelID {
			selectedModel = &models.Items[index]
			break
		}
	}
	if selectedModel == nil || requested.ModelID == "" {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "invalid_selection", message: "unknown model"}
	}
	if !selectedModel.Selectable && selectedModel.ID != current.Model {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "selection_unavailable", message: "model is not selectable"}
	}
	effortAllowed := false
	for _, effort := range selectedModel.ReasoningEfforts {
		if effort.ID == requested.ReasoningEffort {
			effortAllowed = true
			break
		}
	}
	if !effortAllowed {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "invalid_selection", message: "reasoning effort is not supported by the model"}
	}
	if requested.PermissionModeID == "custom" && current.PermissionModeID == "custom" {
		// Preserve the live policy, including approval variants this client cannot
		// represent. The provider validates model requirements on this update.
		projected := current
		projected.Mode, projected.Model, projected.ReasoningEffort = requested.Mode, requested.ModelID, requested.ReasoningEffort
		return map[string]any{
			"collaborationMode": map[string]any{"mode": requested.Mode, "settings": map[string]any{"model": requested.ModelID, "reasoning_effort": requested.ReasoningEffort, "developer_instructions": nil}},
			"model":             requested.ModelID, "effort": requested.ReasoningEffort,
		}, projected, nil
	}
	permissions, err := m.mobileCatalogPermissions(instance, requested.ModelID)
	if err != nil {
		return nil, threadCollaborationSettings{}, err
	}
	var selectedPermission *mobilePermissionMode
	for index := range permissions.Items {
		if permissions.Items[index].ID == requested.PermissionModeID {
			selectedPermission = &permissions.Items[index]
			break
		}
	}
	if selectedPermission == nil {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "invalid_selection", message: "unknown permission mode"}
	}
	if !selectedPermission.Available {
		return nil, threadCollaborationSettings{}, &mobileContractError{code: "selection_unavailable", message: firstNonempty(selectedPermission.Reason, "permission mode is unavailable")}
	}
	if requested.PermissionModeID == current.PermissionModeID {
		if _, complete := decodeSandboxPolicy(current.SandboxPolicyJSON); !complete {
			// A legacy record cannot prove the live network/root policy. Only
			// change model/mode; a later authority notification can hydrate it.
			projected := current
			projected.Mode, projected.Model, projected.ReasoningEffort = requested.Mode, requested.ModelID, requested.ReasoningEffort
			return map[string]any{
				"collaborationMode": map[string]any{"mode": requested.Mode, "settings": map[string]any{"model": requested.ModelID, "reasoning_effort": requested.ReasoningEffort, "developer_instructions": nil}},
				"model":             requested.ModelID, "effort": requested.ReasoningEffort,
			}, projected, nil
		}
	}
	modeSettings := map[string]any{"model": requested.ModelID, "developer_instructions": nil, "reasoning_effort": requested.ReasoningEffort}
	policy, err := m.permissionPolicy(instance, threadID, requested.PermissionModeID, current)
	if err != nil {
		return nil, threadCollaborationSettings{}, err
	}
	policyJSON, _ := json.Marshal(policy)
	provider := map[string]any{
		"collaborationMode": map[string]any{"mode": requested.Mode, "settings": modeSettings},
		"model":             requested.ModelID, "effort": requested.ReasoningEffort,
		"approvalPolicy":    selectedPermission.Approval,
		"approvalsReviewer": selectedPermission.Reviewer,
		"sandboxPolicy":     policy,
	}
	projected := threadCollaborationSettings{
		Mode: requested.Mode, Model: requested.ModelID, ReasoningEffort: requested.ReasoningEffort,
		PermissionModeID: selectedPermission.ID, PermissionProfile: selectedPermission.Profile,
		SandboxMode:       sandboxPolicyTypeForProfile(selectedPermission.Profile),
		SandboxPolicyJSON: string(policyJSON),
		ApprovalPolicy:    selectedPermission.Approval, ApprovalsReviewer: selectedPermission.Reviewer,
	}
	return provider, projected, nil
}

func (m *Manager) mobileConversationSettingsUpdate(instance *Instance, params json.RawMessage) (mobileConversationSettings, error) {
	var request struct {
		ThreadID         string                     `json:"threadId"`
		ExpectedRevision *int64                     `json:"expectedRevision"`
		Settings         mobileConversationSettings `json:"settings"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.ExpectedRevision == nil {
		return mobileConversationSettings{}, &mobileContractError{code: "invalid_selection", message: "threadId, expectedRevision and settings are required"}
	}
	if !instance.CanUpdateSettings(request.ThreadID) {
		return mobileConversationSettings{}, &mobileContractError{code: "conversation_busy", message: "the conversation is unavailable or waiting for an action"}
	}
	if err := instance.CheckExpectedRevision(params); err != nil {
		return mobileConversationSettings{}, &mobileContractError{code: "revision_conflict", message: err.Error()}
	}
	instance.mu.Lock()
	current := instance.threadSettings[request.ThreadID]
	notificationVersion := instance.settingsNotifications[request.ThreadID]
	instance.mu.Unlock()
	provider, projected, err := m.providerThreadSettings(instance, request.Settings, current, request.ThreadID)
	if err != nil {
		return mobileConversationSettings{}, err
	}
	provider["threadId"] = request.ThreadID
	providerParams, _ := json.Marshal(provider)
	if _, err = instance.callWithoutSettingsProjection(m.ctx, "thread/settings/update", providerParams); err != nil {
		return mobileConversationSettings{}, err
	}
	// Compare and commit under the same lock. Even an unchanged authoritative
	// notification must win over the requested local projection.
	instance.mu.Lock()
	latest := instance.threadSettings[request.ThreadID]
	if instance.settingsNotifications[request.ThreadID] != notificationVersion {
		instance.mu.Unlock()
		return projectedMobileSettings(latest), nil
	}
	projected.TurnID, projected.Succeeded = latest.TurnID, latest.Succeeded
	instance.threadSettings[request.ThreadID] = projected
	instance.mu.Unlock()
	instance.persistThreadSettingsAsync(request.ThreadID, projected)
	instance.broadcastEvent("frontend.thread.settings.updated", map[string]any{"threadId": request.ThreadID})
	return projectedMobileSettings(projected), nil
}

type mobileContentRef struct {
	AgentID, ThreadID, TurnID, ItemID string
	BlockIndex                        int
	ExpiresAt                         time.Time
}

type mobileContractError struct {
	code    string
	message string
}

type mobileProviderCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func (e *mobileContractError) Error() string { return e.message }

func mobileControlError(err error) *protocol.ControlError {
	var contractErr *mobileContractError
	if errors.As(err, &contractErr) {
		return &protocol.ControlError{Code: contractErr.code, Message: contractErr.message}
	}
	if code, ok := backendErrorCode(err); ok {
		return &protocol.ControlError{Code: code, Message: err.Error()}
	}
	return &protocol.ControlError{Code: "provider_error", Message: err.Error()}
}

func normalizeConversationMetadata(agentID string, raw json.RawMessage) (mobileConversationMetadata, error) {
	var source struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileConversationMetadata{}, fmt.Errorf("decode thread/read: %w", err)
	}
	if len(source.Thread) == 0 || string(source.Thread) == "null" {
		return mobileConversationMetadata{}, errors.New("thread/read response has no thread")
	}
	var thread struct {
		ID        string          `json:"id"`
		ProjectID string          `json:"projectId"`
		Name      string          `json:"name"`
		Preview   string          `json:"preview"`
		Status    json.RawMessage `json:"status"`
		CreatedAt int64           `json:"createdAt"`
		UpdatedAt int64           `json:"updatedAt"`
	}
	if err := json.Unmarshal(source.Thread, &thread); err != nil {
		return mobileConversationMetadata{}, fmt.Errorf("decode thread/read thread: %w", err)
	}
	if thread.ID == "" {
		return mobileConversationMetadata{}, errors.New("thread/read response has no thread id")
	}
	title := strings.TrimSpace(thread.Name)
	if title == "" {
		title = firstLine(thread.Preview)
	}
	if title == "" {
		title = "Untitled conversation"
	}
	status, running := normalizeThreadStatus(thread.Status)
	return mobileConversationMetadata{ID: thread.ID, AgentID: agentID, ProjectID: thread.ProjectID, Title: title, Preview: thread.Preview, Status: status, Running: running, CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt}, nil
}

func normalizeTimelinePage(raw json.RawMessage) (mobileTimelinePage, []map[string]any, error) {
	var source struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return mobileTimelinePage{}, nil, fmt.Errorf("decode thread/items/list: %w", err)
	}
	page := mobileTimelinePage{Items: make([]mobileTimelineItem, 0, len(source.Data))}
	if source.NextCursor != nil {
		page.NextCursor = *source.NextCursor
	}
	for _, rawEntry := range source.Data {
		// The current Codex app-server returns ThreadItemEntry values shaped as
		// {"item": <ThreadItem>, "turnId": "..."}. Keep accepting the old
		// flattened shape so older recorded fixtures remain useful.
		rawItem := rawEntry
		turnID, _ := rawEntry["turnId"].(string)
		if nested, ok := rawEntry["item"].(map[string]any); ok {
			rawItem = nested
		} else if value, exists := rawEntry["item"]; exists && value != nil {
			return mobileTimelinePage{}, nil, errors.New("thread/items/list item has invalid item")
		}
		id, _ := rawItem["id"].(string)
		if id == "" {
			return mobileTimelinePage{}, nil, errors.New("thread/items/list item has no id")
		}
		kind, _ := rawItem["type"].(string)
		if kind == "" {
			kind = "item"
		}
		if kind == "hookPrompt" {
			continue
		}
		if turnID == "" {
			turnID, _ = rawItem["turnId"].(string)
		}
		status := compactStatus(rawItem["status"])
		clientMessageID := ""
		if kind == "userMessage" {
			clientMessageID, _ = rawItem["clientId"].(string)
		}
		page.Items = append(page.Items, mobileTimelineItem{ID: id, ClientMessageID: clientMessageID, TurnID: turnID, Kind: kind, Status: status, Blocks: timelineBlocks(rawItem)})
	}
	return page, source.Data, nil
}

func compactStatus(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	raw, _ := json.Marshal(value)
	var object map[string]any
	if json.Unmarshal(raw, &object) == nil {
		for key := range object {
			return key
		}
	}
	return "unknown"
}

func timelineBlocks(item map[string]any) []mobileTimelineBlock {
	if summary, ok := timelineSummary(item); ok {
		return []mobileTimelineBlock{summary}
	}
	if kind, _ := item["type"].(string); kind == "commandExecution" {
		if command, ok := item["command"].(string); ok && strings.TrimSpace(command) != "" {
			return []mobileTimelineBlock{{Kind: "command", Text: command}}
		}
		return nil
	}
	blocks := make([]mobileTimelineBlock, 0, 4)
	appendText := func(kind string, value any) {
		for _, text := range readableStrings(value) {
			if strings.TrimSpace(text) != "" {
				blocks = append(blocks, mobileTimelineBlock{Kind: kind, Text: text})
			}
		}
	}
	if kind, _ := item["type"].(string); kind == "reasoning" {
		blocks = blocks[:0]
		appendText("reasoning", item["content"])
		appendText("reasoningSummary", item["summary"])
		return blocks
	}
	// Plan text is Markdown and must remain a single block so the mobile
	// client can render the raw source instead of a lossy plaintext summary.
	if kind, _ := item["type"].(string); kind == "plan" {
		if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
			return []mobileTimelineBlock{{Kind: "plan", Text: text}}
		}
	}
	for _, field := range []struct{ key, kind string }{
		{"text", "text"}, {"message", "text"}, {"content", "content"}, {"summary", "reasoning"},
		{"command", "command"}, {"aggregatedOutput", "output"}, {"output", "output"},
		{"diff", "diff"}, {"changes", "diff"}, {"result", "result"}, {"error", "error"},
	} {
		if value, ok := item[field.key]; ok {
			appendText(field.kind, value)
		}
	}
	if len(blocks) == 0 {
		if name, ok := item["name"].(string); ok && name != "" {
			blocks = append(blocks, mobileTimelineBlock{Kind: "summary", Text: name})
		}
	}
	return blocks
}

func timelineSummary(item map[string]any) (mobileTimelineBlock, bool) {
	kind, _ := item["type"].(string)
	text := func(rawValue any) string {
		value, _ := rawValue.(string)
		return summaryValue(oneLine(value))
	}
	qualified := func(namespace, name string) string {
		if namespace == "" {
			return name
		}
		if name == "" {
			return namespace
		}
		return namespace + "." + name
	}
	result := func(value string) (mobileTimelineBlock, bool) {
		value = summaryValue(value)
		fallback := value
		if fallback == "" {
			fallback = kind
		}
		return mobileTimelineBlock{Kind: "summary", Text: fallback, SummaryKind: kind, SummaryValue: value}, true
	}
	switch kind {
	case "functionCallOutput":
		name, _ := item["name"].(string)
		namespace, _ := item["namespace"].(string)
		return result(qualified(oneLine(namespace), oneLine(name)))
	case "mcpToolCall":
		server, _ := item["server"].(string)
		tool, _ := item["tool"].(string)
		server, tool = oneLine(server), oneLine(tool)
		return result(strings.Trim(server+"/"+tool, "/"))
	case "dynamicToolCall":
		namespace, _ := item["namespace"].(string)
		tool, _ := item["tool"].(string)
		return result(qualified(oneLine(namespace), oneLine(tool)))
	case "collabAgentToolCall":
		tool, _ := item["tool"].(string)
		return result(oneLine(tool))
	case "subAgentActivity":
		activity, _ := item["kind"].(string)
		return result(oneLine(activity))
	case "webSearch":
		query, _ := item["query"].(string)
		return result(text(query))
	case "imageView":
		path, _ := item["path"].(string)
		return result(baseName(path))
	case "sleep":
		return result(durationText(item["durationMs"]) + "ms")
	case "imageGeneration":
		return result("")
	case "enteredReviewMode":
		review, _ := item["review"].(string)
		return result(text(review))
	case "exitedReviewMode":
		review, _ := item["review"].(string)
		return result(text(review))
	case "contextCompaction":
		return result("")
	default:
		return mobileTimelineBlock{}, false
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func summaryValue(value string) string {
	if len(value) <= mobileSummaryValueBytes {
		return value
	}
	return utf8Prefix(value, mobileSummaryValueBytes)
}

func durationText(value any) string {
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%g", typed)
	case float32:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case json.Number:
		return typed.String()
	default:
		return "0"
	}
}

func baseName(path string) string {
	path = strings.TrimSpace(path)
	if index := strings.LastIndexAny(path, `/\\`); index >= 0 {
		return path[index+1:]
	}
	return path
}

// firstRawLines returns the first maxLines source lines without converting or
// reformatting Markdown. SplitAfter preserves the original line endings,
// including CRLF and blank lines, which keeps the preview faithful to the
// provider's source document.
func firstRawLines(value string, maxLines int) (string, bool) {
	if maxLines <= 0 || value == "" {
		return value, false
	}
	lines := strings.SplitAfter(value, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines {
		return value, false
	}
	return strings.Join(lines[:maxLines], ""), true
}

func readableStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, readableStrings(item)...)
		}
		return result
	case map[string]any:
		result := make([]string, 0, 2)
		for _, key := range []string{"text", "message", "path", "diff", "output", "content", "title", "name", "url"} {
			if child, ok := typed[key]; ok {
				result = append(result, readableStrings(child)...)
			}
		}
		return result
	default:
		return nil
	}
}

func utf8Prefix(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func utf8ContentChunk(value string, offset int) (string, int, bool, error) {
	if offset < 0 || offset > len(value) || (offset < len(value) && !utf8.RuneStart(value[offset])) {
		return "", offset, false, errors.New("offset is not a valid UTF-8 boundary")
	}
	end := offset + mobileContentChunkBytes
	if end > len(value) {
		end = len(value)
	}
	for end > offset && end < len(value) && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[offset:end], end, end < len(value), nil
}

func (m *Manager) registerContentRef(agentID, threadID, turnID, itemID string, blockIndex int) string {
	now := time.Now()
	m.contentRefMu.Lock()
	defer m.contentRefMu.Unlock()
	if m.contentRefs == nil {
		m.contentRefs = make(map[string]mobileContentRef)
	}
	for token, ref := range m.contentRefs {
		if now.After(ref.ExpiresAt) || len(m.contentRefs) >= mobileMaxContentRefs {
			delete(m.contentRefs, token)
		}
	}
	token := randomToken(24)
	m.contentRefs[token] = mobileContentRef{AgentID: agentID, ThreadID: threadID, TurnID: turnID, ItemID: itemID, BlockIndex: blockIndex, ExpiresAt: now.Add(mobileContentRefTTL)}
	return token
}

func (m *Manager) resolveContentRef(token, agentID, threadID string, now time.Time) (mobileContentRef, error) {
	m.contentRefMu.Lock()
	defer m.contentRefMu.Unlock()
	ref, ok := m.contentRefs[token]
	if !ok || now.After(ref.ExpiresAt) || ref.AgentID != agentID || ref.ThreadID != threadID {
		if ok && now.After(ref.ExpiresAt) {
			delete(m.contentRefs, token)
		}
		return mobileContentRef{}, &mobileContractError{code: "content_ref_expired", message: "content reference expired or does not belong to this conversation"}
	}
	ref.ExpiresAt = now.Add(mobileContentRefTTL)
	m.contentRefs[token] = ref
	return ref, nil
}

func (m *Manager) applyContentRefs(page *mobileTimelinePage, agentID, threadID string, force bool) {
	for itemIndex := range page.Items {
		for blockIndex := range page.Items[itemIndex].Blocks {
			block := &page.Items[itemIndex].Blocks[blockIndex]
			if block.Kind == "plan" {
				preview, truncated := firstRawLines(block.Text, 5)
				if !truncated {
					continue
				}
				block.TotalBytes = len(block.Text)
				block.Text = preview
				block.Truncated = true
				block.ContentRef = m.registerContentRef(agentID, threadID, page.Items[itemIndex].TurnID, page.Items[itemIndex].ID, blockIndex)
				continue
			}
			if block.Kind == "summary" {
				continue
			}
			threshold, preview := mobileLargeBlockBytes, mobileInlinePreviewBytes
			if force {
				threshold, preview = 4<<10, 1<<10
			}
			if len(block.Text) <= threshold {
				continue
			}
			block.TotalBytes = len(block.Text)
			block.Text = utf8Prefix(block.Text, preview)
			block.Truncated = true
			block.ContentRef = m.registerContentRef(agentID, threadID, page.Items[itemIndex].TurnID, page.Items[itemIndex].ID, blockIndex)
		}
	}
}

func timelineLimit(value int) (int, error) {
	if value == 0 {
		return mobileDefaultTimelineLimit, nil
	}
	if value < 1 || value > mobileMaxTimelineLimit {
		return 0, &mobileContractError{code: "invalid_params", message: "limit must be between 1 and 50"}
	}
	return value, nil
}

type mobileTimelineSegment struct {
	Page      mobileTimelinePage
	Cursor    string
	PageLimit int
}

func (m *Manager) fetchTimelineSegments(instance mobileProviderCaller, threadID, cursor string, limit int) ([]mobileTimelineSegment, error) {
	return m.fetchTimelineSegmentsScoped(instance, threadID, "", cursor, limit)
}

func (m *Manager) fetchTimelineSegmentsScoped(instance mobileProviderCaller, threadID, turnID, cursor string, limit int) ([]mobileTimelineSegment, error) {
	segments := make([]mobileTimelineSegment, 0, 1)
	remaining := limit
	providerCursor := cursor
	for pageCount := 0; pageCount < mobileMaxTimelineProviderPages && remaining > 0; pageCount++ {
		providerParams := map[string]any{"threadId": threadID, "limit": remaining, "sortDirection": "desc"}
		if turnID != "" {
			providerParams["turnId"] = turnID
			providerParams["sortDirection"] = "asc"
		}
		if providerCursor != "" {
			providerParams["cursor"] = providerCursor
		}
		rawParams, _ := json.Marshal(providerParams)
		raw, callErr := instance.Call(m.ctx, "thread/items/list", rawParams)
		if callErr != nil {
			return nil, callErr
		}
		page, _, decodeErr := normalizeTimelinePage(raw)
		if decodeErr != nil {
			return nil, &mobileContractError{code: "invalid_provider_response", message: decodeErr.Error()}
		}
		if len(page.Items) > remaining {
			page.Items = page.Items[:remaining]
		}
		segments = append(segments, mobileTimelineSegment{Page: page, Cursor: providerCursor, PageLimit: remaining})
		remaining -= len(page.Items)
		if remaining == 0 || page.NextCursor == "" {
			break
		}
		providerCursor = page.NextCursor
	}
	return segments, nil
}

func (m *Manager) composeTimelineSegments(segments []mobileTimelineSegment, agentID, threadID string, force bool) mobileTimelinePage {
	page := mobileTimelinePage{Items: make([]mobileTimelineItem, 0)}
	for index := range segments {
		segment := &segments[index]
		m.applyContentRefs(&segment.Page, agentID, threadID, force)
		page.Items = append(page.Items, segment.Page.Items...)
		page.NextCursor = segment.Page.NextCursor
	}
	return page
}

func (m *Manager) readTimelinePageAttempt(instance mobileProviderCaller, agentID, threadID, cursor string, limit int, force bool) (mobileTimelinePage, error) {
	segments, err := m.fetchCompatibleTimeline(instance, agentID, threadID, cursor, limit)
	if err != nil {
		return mobileTimelinePage{}, err
	}
	return m.composeTimelineSegments(segments, agentID, threadID, force), nil
}

func (m *Manager) readTimelinePage(instance mobileProviderCaller, agentID, threadID, cursor string, requestedLimit int) (mobileTimelinePage, error) {
	limit, err := timelineLimit(requestedLimit)
	if err != nil {
		return mobileTimelinePage{}, err
	}
	for {
		page, err := m.readTimelinePageAttempt(instance, agentID, threadID, cursor, limit, false)
		if err != nil {
			return mobileTimelinePage{}, err
		}
		encoded, _ := json.Marshal(page)
		if len(encoded) <= mobileTimelinePageBytes || limit == 1 {
			if len(encoded) > mobileTimelinePageBytes {
				page, err = m.readTimelinePageAttempt(instance, agentID, threadID, cursor, limit, true)
				if err != nil {
					return mobileTimelinePage{}, err
				}
			}
			return page, nil
		}
		limit /= 2
		if limit < 1 {
			limit = 1
		}
	}
}

func (m *Manager) conversationSnapshot(instance *Instance, agentID string, params json.RawMessage) (mobileConversationSnapshot, error) {
	return m.stableConversationSnapshot(instance, agentID, params)
}

func (m *Manager) conversationSnapshotOnce(instance *Instance, agentID string, params json.RawMessage) (mobileConversationSnapshot, error) {
	var request struct {
		ThreadID string `json:"threadId"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	metadataParams, _ := json.Marshal(map[string]any{"threadId": request.ThreadID, "includeTurns": false})
	raw, err := instance.Call(m.ctx, "thread/read", metadataParams)
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	metadata, err := normalizeConversationMetadata(agentID, raw)
	if err != nil {
		return mobileConversationSnapshot{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	var history struct {
		Thread struct {
			Path *string `json:"path"`
			mobileProviderConversation
		} `json:"thread"`
	}
	_ = json.Unmarshal(raw, &history)
	if metadata.ProjectID == "" && (history.Thread.CWD != "" || len(history.Thread.Environments) > 0) {
		projects, projectErr := fetchMobileProjects(m.ctx, instance)
		if projectErr != nil {
			return mobileConversationSnapshot{}, projectErr
		}
		metadata.ProjectID = mobileConversationProjectID(history.Thread.mobileProviderConversation, projects)
	}
	unpersisted := history.Thread.Path == nil || metadataOnlyRollout(*history.Thread.Path)
	allowEmpty := !metadata.Running && instance.emptyCreatedThread(request.ThreadID) && unpersisted
	page, err := m.readTimelinePage(instance, agentID, request.ThreadID, "", request.Limit)
	if allowEmpty && (noRollout(err, request.ThreadID) || emptyHistoryUnsupported(err)) {
		page = mobileTimelinePage{Items: []mobileTimelineItem{}}
		err = nil
	}
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	queue, queueAvailable, err := m.compatibleQueue(instance, request.ThreadID, allowEmpty)
	if err != nil {
		return mobileConversationSnapshot{}, err
	}
	revision, sequence := instance.ConversationVersion(request.ThreadID)
	settings, _ := instance.collaborationSettings(request.ThreadID)
	snapshot := mobileConversationSnapshot{Conversation: metadata, Settings: projectedMobileSettings(settings), Revision: revision, Sequence: sequence, SyncEpoch: m.syncEpoch, Timeline: page, Queue: queue}
	snapshot.Capabilities.Queue = queueAvailable
	snapshot.PlanConfirmation = planConfirmationFor(instance, request.ThreadID, metadata, page, revision)
	return snapshot, nil
}

func planConfirmationFor(instance *Instance, threadID string, metadata mobileConversationMetadata, page mobileTimelinePage, revision int64) *mobilePlanConfirmation {
	if metadata.Running || !instance.CanStartTurn(threadID) {
		return nil
	}
	latestTurnID := ""
	for _, item := range page.Items {
		if item.TurnID != "" {
			latestTurnID = item.TurnID
			break
		}
	}
	if latestTurnID == "" {
		return nil
	}
	planItemID := ""
	for _, item := range page.Items {
		if item.TurnID == latestTurnID && strings.EqualFold(item.Kind, "plan") {
			planItemID = item.ID
			break
		}
	}
	if planItemID == "" {
		return nil
	}
	settings, ok := instance.collaborationSettings(threadID)
	if !ok || settings.Mode != "plan" || settings.TurnID != latestTurnID || !settings.Succeeded {
		return nil
	}
	return &mobilePlanConfirmation{PlanItemID: planItemID, TurnID: latestTurnID, Revision: revision}
}

func (m *Manager) mobilePlanRespond(instance *Instance, agentID string, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID         string `json:"threadId"`
		PlanItemID       string `json:"planItemId"`
		ExpectedRevision *int64 `json:"expectedRevision"`
		Decision         string `json:"decision"`
		Text             string `json:"text"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.PlanItemID == "" || request.ExpectedRevision == nil {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, planItemId, and expectedRevision are required"}
	}
	request.Text = strings.TrimSpace(request.Text)
	if (request.Decision != "implement" && request.Decision != "revise") || request.Text == "" {
		return nil, &mobileContractError{code: "invalid_params", message: "decision must be implement or revise and text is required"}
	}
	if !instance.CanStartTurn(request.ThreadID) {
		return nil, &mobileContractError{code: "conversation_busy", message: "the conversation is active or waiting for an action"}
	}
	if err := instance.CheckExpectedRevision(params); err != nil {
		return nil, &mobileContractError{code: "revision_conflict", message: err.Error()}
	}
	settings, ok := instance.collaborationSettings(request.ThreadID)
	if !ok || settings.Model == "" {
		return nil, &mobileContractError{code: "plan_mode_unavailable", message: "the plan collaboration settings are unavailable"}
	}
	snapshot, err := m.conversationSnapshot(instance, agentID, params)
	if err != nil {
		return nil, err
	}
	confirmation := snapshot.PlanConfirmation
	if confirmation == nil || confirmation.PlanItemID != request.PlanItemID || confirmation.Revision != *request.ExpectedRevision {
		return nil, &mobileContractError{code: "stale_plan", message: "the plan is no longer the latest completed plan"}
	}
	if !ok || settings.Mode != "plan" || settings.TurnID != confirmation.TurnID || settings.Model == "" {
		return nil, &mobileContractError{code: "plan_mode_unavailable", message: "the plan collaboration settings are unavailable"}
	}
	providerParams, _ := json.Marshal(planTurnStartParams(request.ThreadID, request.Decision, request.Text, settings))
	if err := instance.reserveTurn(params); err != nil {
		return nil, err
	}
	result, callErr := instance.Call(m.ctx, "turn/start", providerParams)
	if callErr != nil {
		return nil, callErr
	}
	response := map[string]any{"started": true}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(result, &started) == nil && started.Turn.ID != "" {
		response["turnId"] = started.Turn.ID
	}
	return response, nil
}

func planTurnStartParams(threadID, decision, text string, settings threadCollaborationSettings) map[string]any {
	mode := "plan"
	if decision == "implement" {
		mode = "default"
	}
	modeSettings := map[string]any{"model": settings.Model, "developer_instructions": nil}
	if settings.ReasoningEffort != "" {
		modeSettings["reasoning_effort"] = settings.ReasoningEffort
	}
	params := map[string]any{
		"threadId":          threadID,
		"input":             []map[string]any{{"type": "text", "text": text}},
		"collaborationMode": map[string]any{"mode": mode, "settings": modeSettings},
	}
	// Omitting permission overrides inherits the live thread policy, including
	// fields not understood by this client. A plan response only changes mode.
	return params
}

func (m *Manager) mobileQueueList(instance mobileProviderCaller, threadID string) (mobileQueuePage, error) {
	if threadID == "" {
		return mobileQueuePage{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	params, _ := json.Marshal(map[string]any{"threadId": threadID, "limit": mobileMaxTimelineLimit})
	raw, err := instance.Call(m.ctx, "thread/queue/list", params)
	if err != nil {
		return mobileQueuePage{}, err
	}
	page, err := normalizeMobileQueuePage(raw)
	if err != nil {
		return mobileQueuePage{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	return page, nil
}

func (m *Manager) mobileQueueAdd(instance *Instance, params json.RawMessage) (mobileQueuedSubmission, error) {
	var request struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_params", message: "threadId is required"}
	}
	if !instance.CanSubmitQueue(request.ThreadID) {
		return mobileQueuedSubmission{}, &mobileContractError{code: "conversation_busy", message: "the conversation is waiting for an action"}
	}
	normalized, err := m.normalizeTurnStart(instance.descriptor.ID, params)
	if err != nil {
		return mobileQueuedSubmission{}, err
	}
	var provider map[string]any
	if err = json.Unmarshal(normalized, &provider); err != nil {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_params", message: err.Error()}
	}
	input, _ := provider["input"].([]any)
	messageID, _ := provider["clientUserMessageId"].(string)
	if len(input) == 0 || messageID == "" {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_params", message: "clientMessageId and non-empty input are required"}
	}
	provider = map[string]any{
		"threadId": request.ThreadID, "clientUserMessageId": messageID, "input": input,
	}
	providerParams, _ := json.Marshal(provider)
	raw, err := instance.Call(m.ctx, "thread/queue/add", providerParams)
	if err != nil {
		return mobileQueuedSubmission{}, err
	}
	result, err := normalizeQueuedMutation(raw)
	if err != nil {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	m.releaseAttachments(attachmentIDs(params))
	return result, nil
}

func (m *Manager) providerQueuedSubmission(instance mobileProviderCaller, threadID, submissionID string) (providerQueuedSubmission, error) {
	cursor := ""
	for page := 0; page < mobileMaxTimelineProviderPages; page++ {
		provider := map[string]any{"threadId": threadID, "limit": mobileMaxTimelineLimit}
		if cursor != "" {
			provider["cursor"] = cursor
		}
		params, _ := json.Marshal(provider)
		raw, err := instance.Call(m.ctx, "thread/queue/list", params)
		if err != nil {
			return providerQueuedSubmission{}, err
		}
		var response struct {
			Data       []providerQueuedSubmission `json:"data"`
			NextCursor *string                    `json:"nextCursor"`
		}
		if err = json.Unmarshal(raw, &response); err != nil {
			return providerQueuedSubmission{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
		}
		for _, item := range response.Data {
			if item.ID == submissionID {
				return item, nil
			}
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		cursor = *response.NextCursor
	}
	return providerQueuedSubmission{}, &mobileContractError{code: "queue_item_not_found", message: "the queued message no longer exists"}
}

func (m *Manager) mobileQueueUpdate(instance *Instance, params json.RawMessage) (mobileQueuedSubmission, error) {
	var request struct {
		ThreadID         string `json:"threadId"`
		QueuedSubmission string `json:"queuedSubmissionId"`
		Text             string `json:"text"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.QueuedSubmission == "" {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_params", message: "threadId and queuedSubmissionId are required"}
	}
	if !instance.CanSubmitQueue(request.ThreadID) {
		return mobileQueuedSubmission{}, &mobileContractError{code: "conversation_busy", message: "the conversation is waiting for an action"}
	}
	current, err := m.providerQueuedSubmission(instance, request.ThreadID, request.QueuedSubmission)
	if err != nil {
		return mobileQueuedSubmission{}, err
	}
	inputs, err := queuedInputsWithText(current.Input, request.Text)
	if err != nil {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_params", message: err.Error()}
	}
	providerParams, _ := json.Marshal(map[string]any{
		"threadId": request.ThreadID, "queuedSubmissionId": request.QueuedSubmission, "input": inputs,
	})
	raw, err := instance.Call(m.ctx, "thread/queue/update", providerParams)
	if err != nil {
		return mobileQueuedSubmission{}, err
	}
	result, err := normalizeQueuedMutation(raw)
	if err != nil {
		return mobileQueuedSubmission{}, &mobileContractError{code: "invalid_provider_response", message: err.Error()}
	}
	return result, nil
}

func (m *Manager) mobileQueueDelete(instance *Instance, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID         string `json:"threadId"`
		QueuedSubmission string `json:"queuedSubmissionId"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.QueuedSubmission == "" {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId and queuedSubmissionId are required"}
	}
	if !instance.CanSubmitQueue(request.ThreadID) {
		return nil, &mobileContractError{code: "conversation_busy", message: "the conversation is waiting for an action"}
	}
	providerParams, _ := json.Marshal(map[string]any{"threadId": request.ThreadID, "queuedSubmissionId": request.QueuedSubmission})
	if _, err := instance.Call(m.ctx, "thread/queue/delete", providerParams); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "queuedSubmissionId": request.QueuedSubmission}, nil
}

func (m *Manager) mobileQueueStart(instance *Instance, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID         string `json:"threadId"`
		QueuedSubmission string `json:"queuedSubmissionId"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.QueuedSubmission == "" {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId and queuedSubmissionId are required"}
	}
	if !instance.CanSubmitQueue(request.ThreadID) {
		return nil, &mobileContractError{code: "conversation_busy", message: "the conversation is waiting for an action"}
	}
	providerParams, _ := json.Marshal(map[string]any{"threadId": request.ThreadID, "queuedSubmissionId": request.QueuedSubmission})
	raw, err := instance.Call(m.ctx, "thread/queue/start", providerParams)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"started": true, "queuedSubmissionId": request.QueuedSubmission}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &started) == nil && started.Turn.ID != "" {
		result["turnId"] = started.Turn.ID
	}
	return result, nil
}

func (m *Manager) mobileConversationRead(instance *Instance, params json.RawMessage) (mobileConversationSnapshot, error) {
	return m.conversationSnapshot(instance, instance.descriptor.ID, params)
}

func (m *Manager) conversationTimeline(instance *Instance, agentID string, params json.RawMessage) (mobileTimelinePage, error) {
	var request struct {
		ThreadID string `json:"threadId"`
		Cursor   string `json:"cursor"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.Cursor == "" {
		return mobileTimelinePage{}, &mobileContractError{code: "invalid_params", message: "threadId and cursor are required"}
	}
	return m.readTimelinePage(instance, agentID, request.ThreadID, request.Cursor, request.Limit)
}

func (m *Manager) mobileTimelineList(instance *Instance, params json.RawMessage) (mobileTimelinePage, error) {
	return m.conversationTimeline(instance, instance.descriptor.ID, params)
}

func (m *Manager) findTimelineBlock(instance mobileProviderCaller, threadID, turnID, itemID string, blockIndex int) (string, error) {
	if threadID == "" || itemID == "" || blockIndex < 0 {
		return "", &mobileContractError{code: "invalid_params", message: "threadId, itemId and a non-negative blockIndex are required"}
	}
	agentID := ""
	if i, ok := instance.(*Instance); ok {
		agentID = i.descriptor.ID
	}
	cursor := ""
	seen := make(map[string]bool)
	for {
		if seen[cursor] {
			return "", &mobileContractError{code: "invalid_provider_response", message: "timeline returned a cursor loop"}
		}
		seen[cursor] = true
		segments, err := m.fetchCompatibleTimelineScoped(instance, agentID, threadID, turnID, cursor, mobileMaxTimelineLimit)
		if err != nil {
			return "", err
		}
		next := ""
		for _, segment := range segments {
			for _, item := range segment.Page.Items {
				if item.ID != itemID || (turnID != "" && item.TurnID != "" && item.TurnID != turnID) {
					continue
				}
				if blockIndex >= len(item.Blocks) || item.Blocks[blockIndex].Kind == "summary" || item.Blocks[blockIndex].Text == "" {
					return "", &mobileContractError{code: "content_not_found", message: "referenced content is not available"}
				}
				return item.Blocks[blockIndex].Text, nil
			}
			next = segment.Page.NextCursor
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return "", &mobileContractError{code: "content_not_found", message: "referenced content is no longer available"}
}

func (m *Manager) conversationContent(instance *Instance, agentID string, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID   string `json:"threadId"`
		ContentRef string `json:"contentRef"`
		Offset     int    `json:"offset"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.ContentRef == "" || request.Offset < 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, contentRef and a non-negative offset are required"}
	}
	ref, err := m.resolveContentRef(request.ContentRef, agentID, request.ThreadID, time.Now())
	if err != nil {
		return nil, err
	}
	text, err := m.findTimelineBlock(instance, ref.ThreadID, ref.TurnID, ref.ItemID, ref.BlockIndex)
	if err != nil {
		return nil, err
	}
	chunk, next, hasMore, chunkErr := utf8ContentChunk(text, request.Offset)
	if chunkErr != nil {
		return nil, &mobileContractError{code: "invalid_params", message: chunkErr.Error()}
	}
	return map[string]any{"text": chunk, "nextOffset": next, "hasMore": hasMore, "totalBytes": len(text)}, nil
}

func (m *Manager) mobileContentRead(instance *Instance, params json.RawMessage) (map[string]any, error) {
	return m.conversationContent(instance, instance.descriptor.ID, params)
}

func (m *Manager) conversationContentResolve(instance *Instance, agentID string, params json.RawMessage) (map[string]any, error) {
	var request struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ItemID     string `json:"itemId"`
		BlockIndex int    `json:"blockIndex"`
	}
	if json.Unmarshal(params, &request) != nil || request.ThreadID == "" || request.TurnID == "" || request.ItemID == "" || request.BlockIndex < 0 {
		return nil, &mobileContractError{code: "invalid_params", message: "threadId, turnId, itemId and a non-negative blockIndex are required"}
	}
	text, err := m.findTimelineBlock(instance, request.ThreadID, request.TurnID, request.ItemID, request.BlockIndex)
	if err != nil {
		return nil, err
	}
	contentRef := m.registerContentRef(agentID, request.ThreadID, request.TurnID, request.ItemID, request.BlockIndex)
	return map[string]any{"contentRef": contentRef, "totalBytes": len(text)}, nil
}

func (m *Manager) mobileContentResolve(instance *Instance, params json.RawMessage) (map[string]any, error) {
	return m.conversationContentResolve(instance, instance.descriptor.ID, params)
}

func normalizeThreadStatus(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "unknown", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		lower := strings.ToLower(text)
		return text, strings.Contains(lower, "active") || strings.Contains(lower, "running") || strings.Contains(lower, "waiting")
	}
	compact := strings.ToLower(string(raw))
	var tagged struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &tagged) == nil {
		switch tagged.Type {
		case "idle", "notLoaded", "systemError":
			return tagged.Type, false
		}
	}
	switch {
	case strings.Contains(compact, "waitingonapproval"):
		return "waitingOnApproval", true
	case strings.Contains(compact, "waitingonuserinput"):
		return "waitingOnUserInput", true
	case strings.Contains(compact, "active") || strings.Contains(compact, "running"):
		return "running", true
	default:
		return "unknown", false
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	const maxTitle = 80
	if len([]rune(value)) > maxTitle {
		return string([]rune(value)[:maxTitle])
	}
	return value
}
