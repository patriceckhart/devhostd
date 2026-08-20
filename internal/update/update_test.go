package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.0.1", "0.0.2", true},
		{"0.0.99", "0.1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.99", false},
		{"dev", "0.1.0", false},
	}
	for _, tc := range cases {
		if got := versionLess(tc.current, tc.latest); got != tc.want {
			t.Fatalf("versionLess(%q, %q)=%t, want %t", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestCheckCachesRelease(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(release{TagName: "v0.1.0", HTMLURL: "https://example.test/v0.1.0"})
	}))
	defer server.Close()
	old := latestReleaseURL
	latestReleaseURL = server.URL
	defer func() { latestReleaseURL = old }()

	stateDir := t.TempDir()
	for i := 0; i < 2; i++ {
		info, err := Check(context.Background(), stateDir, "0.0.99", false)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Available || info.Latest != "0.1.0" {
			t.Fatalf("unexpected info: %#v", info)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
}

func TestCheckCachesNetworkFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	old := latestReleaseURL
	latestReleaseURL = server.URL
	defer func() { latestReleaseURL = old }()
	stateDir := t.TempDir()
	if _, err := Check(context.Background(), stateDir, "0.0.1", false); err == nil {
		t.Fatal("expected first check to fail")
	}
	if info, err := Check(context.Background(), stateDir, "0.0.1", false); err != nil || info.Available {
		t.Fatalf("cached check info=%#v err=%v", info, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
}

func TestDownloadAssetUsesAuthenticatedAPI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Accept") != "application/octet-stream" {
			t.Fatalf("missing authenticated asset headers")
		}
		_, _ = w.Write([]byte("asset"))
	}))
	defer server.Close()
	data, err := downloadAsset(context.Background(), release{Assets: []asset{{Name: "file", URL: server.URL}}}, "file")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "asset" {
		t.Fatalf("data=%q", data)
	}
}

func TestExtractTarGzipBinary(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	payload := []byte("binary data")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "devhostd", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	got, err := extractBinary(archive.Bytes(), "tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload=%q", got)
	}
}

func TestDevelopmentBuildSkipsCheck(t *testing.T) {
	old := latestReleaseURL
	latestReleaseURL = "http://127.0.0.1:1"
	defer func() { latestReleaseURL = old }()
	info, err := Check(context.Background(), t.TempDir(), "dev", false)
	if err != nil || info.Available {
		t.Fatalf("info=%#v err=%v", info, err)
	}
}
