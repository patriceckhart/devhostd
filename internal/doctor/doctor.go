package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/devhostd/devhostd/internal/control"
	"github.com/devhostd/devhostd/internal/hosts"
	"github.com/devhostd/devhostd/internal/routes"
	"github.com/devhostd/devhostd/internal/state"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func Run(l state.Layout, cliVersion string) []Check {
	out := []Check{}
	if i, e := os.Stat(l.Root); e != nil {
		out = append(out, Check{"state", "fail", e.Error()})
	} else if i.Mode().Perm()&0077 != 0 {
		out = append(out, Check{"state", "warn", "state directory permissions are too broad"})
	} else {
		out = append(out, Check{"state", "pass", l.Root})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var info state.DaemonInfo
	if e := control.Call(ctx, l.Socket(), "status", nil, &info); e != nil {
		out = append(out, Check{"daemon", "fail", e.Error()})
		return out
	}
	out = append(out, Check{"daemon", "pass", fmt.Sprintf("pid %d, port %d", info.PID, info.Port)})
	if info.Version != cliVersion {
		out = append(out, Check{"version", "fail", fmt.Sprintf("CLI %s differs from daemon %s; restart the daemon", cliVersion, info.Version)})
	} else {
		out = append(out, Check{"version", "pass", cliVersion})
	}
	if info.TLS {
		if _, e := os.Stat(l.CA() + "/rootCA.pem"); e != nil {
			out = append(out, Check{"ca", "fail", e.Error()})
		} else {
			out = append(out, Check{"ca", "pass", "root CA is present"})
		}
		if _, e := os.Stat(l.CA() + "/trusted"); e != nil {
			out = append(out, Check{"trust", "warn", "CA trust has not been confirmed"})
		} else {
			out = append(out, Check{"trust", "pass", "CA trust installation was completed"})
		}
	}
	for _, tld := range info.TLDs {
		if tld == "local" || tld == "dev" {
			out = append(out, Check{"tld", "warn", tld + " can conflict with mDNS or browser HSTS"})
		}
	}
	var rs []routes.Route
	if control.Call(ctx, l.Socket(), "list", nil, &rs) == nil {
		for _, r := range rs {
			c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", r.Port), 200*time.Millisecond)
			if e != nil {
				out = append(out, Check{"route " + r.Name, "warn", "upstream is not accepting connections"})
			} else {
				c.Close()
				out = append(out, Check{"route " + r.Name, "pass", fmt.Sprintf("port %d", r.Port)})
			}
		}
		if len(rs) > 0 && len(rs[0].Hostnames) > 0 {
			if addrs, e := net.LookupHost(rs[0].Hostnames[0]); e != nil {
				out = append(out, Check{"dns", "warn", e.Error()})
			} else {
				out = append(out, Check{"dns", "pass", strings.Join(addrs, ", ")})
			}
		}
		if b, e := os.ReadFile(hosts.Path()); e == nil {
			expected := hosts.Block(rs)
			if len(rs) > 0 && !strings.Contains(string(b), expected) {
				out = append(out, Check{"hosts", "warn", "managed block differs from route table"})
			} else {
				out = append(out, Check{"hosts", "pass", "managed block is consistent"})
			}
		}
	}
	return out
}
