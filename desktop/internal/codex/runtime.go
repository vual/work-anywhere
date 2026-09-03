package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/platform/identity"
	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	"github.com/hurf/work-anywhere/desktop/internal/platform/processinfo"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func ResolveRuntime(_ []string) (protocol.RuntimeDescriptor, error) {
	candidates := runtimeCandidates()
	self, _ := os.Executable()
	self = paths.CanonicalPath(self)
	for _, candidate := range candidates {
		candidate.Path = expandPath(candidate.Path)
		info, err := os.Stat(candidate.Path)
		if err != nil || info.IsDir() || paths.CanonicalPath(candidate.Path) == self {
			continue
		}
		digest, _ := fileSHA256(candidate.Path)
		return protocol.RuntimeDescriptor{
			RealExecutable: candidate.Path,
			Version:        executableVersion(candidate.Path),
			SHA256:         digest,
			ResourcesDir:   resourcesDirectory(candidate.Path),
			DiscoveredBy:   candidate.Source,
			DiscoveredAt:   time.Now().UTC(),
		}, nil
	}
	return protocol.RuntimeDescriptor{}, errors.New("official codex.exe was not found; set WORK_ANYWHERE_CODEX_EXE")
}

type runtimeCandidate struct{ Path, Source string }

func runtimeCandidates() []runtimeCandidate {
	var result []runtimeCandidate
	if parent, ok := processinfo.ParentChatGPTExecutable(); ok {
		result = append(result, runtimeCandidate{filepath.Join(filepath.Dir(parent), "resources", "codex.exe"), "parent_chatgpt_resources"})
	}
	for _, item := range []struct{ key, source string }{
		{"WORK_ANYWHERE_CODEX_EXE", "WORK_ANYWHERE_CODEX_EXE"},
		{"CODEX_REAL_EXE", "CODEX_REAL_EXE"},
	} {
		if value := os.Getenv(item.key); value != "" {
			result = append(result, runtimeCandidate{value, item.source})
		}
	}
	if self, err := os.Executable(); err == nil {
		result = append(result, runtimeCandidate{filepath.Join(filepath.Dir(self), "codex-real.exe"), "sibling_codex_real"})
	}
	result = append(result, scanPortableCodex()...)
	return deduplicateCandidates(result)
}

func scanPortableCodex() []runtimeCandidate {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return nil
	}
	patterns := []string{
		filepath.Join(local, "Programs", "CodexPortable", "*", "app", "resources", "codex.exe"),
		filepath.Join(local, "Programs", "ChatGPT", "*", "resources", "codex.exe"),
	}
	var found []runtimeCandidate
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			found = append(found, runtimeCandidate{match, "portable_scan"})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Path > found[j].Path })
	return found
}

func ResolveInstance() (protocol.InstanceDescriptor, error) {
	sid, err := identity.CurrentUser()
	if err != nil {
		return protocol.InstanceDescriptor{}, err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			codexHome = filepath.Join(home, ".codex")
		}
	}
	userData := discoverUserData(codexHome)
	name := os.Getenv("WORK_ANYWHERE_INSTANCE_NAME")
	canonicalHome := paths.CanonicalPath(codexHome)
	canonicalUserData := paths.CanonicalPath(userData)
	sum := sha256.Sum256([]byte(strings.Join([]string{sid, canonicalHome, canonicalUserData, name}, "\x00")))
	return protocol.InstanceDescriptor{
		ID: hex.EncodeToString(sum[:]), CodexHome: canonicalHome,
		UserDataDir: canonicalUserData, Name: name, WindowsSID: sid,
	}, nil
}

func discoverUserData(codexHome string) string {
	for _, key := range []string{"WORK_ANYWHERE_USER_DATA_DIR", "CODEX_ELECTRON_USER_DATA_PATH", "ELECTRON_USER_DATA_DIR"} {
		if value := os.Getenv(key); value != "" {
			return expandPath(value)
		}
	}
	if value, ok := processinfo.ParentChatGPTUserData(); ok {
		return value
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "OpenAI", "Codex")
	}
	return filepath.Join(codexHome, "electron")
}

func expandPath(value string) string {
	value = os.ExpandEnv(strings.TrimSpace(strings.Trim(value, `"`)))
	if strings.HasPrefix(value, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(value, "~"), `\`))
		}
	}
	absolute, err := filepath.Abs(value)
	if err == nil {
		return absolute
	}
	return value
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func executableVersion(path string) string {
	cmd := exec.Command(path, "--version")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func resourcesDirectory(executable string) string {
	dir := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(dir), "resources") {
		return dir
	}
	return ""
}

func deduplicateCandidates(values []runtimeCandidate) []runtimeCandidate {
	seen := make(map[string]bool)
	result := make([]runtimeCandidate, 0, len(values))
	for _, value := range values {
		key := paths.CanonicalPath(value.Path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func DescribeRuntime(runtime protocol.RuntimeDescriptor) string {
	return fmt.Sprintf("%s (%s)", runtime.RealExecutable, runtime.Version)
}
