//go:build windows

package secrets

import (
	"fmt"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	size uint32
	data *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	cryptProtectData   = crypt32.NewProc("CryptProtectData")
	cryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	localFree          = kernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

func ProtectForCurrentUser(data []byte) ([]byte, error)   { return callDPAPI(cryptProtectData, data) }
func UnprotectForCurrentUser(data []byte) ([]byte, error) { return callDPAPI(cryptUnprotectData, data) }

func callDPAPI(proc *syscall.LazyProc, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, nil
	}
	in := dataBlob{size: uint32(len(input)), data: &input[0]}
	var out dataBlob
	result, _, callErr := proc.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if result == 0 {
		return nil, fmt.Errorf("DPAPI failed: %w", callErr)
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.data)))
	copyOfOutput := make([]byte, out.size)
	copy(copyOfOutput, unsafe.Slice(out.data, out.size))
	return copyOfOutput, nil
}
