//go:build windows

package process

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Group owns one independent job per managed root. Reusing a populated job
// can fail when a new root inherits a different host job hierarchy.
type Group struct {
	mu      sync.Mutex
	closed  bool
	members map[*jobMember]struct{}
}

type jobMember struct {
	job       windows.Handle
	process   windows.Handle
	closeOnce sync.Once
}

func NewGroup() (*Group, error) {
	return &Group{members: make(map[*jobMember]struct{})}, nil
}

func (j *Group) Add(cmd *exec.Cmd) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("attach process: group is closed")
	}
	if cmd == nil || cmd.Process == nil {
		return errors.New("attach process: process has not started")
	}
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return jobError("create job", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(handle)
		return jobError("configure job", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(handle)
		return jobError("open process for job", err)
	}
	if err = windows.AssignProcessToJobObject(handle, process); err != nil {
		windows.CloseHandle(process)
		windows.CloseHandle(handle)
		return jobError("assign process to job", err)
	}
	member := &jobMember{job: handle, process: process}
	j.members[member] = struct{}{}
	go func() {
		// Close kills the job and wakes this waiter. The process wait handle
		// stays valid until the wait finishes, even if Group.Close races it.
		_, _ = windows.WaitForSingleObject(member.process, windows.INFINITE)
		j.mu.Lock()
		delete(j.members, member)
		_ = member.closeJob()
		j.mu.Unlock()
		windows.CloseHandle(member.process)
	}()
	return nil
}

func jobError(stage string, err error) error {
	var code syscall.Errno
	_ = errors.As(err, &code)
	return &JobError{Stage: stage, Code: uint32(code), Err: err}
}

func (m *jobMember) closeJob() (err error) {
	m.closeOnce.Do(func() { err = windows.CloseHandle(m.job) })
	return err
}

func (j *Group) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	var errs []error
	for member := range j.members {
		errs = append(errs, member.closeJob())
	}
	return errors.Join(errs...)
}

func ConfigureManagedChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
