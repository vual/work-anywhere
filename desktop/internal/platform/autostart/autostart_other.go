//go:build !windows

package autostart

import "errors"

var errUnsupported = errors.New("login start is currently supported only on Windows")

func Enable() error        { return errUnsupported }
func Disable() error       { return errUnsupported }
func Get() (Status, error) { return Status{}, nil }
