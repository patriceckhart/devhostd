//go:build windows

package control

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func Listen(address string) (net.Listener, error) {
	return winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)", MessageMode: false, InputBufferSize: 65536, OutputBufferSize: 65536})
}
func dial(ctx context.Context, d net.Dialer, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
