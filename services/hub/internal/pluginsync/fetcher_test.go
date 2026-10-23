package pluginsync_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/pluginsync"
)

// hostOf strips the "http://" scheme from an httptest server URL: the fetcher
// takes an "ip:port" endpoint and prepends the scheme itself.
func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	host, ok := strings.CutPrefix(srv.URL, "http://")
	if !ok {
		t.Fatalf("unexpected server URL %q", srv.URL)
	}
	return host
}

func TestSkillFetcher_RejectsOversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64*1024+1))
	}))
	defer srv.Close()

	fetch := pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 5 * time.Second})
	_, err := fetch(context.Background(), hostOf(t, srv), "/skills/big.md")
	if err == nil {
		t.Fatal("expected an error for a body larger than 64 KiB")
	}
	if err.Error() != "skill body exceeds 64 KiB" {
		t.Errorf("error: %v", err)
	}
}

// TestSkillFetcher_AcceptsExactly64KiB pins the boundary: 64 KiB itself is
// within the cap, not over it.
func TestSkillFetcher_AcceptsExactly64KiB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64*1024))
	}))
	defer srv.Close()

	fetch := pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 5 * time.Second})
	body, err := fetch(context.Background(), hostOf(t, srv), "/skills/exact.md")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(body) != 64*1024 {
		t.Errorf("body length: %d, want %d", len(body), 64*1024)
	}
}

func TestSkillFetcher_GETsEndpointPathAndReturnsBody(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method: %s", r.Method)
		}
		_, _ = w.Write([]byte("# usage guide"))
	}))
	defer srv.Close()

	fetch := pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 5 * time.Second})
	body, err := fetch(context.Background(), hostOf(t, srv), "/skills/memory-usage.md")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if body != "# usage guide" {
		t.Errorf("body: %q", body)
	}
	if gotPath != "/skills/memory-usage.md" {
		t.Errorf("path: %q", gotPath)
	}
}

func TestSkillFetcher_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	fetch := pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 5 * time.Second})
	_, err := fetch(context.Background(), hostOf(t, srv), "/skills/missing.md")
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should name the status, got: %v", err)
	}
}
