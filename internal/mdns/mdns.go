package mdns

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/devhostd/devhostd/internal/routes"
	hmdns "github.com/hashicorp/mdns"
	"github.com/miekg/dns"
)

type Zone struct {
	Table *routes.Table
	mu    sync.RWMutex
	addrs []net.IP
}

func (z *Zone) Records(q dns.Question) []dns.RR {
	name := strings.TrimSuffix(strings.ToLower(q.Name), ".")
	if !strings.HasSuffix(name, ".local") {
		return nil
	}
	if _, ok := z.Table.Lookup(name); !ok {
		return nil
	}
	z.mu.RLock()
	defer z.mu.RUnlock()
	out := []dns.RR{}
	for _, ip := range z.addrs {
		if v := ip.To4(); v != nil && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY) {
			out = append(out, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: v})
		} else if ip.To4() == nil && (q.Qtype == dns.TypeAAAA || q.Qtype == dns.TypeANY) {
			out = append(out, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 120}, AAAA: ip})
		}
	}
	return out
}

type Manager struct{ servers []*hmdns.Server }

func Start(table *routes.Table) (*Manager, error) {
	m := &Manager{}
	ifaces, e := net.Interfaces()
	if e != nil {
		return nil, e
	}
	logger := log.New(os.Stderr, "mdns: ", log.LstdFlags)
	for i := range ifaces {
		iface := ifaces[i]
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != (net.FlagUp|net.FlagMulticast) || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		raw, _ := iface.Addrs()
		ips := []net.IP{}
		for _, a := range raw {
			var s string
			switch x := a.(type) {
			case *net.IPNet:
				s = x.IP.String()
			case *net.IPAddr:
				s = x.IP.String()
			}
			if ip := net.ParseIP(s); ip != nil {
				ips = append(ips, ip)
			}
		}
		if len(ips) == 0 {
			continue
		}
		zone := &Zone{Table: table, addrs: ips}
		srv, e := hmdns.NewServer(&hmdns.Config{Zone: zone, Iface: &iface, Logger: logger})
		if e != nil {
			logger.Printf("interface %s: %v", iface.Name, e)
			continue
		}
		m.servers = append(m.servers, srv)
	}
	if len(m.servers) == 0 {
		return nil, fmt.Errorf("no multicast-capable network interface available")
	}
	return m, nil
}
func (m *Manager) Close() {
	for _, s := range m.servers {
		_ = s.Shutdown()
	}
}
