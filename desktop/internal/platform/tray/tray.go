package tray

type InstanceStatus struct {
	ID             string
	Name           string
	State          string
	RuntimeVersion string
	ActiveTurn     bool
}

type Controller interface {
	ManagementURL() string
	ControlPaused() bool
	ToggleControlPaused()
	TrayInstances() []InstanceStatus
	RestartErroredInstances() int
	Shutdown(interrupt bool)
}
