package process

import "fmt"

// JobError preserves the failing Windows operation and native error code.
type JobError struct {
	Stage string
	Code  uint32
	Err   error
}

func (e *JobError) Error() string { return fmt.Sprintf("%s (win32=%d): %v", e.Stage, e.Code, e.Err) }
func (e *JobError) Unwrap() error { return e.Err }
