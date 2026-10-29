package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/provider"
)

// MinimumVersion is the oldest CLI whose headless control protocol has been
// recorded (see docs/claude-code-provider/protocol.md).
const MinimumVersion = "2.1.306"

// Runtime is a resolved Claude Code executable.
type Runtime struct {
	Executable string
	Version    string
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ResolveRuntime finds the native Claude Code executable. A configured path
// wins; otherwise PATH is searched, unwrapping the npm .cmd shim to the
// packaged claude.exe so no command interpreter is involved, and finally the
// native installer location is tried.
func ResolveRuntime(ctx context.Context, configured string) (Runtime, error) {
	candidates := make([]string, 0, 4)
	if configured != "" {
		candidates = append(candidates, unwrapShim(configured), configured)
	} else {
		candidates = append(candidates, pathCandidates()...)
		if home, err := os.UserHomeDir(); err == nil {
			name := "claude"
			if runtime.GOOS == "windows" {
				name = "claude.exe"
			}
			candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
		}
	}
	var lastErr error = errors.New("claude executable not found")
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		version, err := executableVersion(ctx, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if compareVersions(version, MinimumVersion) < 0 {
			lastErr = fmt.Errorf("claude %s is older than the supported minimum %s", version, MinimumVersion)
			continue
		}
		return Runtime{Executable: candidate, Version: version}, nil
	}
	return Runtime{}, lastErr
}

// unwrapShim maps the npm claude.cmd wrapper to the packaged native
// executable so the daemon runs and tracks claude.exe itself.
func unwrapShim(path string) string {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path
	}
	return filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
}

func pathCandidates() []string {
	result := make([]string, 0, 2)
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			native := filepath.Join(directory, "claude.exe")
			if _, err := os.Stat(native); err == nil {
				result = append(result, native)
			}
			if _, err := os.Stat(filepath.Join(directory, "claude.cmd")); err == nil {
				result = append(result, filepath.Join(directory, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"))
			}
			continue
		}
		result = append(result, filepath.Join(directory, "claude"))
	}
	return result
}

func executableVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--version")
	cmd.Env = childEnvironment(os.Environ())
	configureChild(cmd)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w", executable, err)
	}
	match := versionPattern.FindString(string(output))
	if match == "" {
		return "", fmt.Errorf("unrecognized claude version output %q", strings.TrimSpace(string(output)))
	}
	return match, nil
}

func compareVersions(left, right string) int {
	parse := func(value string) [3]int {
		var result [3]int
		match := versionPattern.FindStringSubmatch(value)
		for index := 0; match != nil && index < 3; index++ {
			result[index], _ = strconv.Atoi(match[index+1])
		}
		return result
	}
	a, b := parse(left), parse(right)
	for index := range a {
		if a[index] != b[index] {
			if a[index] < b[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// childEnvironment removes variables a parent Claude Code host injects.
// Inheriting them makes the CLI expect host-provided authentication and fail
// with "Not logged in". The question tool is opted in explicitly.
func childEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "CLAUDE_CODE_") || strings.HasPrefix(upper, "CLAUDE_AGENT_SDK_") {
			continue
		}
		switch upper {
		case "CLAUDECODE", "CLAUDE_EFFORT", "CLAUDE_PID", "CLAUDE_PREVIEW_CLASSIFIER_FLOOR":
			continue
		}
		result = append(result, entry)
	}
	return append(result, "CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL=1")
}

// sessionArgs builds the headless command line for one conversation.
func sessionArgs(conversationID string, resume bool, settings provider.ConversationSettings) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--replay-user-messages",
		"--permission-prompts", "host", "--permission-prompt-tool", "stdio"}
	if resume {
		args = append(args, "--resume", conversationID)
	} else {
		args = append(args, "--session-id", conversationID)
	}
	mode := cliPermissionMode(settings)
	args = append(args, "--permission-mode", mode)
	if mode == "bypassPermissions" {
		args = append(args, "--allow-dangerously-skip-permissions")
	}
	if settings.ModelID != "" && settings.ModelID != defaultModelID {
		args = append(args, "--model", settings.ModelID)
	}
	if settings.ReasoningEffort != "" && settings.ReasoningEffort != defaultEffortID {
		args = append(args, "--effort", settings.ReasoningEffort)
	}
	return args
}

const (
	defaultModelID = "default"
	// defaultEffortID leaves the effort to the user's Claude Code
	// configuration. It must be the catalog default: Mobile replaces an
	// empty default with the first listed effort.
	defaultEffortID = "auto"
)

