//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func elevated() bool { return os.Geteuid() == 0 }
func elevate(args []string) error {
	if os.Getenv("CI") == "1" {
		return fmt.Errorf("ports below 1024 require root; choose --port 1024 or higher")
	}
	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("ports below 1024 require root in non-interactive mode; choose a high port")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", append([]string{exe}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
