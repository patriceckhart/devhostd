package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Project struct {
	Name    string             `json:"name"`
	Script  string             `json:"script"`
	AppPort int                `json:"appPort"`
	Apps    map[string]Project `json:"apps,omitempty"`
}

type packageFile struct {
	Name       string          `json:"name"`
	Devhostd   json.RawMessage `json:"devhostd"`
	Workspaces json.RawMessage `json:"workspaces"`
}

func Find(start string) (Project, string, error) {
	origin, e := filepath.Abs(start)
	if e != nil {
		return Project{}, "", e
	}
	dirs := ancestors(origin)

	for _, dir := range dirs {
		configPath := filepath.Join(dir, "devhostd.json")
		if b, er := os.ReadFile(configPath); er == nil {
			return projectConfig(b, configPath, dir, origin)
		}
	}
	for _, dir := range dirs {
		pkgPath := filepath.Join(dir, "package.json")
		if pkg, ok := readPackage(pkgPath); ok && len(pkg.Devhostd) > 0 {
			var c Project
			if pkg.Devhostd[0] == '"' {
				e = json.Unmarshal(pkg.Devhostd, &c.Name)
			} else {
				e = json.Unmarshal(pkg.Devhostd, &c)
			}
			return c, pkgPath, e
		}
	}
	for _, dir := range dirs {
		pkgPath := filepath.Join(dir, "package.json")
		if pkg, ok := readPackage(pkgPath); ok && len(pkg.Workspaces) > 0 && dir != origin {
			if child, ok := nearestPackage(origin, dir); ok {
				projectName, appName := slug(pkg.Name), slug(child.Name)
				if projectName == "" {
					projectName = slug(filepath.Base(dir))
				}
				if appName != "" {
					return Project{Name: appName + "." + projectName}, pkgPath, nil
				}
			}
		}
	}
	return Project{}, "", os.ErrNotExist
}

func ancestors(origin string) []string {
	var dirs []string
	for dir := origin; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if _, e := os.Stat(filepath.Join(dir, ".git")); e == nil || filepath.Dir(dir) == dir {
			return dirs
		}
	}
}

func projectConfig(b []byte, path, root, origin string) (Project, string, error) {
	var project Project
	if e := json.Unmarshal(b, &project); e != nil {
		return project, path, e
	}
	if len(project.Apps) == 0 {
		return project, path, nil
	}
	rel, _ := filepath.Rel(root, origin)
	rel = filepath.ToSlash(rel)
	best := ""
	var app Project
	for appPath, candidate := range project.Apps {
		appPath = strings.Trim(filepath.ToSlash(filepath.Clean(appPath)), "./")
		if (rel == appPath || strings.HasPrefix(rel, appPath+"/")) && len(appPath) > len(best) {
			best, app = appPath, candidate
		}
	}
	if best == "" {
		return project, path, nil
	}
	projectName := slug(project.Name)
	if projectName == "" {
		projectName = slug(filepath.Base(root))
	}
	appName := slug(app.Name)
	if appName == "" {
		if pkg, ok := readPackage(filepath.Join(root, filepath.FromSlash(best), "package.json")); ok {
			appName = slug(pkg.Name)
		}
	}
	if appName == "" {
		appName = slug(filepath.Base(best))
	}
	app.Name = appName + "." + projectName
	return app, path, nil
}

func readPackage(path string) (packageFile, bool) {
	b, e := os.ReadFile(path)
	if e != nil {
		return packageFile{}, false
	}
	var p packageFile
	if json.Unmarshal(b, &p) != nil {
		return packageFile{}, false
	}
	return p, true
}

func nearestPackage(start, stop string) (packageFile, bool) {
	for dir := start; ; dir = filepath.Dir(dir) {
		if p, ok := readPackage(filepath.Join(dir, "package.json")); ok {
			return p, true
		}
		if dir == stop || filepath.Dir(dir) == dir {
			return packageFile{}, false
		}
	}
}

func slug(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
