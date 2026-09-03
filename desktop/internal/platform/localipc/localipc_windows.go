//go:build windows

package localipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func ListenPipe(name, sid string) (net.Listener, error) {
	config := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	}
	return winio.ListenPipe(name, config)
}

func DialPipe(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, name)
}
