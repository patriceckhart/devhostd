package hosts

import (
	"github.com/devhostd/devhostd/internal/routes"
	"strings"
	"testing"
)

func TestReplaceIsIdempotent(t *testing.T) {
	input := "127.0.0.1 localhost\n"
	rs := []routes.Route{{Hostnames: []string{"app.localhost"}}}
	first := Replace(input, rs)
	second := Replace(first, rs)
	if first != second {
		t.Fatalf("managed block is not idempotent:\n%s", second)
	}
	if strings.Count(first, begin) != 1 {
		t.Fatal("expected one managed block")
	}
	clean := Replace(first, nil)
	if strings.Contains(clean, begin) {
		t.Fatal("managed block remained after clean")
	}
}
