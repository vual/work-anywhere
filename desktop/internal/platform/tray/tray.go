package tray

import "sync"

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
	// CheckForUpdates runs an update check; the result arrives via Notify.
	CheckForUpdates()
	Shutdown(interrupt bool)
}

// Notice is a balloon notification; clicking it opens the management page.
type Notice struct {
	Title   string
	Message string
}

var (
	noticeMu   sync.Mutex
	noticeNext *Notice
	noticePost func()
)

// Notify shows a balloon notification on the running tray icon. It is a
// no-op when no tray icon is running.
func Notify(notice Notice) {
	noticeMu.Lock()
	noticeNext = &notice
	post := noticePost
	noticeMu.Unlock()
	if post != nil {
		post()
	}
}

func takeNotice() *Notice {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	notice := noticeNext
	noticeNext = nil
	return notice
}

func setNoticePoster(post func()) {
	noticeMu.Lock()
	noticePost = post
	noticeMu.Unlock()
}
