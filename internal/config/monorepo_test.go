package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppsMapName(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "apps", "web", "src")
	if e := os.MkdirAll(app, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "devhostd.json"), []byte(`{"name":"acme","apps":{"apps/web":{"name":"store","script":"serve"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, _, e := Find(app)
	if e != nil {
		t.Fatal(e)
	}
	if c.Name != "store.acme" || c.Script != "serve" {
		t.Fatalf("unexpected config %#v", c)
	}
}
func TestWorkspaceDiscovery(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "packages", "api")
	if e := os.MkdirAll(app, 0700); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"@acme/platform","workspaces":["packages/*"]}`), 0600)
	_ = os.WriteFile(filepath.Join(app, "package.json"), []byte(`{"name":"@acme/api"}`), 0600)
	c, _, e := Find(app)
	if e != nil {
		t.Fatal(e)
	}
	if c.Name != "api.platform" {
		t.Fatalf("name=%q", c.Name)
	}
}
