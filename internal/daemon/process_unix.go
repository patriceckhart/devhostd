//go:build !windows

package daemon

import (
	"os"
	"syscall"
	"time"
)

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, e := os.FindProcess(pid)
	return e == nil && p.Signal(syscall.Signal(0)) == nil
}
func takeover(pid int) error {
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	if e = p.Signal(syscall.SIGTERM); e != nil {
		return e
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		return p.Signal(syscall.SIGKILL)
	}
	return nil
}
