//go:build !windows

package process

import (
	"errors"
	"os/exec"
)

type Group struct{}

func NewGroup() (*Group, error) {
	return nil, errors.New("managed child groups are not implemented on this platform")
}
func (*Group) Add(*exec.Cmd) error {
	return errors.New("managed child groups are not implemented on this platform")
}
func (*Group) Close() error           { return nil }
func ConfigureManagedChild(*exec.Cmd) {}
