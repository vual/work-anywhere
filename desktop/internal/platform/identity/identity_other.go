//go:build !windows

package identity

import (
	"fmt"
	"os/user"
)

func CurrentUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Uid, nil
}

func InstanceMutexName(instanceID string) string { return fmt.Sprintf("work-anywhere-%s", instanceID) }
func DaemonMutexName(sid string) string          { return fmt.Sprintf("work-anywhere-daemon-%s", sid) }

type Lease struct{}

func AcquireLease(string) (*Lease, bool, error) { return nil, false, fmt.Errorf("Windows only") }
func (*Lease) Close() error                     { return nil }
