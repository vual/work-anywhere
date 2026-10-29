package claudecode

import (
	"errors"
	"fmt"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"os/exec"
	"strings"
	"sync"
)

const maxProcessDiagnostic = 64 << 10

// ProcessDiagnostic contains metadata safe for normal structured logs. Stderr
// stays in memory and is only attached to a synchronous failure response.
type ProcessDiagnostic struct {
	Stage          string
	PID            int
	RuntimeVersion string
	ExitCode       *int
	Win32Code      *uint32
}

type diagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

func (d *diagnosticTail) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	if d.data == nil {
		d.data = make([]byte, 0, maxProcessDiagnostic)
	}
	if n >= maxProcessDiagnostic {
		d.data = append(d.data[:0], p[n-maxProcessDiagnostic:]...)
	} else {
		if excess := len(d.data) + n - maxProcessDiagnostic; excess > 0 {
			copy(d.data, d.data[excess:])
			d.data = d.data[:len(d.data)-excess]
		}
		d.data = append(d.data, p...)
	}
	return n, nil
}

func (d *diagnosticTail) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(string(d.data))
}

func (b *Backend) processDiagnostic(stage string, cmd *exec.Cmd, cause error) ProcessDiagnostic {
	d := ProcessDiagnostic{Stage: stage, RuntimeVersion: b.runtime.Version}
	if cmd.Process != nil {
		d.PID = cmd.Process.Pid
	}
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		d.ExitCode = &code
	}
	var jobErr *platformprocess.JobError
	if errors.As(cause, &jobErr) {
		d.Stage = jobErr.Stage
		code := jobErr.Code
		d.Win32Code = &code
	}
	if b.onProcessDiagnostic != nil {
		b.onProcessDiagnostic(d)
	}
	return d
}

func (b *Backend) processFailure(stage string, cmd *exec.Cmd, cause error, tail *diagnosticTail) error {
	d := b.processDiagnostic(stage, cmd, cause)
	exit, stderr := "", ""
	if d.ExitCode != nil {
		exit = fmt.Sprintf(" (exitCode=%d)", *d.ExitCode)
	}
	if value := tail.String(); value != "" {
		stderr = ": " + value
	}
	err := fmt.Errorf("%s Claude Code process%s: %w%s", stage, exit, cause, stderr)
	b.lastProcessError = err.Error()
	return err
}
