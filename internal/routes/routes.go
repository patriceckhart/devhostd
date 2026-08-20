package routes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Route struct {
	Name      string    `json:"name"`
	Hostnames []string  `json:"hostnames"`
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	Static    bool      `json:"static"`
	CreatedAt time.Time `json:"created_at"`
}

type Table struct {
	mu       sync.RWMutex
	byName   map[string]Route
	tlds     []string
	wildcard bool
}

func New(tlds []string, wildcard bool) *Table {
	if len(tlds) == 0 {
		tlds = []string{"localhost"}
	}
	return &Table{byName: make(map[string]Route), tlds: tlds, wildcard: wildcard}
}

func ValidateName(name string) error {
	if name == "" || len(name) > 253 {
		return errors.New("name must contain between 1 and 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid DNS label %q", label)
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return fmt.Errorf("invalid character %q in name", r)
			}
		}
	}
	return nil
}

func (t *Table) Expand(name string) []string {
	h := make([]string, 0, len(t.tlds))
	for _, tld := range t.tlds {
		h = append(h, name+"."+strings.TrimSuffix(tld, "."))
	}
	return h
}

func (t *Table) Register(r Route, force bool, alive func(int) bool) error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.byName[r.Name]; ok && !force && (old.Static || old.PID == 0 || alive == nil || alive(old.PID)) {
		return fmt.Errorf("route %q is already registered", r.Name)
	}
	if len(r.Hostnames) == 0 {
		r.Hostnames = t.Expand(r.Name)
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	t.byName[r.Name] = r
	return nil
}

func (t *Table) AddHostname(name, hostname string) error {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	if hostname == "" {
		return errors.New("hostname is empty")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.byName[name]
	if !ok {
		return fmt.Errorf("route %q not found", name)
	}
	for _, h := range r.Hostnames {
		if h == hostname {
			return nil
		}
	}
	r.Hostnames = append(r.Hostnames, hostname)
	t.byName[name] = r
	return nil
}
func (t *Table) RemoveHostname(name, hostname string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.byName[name]
	if !ok {
		return fmt.Errorf("route %q not found", name)
	}
	out := r.Hostnames[:0]
	for _, h := range r.Hostnames {
		if h != hostname {
			out = append(out, h)
		}
	}
	r.Hostnames = out
	t.byName[name] = r
	return nil
}
func (t *Table) Remove(name string, pid int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.byName[name]
	if !ok || (pid != 0 && r.PID != pid) {
		return false
	}
	delete(t.byName, name)
	return true
}

func (t *Table) Lookup(host string) (Route, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.Split(host, ":")[0], "."))
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, r := range t.byName {
		for _, h := range r.Hostnames {
			if host == h {
				return r, true
			}
		}
	}
	if t.wildcard {
		var best Route
		n := 0
		for _, r := range t.byName {
			for _, h := range r.Hostnames {
				if strings.HasSuffix(host, "."+h) && len(h) > n {
					best, n = r, len(h)
				}
			}
		}
		if n > 0 {
			return best, true
		}
	}
	return Route{}, false
}

func (t *Table) List() []Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Route, 0, len(t.byName))
	for _, r := range t.byName {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (t *Table) Replace(rs []Route) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byName = make(map[string]Route, len(rs))
	for _, r := range rs {
		t.byName[r.Name] = r
	}
}
func (t *Table) TLDs() []string { return append([]string(nil), t.tlds...) }
