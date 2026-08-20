//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd)               { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func started(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
func stop(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
func forward(cmd *exec.Cmd, s os.Signal) {
	if x, ok := s.(syscall.Signal); ok && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, x)
	}
}
