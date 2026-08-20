//go:build windows

package daemon

import (
	"golang.org/x/sys/windows"
	"os"
)

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	status, e := windows.WaitForSingleObject(h, 0)
	return e == nil && status == uint32(windows.WAIT_TIMEOUT)
}
func takeover(pid int) error {
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	return p.Kill()
}
