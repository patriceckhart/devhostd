package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const cacheTTL = 12 * time.Hour

var latestReleaseURL = "https://api.github.com/repos/patriceckhart/devhostd/releases/latest"

type Info struct {
	Current   string
	Latest    string
	URL       string
	Available bool
}

type asset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []asset `json:"assets"`
}

type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
}

func Check(ctx context.Context, stateDir, current string, force bool) (Info, error) {
	current = cleanVersion(current)
	if isDevelopment(current) {
		return Info{}, nil
	}
	path := filepath.Join(stateDir, "update-check.json")
	if !force {
		if cached, ok := readCache(path); ok && cached.Current == current && time.Since(cached.CheckedAt) < cacheTTL {
			return makeInfo(current, cached.Latest, cached.URL), nil
		}
	}
	rel, err := fetchRelease(ctx)
	if err != nil {
		_ = writeCache(path, cache{CheckedAt: time.Now().UTC(), Current: current})
		return Info{}, err
	}
	latest := cleanVersion(rel.TagName)
	_ = writeCache(path, cache{CheckedAt: time.Now().UTC(), Current: current, Latest: latest, URL: rel.HTMLURL})
	return makeInfo(current, latest, rel.HTMLURL), nil
}

func Notify(stateDir, current string, out io.Writer) {
	if os.Getenv("DEVHOSTD_UPDATE_CHECK") == "0" || isDevelopment(cleanVersion(current)) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := Check(ctx, stateDir, current, false)
	if err == nil && info.Available {
		fmt.Fprintf(out, "devhostd %s is available (current: %s). Run `devhostd update`.\n", info.Latest, info.Current)
	}
}

func Install(ctx context.Context, current string, out io.Writer) error {
	current = cleanVersion(current)
	if isDevelopment(current) {
		return errors.New("updates are disabled for development builds")
	}
	fmt.Fprintln(out, "devhostd update: checking the latest release")
	rel, err := fetchRelease(ctx)
	if err != nil {
		return fmt.Errorf("query latest release: %w", err)
	}
	latest := cleanVersion(rel.TagName)
	if !versionLess(current, latest) {
		fmt.Fprintf(out, "devhostd %s is already up to date.\n", current)
		return nil
	}
	archiveName, format, err := releaseAssetName(latest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "devhostd update: %s -> %s\n", current, latest)

	checksums, err := downloadAsset(ctx, rel, "checksums.txt")
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	archive, err := downloadAsset(ctx, rel, archiveName)
	if err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}
	want, err := checksumFor(checksums, archiveName)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	binary, err := extractBinary(archive, format)
	if err != nil {
		return err
	}
	if err := replaceExecutable(binary); err != nil {
		return err
	}
	fmt.Fprintf(out, "devhostd update: installed %s\n", latest)
	return nil
}

func makeInfo(current, latest, url string) Info {
	return Info{Current: current, Latest: latest, URL: url, Available: versionLess(current, latest)}
}

func cleanVersion(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if fields := strings.Fields(v); len(fields) > 0 {
		v = fields[0]
	}
	return v
}

func isDevelopment(v string) bool { return v == "" || v == "dev" || v == "0.0.0" }

func versionLess(a, b string) bool {
	a, b = "v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")
	return semver.IsValid(a) && semver.IsValid(b) && semver.Compare(a, b) < 0
}

func fetchRelease(ctx context.Context) (release, error) {
	var rel release
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return rel, err
	}
	setGitHubHeaders(req, false)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return rel, err
	}
	if rel.TagName == "" {
		return rel, errors.New("latest release has no tag")
	}
	return rel, nil
}

func downloadAsset(ctx context.Context, rel release, name string) ([]byte, error) {
	var found *asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			found = &rel.Assets[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("release does not contain %s", name)
	}
	url := found.BrowserDownloadURL
	api := false
	if os.Getenv("GITHUB_TOKEN") != "" && found.URL != "" {
		url, api = found.URL, true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, api)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func setGitHubHeaders(req *http.Request, assetAPI bool) {
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if assetAPI {
		req.Header.Set("Accept", "application/octet-stream")
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func releaseAssetName(version string) (string, string, error) {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", "", fmt.Errorf("updates are unsupported on %s/%s", runtime.GOOS, arch)
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		return fmt.Sprintf("devhostd_%s_%s_%s.tar.gz", version, runtime.GOOS, arch), "tar.gz", nil
	case "windows":
		if arch != "amd64" {
			return "", "", fmt.Errorf("updates are unsupported on windows/%s", arch)
		}
		return fmt.Sprintf("devhostd_%s_windows_amd64.zip", version), "zip", nil
	default:
		return "", "", fmt.Errorf("updates are unsupported on %s", runtime.GOOS)
	}
}

func checksumFor(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", name)
}

func extractBinary(data []byte, format string) ([]byte, error) {
	switch format {
	case "tar.gz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if filepath.Base(h.Name) == "devhostd" && h.Typeflag == tar.TypeReg {
				return io.ReadAll(tr)
			}
		}
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) != "devhostd.exe" {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(r)
			r.Close()
			return b, err
		}
	default:
		return nil, fmt.Errorf("unsupported archive format %s", format)
	}
	return nil, errors.New("release archive does not contain devhostd")
}

func replaceExecutable(binary []byte) error {
	current, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	info, err := os.Stat(current)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(current), ".devhostd-update-*")
	if err != nil {
		return fmt.Errorf("create update beside %s: %w", current, err)
	}
	newPath := file.Name()
	defer os.Remove(newPath)
	if err = file.Chmod(info.Mode()); err == nil {
		_, err = file.Write(binary)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	backup := current + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(current, backup); err != nil {
		return fmt.Errorf("replace %s: %w", current, err)
	}
	if err := os.Rename(newPath, current); err != nil {
		_ = os.Rename(backup, current)
		return fmt.Errorf("install update: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

func readCache(path string) (cache, bool) {
	var c cache
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	return c, true
}

func writeCache(path string, c cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
