//go:build !windows

package installation

import "errors"

func InstallBridge() error { return errors.New("installation is currently supported only on Windows") }
func UninstallBridge() error {
	return errors.New("uninstallation is currently supported only on Windows")
}

func Status() (BridgeStatus, error) { return BridgeStatus{}, nil }
