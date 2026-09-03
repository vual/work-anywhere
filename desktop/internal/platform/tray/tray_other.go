//go:build !windows

package tray

func Start(Controller) (func(), error) { return func() {}, nil }
