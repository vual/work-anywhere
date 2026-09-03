//go:build windows

package tray

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmApp         = 0x8000
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	tpmReturnCmd  = 0x0100
	mfString      = 0x0000
	mfSeparator   = 0x0800
	mbYesNo       = 0x00000004
	mbIconWarning = 0x00000030
	idYes         = 6
	cmdOpen       = 1001
	cmdStatus     = 1002
	cmdPause      = 1003
	cmdRestart    = 1004
	cmdExit       = 1005
)

type wndClassEx struct {
	Size, Style            uint32
	WndProc                uintptr
	ClsExtra, WndExtra     int32
	Instance, Icon, Cursor uintptr
	Background             uintptr
	MenuName, ClassName    *uint16
	IconSmall              uintptr
}
type point struct{ X, Y int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type notifyIconData struct {
	Size                       uint32
	Window                     uintptr
	ID, Flags, CallbackMessage uint32
	Icon                       uintptr
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	Version                    uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	Guid                       [16]byte
	BalloonIcon                uintptr
}

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	kernel              = syscall.NewLazyDLL("kernel32.dll")
	registerClassEx     = user32.NewProc("RegisterClassExW")
	createWindowEx      = user32.NewProc("CreateWindowExW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	postMessage         = user32.NewProc("PostMessageW")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	getMessage          = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	loadIcon            = user32.NewProc("LoadIconW")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	messageBox          = user32.NewProc("MessageBoxW")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle     = kernel.NewProc("GetModuleHandleW")
)

func Start(manager Controller) (func(), error) {
	type result struct {
		hwnd uintptr
		err  error
	}
	ready := make(chan result, 1)
	go trayLoop(manager, func(hwnd uintptr, err error) { ready <- result{hwnd: hwnd, err: err} })
	started := <-ready
	if started.err != nil {
		return nil, started.err
	}
	return func() { postMessage.Call(started.hwnd, wmClose, 0, 0) }, nil
}

func trayLoop(manager Controller, ready func(uintptr, error)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className, _ := syscall.UTF16PtrFromString("WorkAnywhereTrayWindow")
	instance, _, _ := getModuleHandle.Call(0)
	icon, _, _ := loadIcon.Call(0, 32512)
	wndProc := syscall.NewCallback(func(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case wmApp + 1:
			event := uint32(lParam & 0xffff)
			if event == wmRButtonUp || event == wmContextMenu {
				showTrayMenu(manager, hwnd)
			}
			return 0
		case wmClose:
			destroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			postQuitMessage.Call(0)
			return 0
		}
		result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	})
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: wndProc, Instance: instance, Icon: icon, IconSmall: icon, ClassName: className}
	if atom, _, callErr := registerClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		ready(0, fmt.Errorf("RegisterClassExW: %w", callErr))
		return
	}
	hwnd, _, callErr := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		ready(0, fmt.Errorf("CreateWindowExW: %w", callErr))
		return
	}
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmApp + 1, Icon: icon}
	copy(data.Tip[:], syscall.StringToUTF16("Work Anywhere"))
	if ok, _, callErr := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); ok == 0 {
		destroyWindow.Call(hwnd)
		ready(0, fmt.Errorf("Shell_NotifyIconW: %w", callErr))
		return
	}
	ready(hwnd, nil)
	var message winMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
	shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func showTrayMenu(manager Controller, hwnd uintptr) {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	openText, _ := syscall.UTF16PtrFromString("打开管理页")
	statusText, _ := syscall.UTF16PtrFromString("实例状态")
	pauseLabel := "暂停本机控制"
	if manager.ControlPaused() {
		pauseLabel = "恢复本机控制"
	}
	pauseText, _ := syscall.UTF16PtrFromString(pauseLabel)
	restartText, _ := syscall.UTF16PtrFromString("重启异常实例")
	exitText, _ := syscall.UTF16PtrFromString("退出")
	appendMenu.Call(menu, mfString, cmdOpen, uintptr(unsafe.Pointer(openText)))
	appendMenu.Call(menu, mfString, cmdStatus, uintptr(unsafe.Pointer(statusText)))
	appendMenu.Call(menu, mfString, cmdPause, uintptr(unsafe.Pointer(pauseText)))
	appendMenu.Call(menu, mfString, cmdRestart, uintptr(unsafe.Pointer(restartText)))
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenu.Call(menu, mfString, cmdExit, uintptr(unsafe.Pointer(exitText)))
	var cursor point
	getCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	setForegroundWindow.Call(hwnd)
	command, _, _ := trackPopupMenu.Call(menu, tpmReturnCmd, uintptr(cursor.X), uintptr(cursor.Y), 0, hwnd, 0)
	switch command {
	case cmdOpen:
		openURLNative(manager.ManagementURL())
	case cmdStatus:
		items := manager.TrayInstances()
		textValue := fmt.Sprintf("Daemon 正在运行\n实例数量：%d", len(items))
		for _, item := range items {
			id := item.ID
			if len(id) > 12 {
				id = id[:12]
			}
			textValue += fmt.Sprintf("\n\n%s\n状态：%s\nCodex：%s", firstNonempty(item.Name, id), item.State, item.RuntimeVersion)
		}
		text, _ := syscall.UTF16PtrFromString(textValue)
		title, _ := syscall.UTF16PtrFromString("Work Anywhere 状态")
		messageBox.Call(hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40)
	case cmdPause:
		manager.ToggleControlPaused()
	case cmdRestart:
		count := manager.RestartErroredInstances()
		text, _ := syscall.UTF16PtrFromString(fmt.Sprintf("已请求重启 %d 个异常实例。", count))
		title, _ := syscall.UTF16PtrFromString("Work Anywhere")
		messageBox.Call(hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40)
	case cmdExit:
		active := 0
		for _, item := range manager.TrayInstances() {
			if item.ActiveTurn {
				active++
			}
		}
		if active > 0 {
			text, _ := syscall.UTF16PtrFromString(fmt.Sprintf("有 %d 个任务正在运行。是否中断任务并退出？", active))
			title, _ := syscall.UTF16PtrFromString("Work Anywhere")
			answer, _, _ := messageBox.Call(hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbYesNo|mbIconWarning)
			if answer != idYes {
				return
			}
		}
		manager.Shutdown(active > 0)
	}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func openURLNative(url string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	target, _ := syscall.UTF16PtrFromString(url)
	shellExecute := shell32.NewProc("ShellExecuteW")
	shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
}
