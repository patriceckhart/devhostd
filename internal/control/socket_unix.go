//go:build !windows

package control

import (
	"context"
	"net"
	"os"
)

func Listen(address string) (net.Listener, error) {
	_ = os.Remove(address)
	ln, e := net.Listen("unix", address)
	if e == nil {
		e = os.Chmod(address, 0600)
	}
	if e != nil && ln != nil {
		ln.Close()
	}
	return ln, e
}
func dial(ctx context.Context, d net.Dialer, address string) (net.Conn, error) {
	return d.DialContext(ctx, "unix", address)
}
