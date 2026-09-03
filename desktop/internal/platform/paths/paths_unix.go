//go:build !windows

package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ProductName), nil
}

func LocalEndpoint(userIdentity string) string {
	hash := sha256.Sum256([]byte(userIdentity))
	return filepath.Join(os.TempDir(), "work-anywhere-"+hex.EncodeToString(hash[:6])+"-v1.sock")
}

func CanonicalPath(value string) string {
	if value == "" {
		return ""
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		absolute = value
	}
	return filepath.Clean(absolute)
}