// Mobile permission presets mapped onto Claude Code permission modes. The
// collaboration mode "plan" selects Claude Code's plan mode regardless of
// the preset. readOnly has no faithful Claude Code equivalent.
func cliPermissionMode(settings provider.ConversationSettings) string {
	if settings.Mode == "plan" {
		return "plan"
	}
	switch settings.PermissionModeID {
	case "autoReview":
		return "auto"
	case "fullAccess":
		return "bypassPermissions"
	default:
		return "default"
	}
}

func defaultSettings() provider.ConversationSettings {
	return provider.ConversationSettings{Mode: "default", ModelID: defaultModelID, ReasoningEffort: defaultEffortID, PermissionModeID: "default"}
}

func permissionModes() []provider.PermissionMode {
	return []provider.PermissionMode{
		{ID: "default", Available: true},
		{ID: "autoReview", Available: true},
		{ID: "readOnly", Available: false, Reason: "Claude Code has no read-only permission mode"},
		{ID: "fullAccess", Available: true, Dangerous: true},
	}
}

var reasoningEfforts = []provider.ReasoningEffort{{ID: defaultEffortID, Description: "Claude Code configured effort"}, {ID: "low"}, {ID: "medium"}, {ID: "high"}, {ID: "xhigh"}, {ID: "max"}}

// modelCatalog lists the CLI default and the model aliases. Aliases may be
// remapped by ANTHROPIC_DEFAULT_*_MODEL in the process environment or in the
// user's Claude Code settings; the mapped model is shown as the description.
func modelCatalog(configDir string) provider.ModelCatalog {
	mapped := modelOverrides(configDir)
	items := []provider.ModelCatalogItem{{ID: defaultModelID, Name: "Default", Description: "Claude Code configured model", Default: true, Selectable: true, DefaultReasoningEffort: defaultEffortID, ReasoningEfforts: reasoningEfforts}}
	for _, alias := range []struct{ id, name, env string }{{"opus", "Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL"}, {"sonnet", "Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"}, {"haiku", "Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}} {
		items = append(items, provider.ModelCatalogItem{ID: alias.id, Name: alias.name, Description: mapped[alias.env], Selectable: true, DefaultReasoningEffort: defaultEffortID, ReasoningEfforts: reasoningEfforts})
	}
	return provider.ModelCatalog{Items: items}
}

func modelOverrides(configDir string) map[string]string {
	result := make(map[string]string)
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err == nil && json.Unmarshal(data, &settings) == nil {
		for key, value := range settings.Env {
			if text, ok := value.(string); ok && strings.HasPrefix(key, "ANTHROPIC_DEFAULT_") {
				result[key] = text
			}
		}
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if value := os.Getenv(key); value != "" {
			result[key] = value
		}
	}
	return result
}

// Claude API image input limits; larger or other images fail the model call.
const maxImageBytes = 5 << 20

var supportedImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

func validImages(images []provider.Image) error {
	for _, image := range images {
		if !supportedImageTypes[strings.ToLower(image.MimeType)] {
			return provider.NewError("unsupported_attachment_type", "Claude Code accepts JPEG, PNG, GIF and WebP images")
		}
		if len(image.Data) > maxImageBytes {
			return provider.NewError("too_large", "Claude Code images must be at most 5 MiB")
		}
	}
	return nil
}

func validSettings(settings provider.ConversationSettings) error {
	switch settings.Mode {
	case "", "default", "plan":
	default:
		return provider.NewError("invalid_selection", "unknown collaboration mode "+settings.Mode)
	}
	switch settings.ModelID {
	case "", defaultModelID, "opus", "sonnet", "haiku":
	default:
		return provider.NewError("invalid_selection", "unknown model "+settings.ModelID)
	}
	if settings.ReasoningEffort != "" {
		found := false
		for _, effort := range reasoningEfforts {
			found = found || effort.ID == settings.ReasoningEffort
		}
		if !found {
			return provider.NewError("invalid_selection", "unknown reasoning effort "+settings.ReasoningEffort)
		}
	}
	for _, mode := range permissionModes() {
		if mode.ID == settings.PermissionModeID || settings.PermissionModeID == "" {
			if settings.PermissionModeID != "" && !mode.Available {
				return provider.NewError("selection_unavailable", mode.Reason)
			}
			return nil
		}
	}
	return provider.NewError("invalid_selection", "unknown permission mode "+settings.PermissionModeID)
}
