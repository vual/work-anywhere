//go:build !windows

package localipc

import (
	"context"
	"errors"
	"net"
)

func ListenPipe(_, _ string) (net.Listener, error) {
	return nil, errors.New("named pipes require Windows")
}
func DialPipe(_ context.Context, _ string) (net.Conn, error) {
	return nil, errors.New("named pipes require Windows")
}
