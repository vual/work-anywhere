package paths

import "path/filepath"

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
