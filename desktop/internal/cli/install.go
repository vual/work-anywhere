package cli

import "github.com/hurf/work-anywhere/desktop/internal/platform/installation"

func installBridge() error   { return installation.InstallBridge() }
func uninstallBridge() error { return installation.UninstallBridge() }
