//go:build windows

package processinfo

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ParentChatGPTExecutable() (string, bool) {
	path, _, ok := parentChatGPTProcess()
	return path, ok
}

func parentChatGPTProcess() (string, uint32, bool) {
	pid := uint32(os.Getpid())
	for range 8 {
		parent, ok := parentPID(pid)
		if !ok || parent == 0 {
			return "", 0, false
		}
		path, ok := processImage(parent)
		if ok && strings.EqualFold(filepath.Base(path), "ChatGPT.exe") {
			return path, parent, true
		}
		pid = parent
	}
	return "", 0, false
}

func parentPID(pid uint32) (uint32, bool) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err = windows.Process32First(snapshot, &entry); err != nil {
		return 0, false
	}
	for {
		if entry.ProcessID == pid {
			return entry.ParentProcessID, true
		}
		if err = windows.Process32Next(snapshot, &entry); err != nil {
			return 0, false
		}
	}
}

func processImage(pid uint32) (string, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, windows.MAX_PATH*4)
	size := uint32(len(buffer))
	if err = windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buffer[:size]), true
}

func ParentChatGPTUserData() (string, bool) {
	_, pid, ok := parentChatGPTProcess()
	if !ok {
		return "", false
	}
	commandLine, ok := processCommandLine(pid)
	if !ok {
		return "", false
	}
	args, ok := commandLineArgs(commandLine)
	if !ok {
		return "", false
	}
	for index, arg := range args {
		if strings.EqualFold(arg, "--user-data-dir") && index+1 < len(args) {
			return expandPath(args[index+1]), true
		}
		if key, value, found := strings.Cut(arg, "="); found && strings.EqualFold(key, "--user-data-dir") {
			return expandPath(value), true
		}
	}
	return "", false
}

func expandPath(value string) string {
	value = os.ExpandEnv(strings.TrimSpace(strings.Trim(value, `"`)))
	if strings.HasPrefix(value, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(value, "~"), `\`))
		}
	}
	if absolute, err := filepath.Abs(value); err == nil {
		return absolute
	}
	return value
}

type processBasicInformation struct {
	Reserved1                    uintptr
	PEBBaseAddress               uintptr
	Reserved2                    [2]uintptr
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr
}

type remoteUnicodeString struct {
	Length, MaximumLength uint16
	_                     uint32
	Buffer                uintptr
}

var (
	ntdllProcess              = syscall.NewLazyDLL("ntdll.dll")
	kernel32Process           = syscall.NewLazyDLL("kernel32.dll")
	ntQueryInformationProcess = ntdllProcess.NewProc("NtQueryInformationProcess")
	readProcessMemory         = kernel32Process.NewProc("ReadProcessMemory")
)

func processCommandLine(pid uint32) (string, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(handle)
	var basic processBasicInformation
	status, _, _ := ntQueryInformationProcess.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&basic)), unsafe.Sizeof(basic), 0)
	if status != 0 || basic.PEBBaseAddress == 0 {
		return "", false
	}
	var parameters uintptr
	if !readRemote(handle, basic.PEBBaseAddress+0x20, unsafe.Pointer(&parameters), unsafe.Sizeof(parameters)) || parameters == 0 {
		return "", false
	}
	var command remoteUnicodeString
	if !readRemote(handle, parameters+0x70, unsafe.Pointer(&command), unsafe.Sizeof(command)) || command.Buffer == 0 || command.Length == 0 {
		return "", false
	}
	buffer := make([]uint16, int(command.Length)/2)
	if !readRemote(handle, command.Buffer, unsafe.Pointer(&buffer[0]), uintptr(command.Length)) {
		return "", false
	}
	return windows.UTF16ToString(buffer), true
}

func readRemote(handle windows.Handle, address uintptr, target unsafe.Pointer, size uintptr) bool {
	var read uintptr
	result, _, _ := readProcessMemory.Call(uintptr(handle), address, uintptr(target), size, uintptr(unsafe.Pointer(&read)))
	return result != 0 && read == size
}

func commandLineArgs(commandLine string) ([]string, bool) {
	input, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, false
	}
	var count int32
	argv, err := windows.CommandLineToArgv(input, &count)
	if err != nil || count <= 0 {
		return nil, false
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(argv))))
	args := make([]string, 0, count)
	for index := 0; index < int(count); index++ {
		args = append(args, windows.UTF16PtrToString(&argv[index][0]))
	}
	return args, true
}
