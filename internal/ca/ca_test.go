package ca

import "testing"

func TestGenerateAndMint(t *testing.T) {
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := a.Certificate("app.localhost", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.Certificate) < 2 {
		t.Fatalf("certificate chain has %d entries", len(cert.Certificate))
	}
	if _, err = Open(a.dir); err != nil {
		t.Fatalf("reload: %v", err)
	}
}
