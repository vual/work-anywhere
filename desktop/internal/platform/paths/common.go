package paths

import (
	"os"
	"path/filepath"
)

const ProductName = "WorkAnywhere"

func RuntimeDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run"), nil
}

func LogDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".work-anywhere", "config.json"), nil
}
