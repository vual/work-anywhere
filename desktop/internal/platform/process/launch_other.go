//go:build !windows && !darwin && !linux

package process

import (
	"errors"
	"os/exec"
)

func ConfigureDetached(*exec.Cmd)   {}
func ConfigureStandalone(*exec.Cmd) {}
func OpenURL(string) error          { return errors.New("opening URLs is not implemented on this platform") }
