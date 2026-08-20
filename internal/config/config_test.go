package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevhostdJSONTakesPrecedence(t *testing.T) {
	d := t.TempDir()
	if e := os.WriteFile(filepath.Join(d, "devhostd.json"), []byte(`{"name":"file"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d, "package.json"), []byte(`{"devhostd":{"name":"package","appPort":4100}}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, path, e := Find(d)
	if e != nil {
		t.Fatal(e)
	}
	if c.Name != "file" || filepath.Base(path) != "devhostd.json" {
		t.Fatalf("unexpected config: %#v %s", c, path)
	}
}
