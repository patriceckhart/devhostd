package mdns

import (
	"github.com/devhostd/devhostd/internal/routes"
	"github.com/miekg/dns"
	"net"
	"testing"
)

func TestZoneRecordsLocalRoutes(t *testing.T) {
	table := routes.New([]string{"localhost", "local"}, false)
	if e := table.Register(routes.Route{Name: "app", Port: 4000}, false, nil); e != nil {
		t.Fatal(e)
	}
	z := &Zone{Table: table, addrs: []net.IP{net.ParseIP("192.0.2.1")}}
	records := z.Records(dns.Question{Name: "app.local.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	if len(records) != 1 {
		t.Fatalf("records=%v", records)
	}
	if got := records[0].(*dns.A).A.String(); got != "192.0.2.1" {
		t.Fatalf("address=%s", got)
	}
	if records = z.Records(dns.Question{Name: "missing.local.", Qtype: dns.TypeA, Qclass: dns.ClassINET}); len(records) != 0 {
		t.Fatal("answered unknown route")
	}
}
