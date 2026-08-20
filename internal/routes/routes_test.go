package routes

import "testing"

func TestLookupExactAndWildcard(t *testing.T) {
	table := New([]string{"localhost", "test"}, true)
	if err := table.Register(Route{Name: "api.myapp", Port: 4123}, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.myapp.localhost", "api.myapp.test", "tenant.api.myapp.localhost:443"} {
		r, ok := table.Lookup(host)
		if !ok || r.Port != 4123 {
			t.Fatalf("lookup %s = %#v, %v", host, r, ok)
		}
	}
}
func TestValidateName(t *testing.T) {
	for _, name := range []string{"-bad", "bad_thing", "UPPER", "a..b"} {
		if ValidateName(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"app", "api.my-app", "a1"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
}
