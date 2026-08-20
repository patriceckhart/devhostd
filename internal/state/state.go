package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

type Layout struct{ Root string }

type DaemonConfig struct {
	Port     int      `json:"port"`
	TLS      bool     `json:"tls"`
	TLDs     []string `json:"tlds"`
	Wildcard bool     `json:"wildcard"`
	LAN      bool     `json:"lan"`
	CertFile string   `json:"cert,omitempty"`
	KeyFile  string   `json:"key,omitempty"`
}

type DaemonInfo struct {
	Version   string   `json:"version"`
	PID       int      `json:"pid"`
	Port      int      `json:"port"`
	TLS       bool     `json:"tls"`
	LAN       bool     `json:"lan"`
	TLDs      []string `json:"tlds"`
	Socket    string   `json:"socket"`
	StartedAt string   `json:"started_at"`
}

func Default() (Layout, error) {
	if p := os.Getenv("DEVHOSTD_STATE_DIR"); p != "" {
		return New(p)
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	return New(filepath.Join(h, ".devhostd"))
}
func New(root string) (Layout, error) { p, err := filepath.Abs(root); return Layout{Root: p}, err }
func (l Layout) CA() string           { return filepath.Join(l.Root, "ca") }
func (l Layout) Certs() string        { return filepath.Join(l.Root, "certs") }
func (l Layout) Routes() string       { return filepath.Join(l.Root, "routes.json") }
func (l Layout) Info() string         { return filepath.Join(l.Root, "daemon.json") }
func (l Layout) Config() string       { return filepath.Join(l.Root, "config.json") }
func (l Layout) Socket() string {
	if runtime.GOOS == "windows" {
		name := os.Getenv("USERNAME")
		if name == "" {
			name = "user"
		}
		name = filepath.Base(name)
		return `\\.\pipe\devhostd-` + name
	}
	return filepath.Join(l.Root, "daemon.sock")
}
func (l Layout) Ensure() error {
	if err := os.MkdirAll(l.CA(), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(l.Certs(), 0700); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(l.Root, "logs"), 0700)
}
func ReadJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func WriteJSON(path string, v any, mode os.FileMode) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	return AtomicWrite(path, b, mode)
}
func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, path)
}
func LoadInfo(l Layout) (DaemonInfo, error) {
	var i DaemonInfo
	e := ReadJSON(l.Info(), &i)
	return i, e
}
func Remove(l Layout) error {
	if l.Root == "" || l.Root == "/" {
		return errors.New("refusing unsafe state path")
	}
	return os.RemoveAll(l.Root)
}
