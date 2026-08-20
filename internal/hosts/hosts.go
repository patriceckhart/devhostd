package hosts

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/devhostd/devhostd/internal/routes"
	"github.com/devhostd/devhostd/internal/state"
)

const begin = "# devhostd begin"
const end = "# devhostd end"

func Path() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("SystemRoot") + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}
func Block(rs []routes.Route) string {
	set := map[string]bool{}
	for _, r := range rs {
		for _, h := range r.Hostnames {
			set[h] = true
		}
	}
	names := make([]string, 0, len(set))
	for h := range set {
		names = append(names, h)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(begin + "\n")
	for _, h := range names {
		fmt.Fprintf(&b, "127.0.0.1 %s\n::1 %s\n", h, h)
	}
	b.WriteString(end + "\n")
	return b.String()
}
func Replace(content string, rs []routes.Route) string {
	start := strings.Index(content, begin)
	if start >= 0 {
		stop := strings.Index(content[start:], end)
		if stop >= 0 {
			stop = start + stop + len(end)
			for stop < len(content) && (content[stop] == '\r' || content[stop] == '\n') {
				stop++
			}
			content = content[:start] + content[stop:]
		}
	}
	content = strings.TrimRight(content, "\r\n") + "\n"
	if len(rs) > 0 {
		content += "\n" + Block(rs)
	}
	return content
}
func Sync(path string, rs []routes.Route) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	return state.AtomicWrite(path, []byte(Replace(string(b), rs)), info.Mode())
}
