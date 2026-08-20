package service

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Status struct {
	Installed  bool   `json:"installed"`
	Manager    string `json:"manager"`
	Definition string `json:"definition"`
}

func Install(exe, stateDir string, daemonArgs []string) error {
	args := append([]string{"daemon", "start", "--foreground", "--state-dir", stateDir}, daemonArgs...)
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(exe, args)
	case "linux":
		return installSystemd(exe, args)
	case "windows":
		return run(exec.Command("schtasks", "/Create", "/F", "/SC", "ONSTART", "/RU", "SYSTEM", "/TN", "devhostd", "/TR", quoteCommand(exe, args)))
	default:
		return errors.New("startup services are unsupported on this platform")
	}
}
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		_ = run(exec.Command("sudo", "launchctl", "bootout", "system/dev.devhostd.daemon"))
		return run(exec.Command("sudo", "rm", "-f", "/Library/LaunchDaemons/dev.devhostd.daemon.plist"))
	case "linux":
		_ = run(exec.Command("sudo", "systemctl", "disable", "--now", "devhostd.service"))
		return run(exec.Command("sudo", "rm", "-f", "/etc/systemd/system/devhostd.service"))
	case "windows":
		return run(exec.Command("schtasks", "/Delete", "/F", "/TN", "devhostd"))
	default:
		return errors.New("startup services are unsupported on this platform")
	}
}
func GetStatus() Status {
	var path, manager string
	switch runtime.GOOS {
	case "darwin":
		path, manager = "/Library/LaunchDaemons/dev.devhostd.daemon.plist", "launchd"
	case "linux":
		path, manager = "/etc/systemd/system/devhostd.service", "systemd"
	case "windows":
		e := exec.Command("schtasks", "/Query", "/TN", "devhostd").Run()
		return Status{e == nil, "Task Scheduler", "devhostd"}
	default:
		return Status{Manager: "unsupported"}
	}
	_, e := os.Stat(path)
	return Status{e == nil, manager, path}
}
func installLaunchd(exe string, args []string) error {
	var a strings.Builder
	for _, v := range append([]string{exe}, args...) {
		fmt.Fprintf(&a, "<string>%s</string>", html.EscapeString(v))
	}
	content := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>dev.devhostd.daemon</string><key>ProgramArguments</key><array>` + a.String() + `</array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>`
	path := "/Library/LaunchDaemons/dev.devhostd.daemon.plist"
	return installFile([]byte(content), path, exec.Command("sudo", "launchctl", "bootstrap", "system", path))
}
func installSystemd(exe string, args []string) error {
	content := fmt.Sprintf("[Unit]\nDescription=devhostd local development proxy\nAfter=network.target\n\n[Service]\nType=simple\nExecStart=%s\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n", quoteCommand(exe, args))
	return installFile([]byte(content), "/etc/systemd/system/devhostd.service", exec.Command("sudo", "systemctl", "enable", "--now", "devhostd.service"))
}
func installFile(content []byte, dest string, start *exec.Cmd) error {
	f, e := os.CreateTemp("", "devhostd-service-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(content); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = run(exec.Command("sudo", "install", "-m", "0644", name, dest)); e != nil {
		return e
	}
	return run(start)
}
func quoteCommand(exe string, args []string) string {
	all := append([]string{exe}, args...)
	for i, s := range all {
		if strings.ContainsAny(s, " \t\"") {
			all[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
	}
	return strings.Join(all, " ")
}
func run(c *exec.Cmd) error {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
