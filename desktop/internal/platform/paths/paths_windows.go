//go:build windows

package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func DataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, ProductName), nil
}

func LocalEndpoint(userIdentity string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(userIdentity)))
	return `\\.\pipe\work-anywhere-` + hex.EncodeToString(hash[:6]) + "-v1"
}

func CanonicalPath(value string) string {
	if value == "" {
		return ""
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		absolute = value
	}
	return strings.ToLower(filepath.Clean(absolute))
}
