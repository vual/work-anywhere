//go:build !windows

package secrets

import "errors"

func ProtectForCurrentUser([]byte) ([]byte, error) {
	return nil, errors.New("secure key storage is not implemented on this platform")
}
func UnprotectForCurrentUser([]byte) ([]byte, error) {
	return nil, errors.New("secure key storage is not implemented on this platform")
}
