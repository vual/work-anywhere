//go:build windows

package identity

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

func CurrentUser() (string, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func InstanceMutexName(instanceID string) string {
	return fmt.Sprintf(`Local\WorkAnywhere.Instance.%s`, instanceID)
}

func DaemonMutexName(sid string) string {
	return fmt.Sprintf(`Local\WorkAnywhere.Daemon.%s`, sid)
}

type Lease struct {
	handle windows.Handle
	once   sync.Once
}

func AcquireLease(name string) (*Lease, bool, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		return nil, false, err
	}
	alreadyExists := windows.GetLastError() == windows.ERROR_ALREADY_EXISTS
	return &Lease{handle: handle}, alreadyExists, nil
}

func (l *Lease) Close() error {
	var err error
	l.once.Do(func() { err = windows.CloseHandle(l.handle) })
	return err
}
