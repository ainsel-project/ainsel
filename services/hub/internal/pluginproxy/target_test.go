package pluginproxy

import (
	"net/http"
	"testing"

	"github.com/ainsel-project/ainsel/shared/api/plugin"
)

func TestResolve_Mapping(t *testing.T) {
	m := &plugin.Manifest{
		UI: &plugin.UISpec{
			Entry: "/static/ui/index.js",
		},
		API: &plugin.APISpec{
			BasePath: "/api",
		},
		MCP: &plugin.MCPSpec{
			Path: "/mcp",
		},
	}

	cases := []struct {
		name        string
		surface     Surface
		escapedRest string
		wantPath    string
		wantErr     bool
	}{
		{"ui with rest", SurfaceUI, "assets/app-1.css", "/static/ui/assets/app-1.css", false},
		{"ui entry only", SurfaceUI, "index.js", "/static/ui/index.js", false},
		{"api with rest", SurfaceAPI, "memories/42", "/api/memories/42", false},
		{"api empty rest", SurfaceAPI, "", "/api", false},
		{"mcp with rest", SurfaceMCP, "test", "/mcp/test", false},
		{"mcp empty rest", SurfaceMCP, "", "/mcp", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(m, tc.surface, tc.escapedRest)
			if tc.wantErr && err == nil {
				t.Errorf("Resolve: expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Resolve: unexpected error: %v", err)
			}
			if !tc.wantErr && got != tc.wantPath {
				t.Errorf("Resolve = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestResolve_RejectsEncodedTraversal(t *testing.T) {
	m := &plugin.Manifest{
		UI: &plugin.UISpec{
			Entry: "/static/ui/index.js",
		},
	}

	cases := []struct {
		name        string
		escapedRest string
	}{
		{"encoded dot-dot slash", "%2e%2e/api/x"},
		{"encoded dot-dot", "..%2fapi"},
		{"plain dot-dot", ".."},
		{"escaped traversal complex", "a/../../api"},
		{"dot segment", "./index.js"},
		{"double slash", "a//b"},
		{"backslash encoded", "a%5cb"},
		{"backslash plain", "a\\b"},
		{"encoded slash", "%2F"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(m, SurfaceUI, tc.escapedRest)
			if err != ErrInvalidPath {
				t.Errorf("Resolve(%q): expected ErrInvalidPath, got %v", tc.escapedRest, err)
			}
		})
	}
}

func TestResolve_NoSurface(t *testing.T) {
	cases := []struct {
		name    string
		surface Surface
		m       *plugin.Manifest
	}{
		{"no api", SurfaceAPI, &plugin.Manifest{
			UI: &plugin.UISpec{Entry: "/ui"},
		}},
		{"no mcp", SurfaceMCP, &plugin.Manifest{
			UI: &plugin.UISpec{Entry: "/ui"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.m, tc.surface, "x")
			if err != ErrNoSurface {
				t.Errorf("Resolve: expected ErrNoSurface, got %v", err)
			}
		})
	}
}

func TestPickEndpoint(t *testing.T) {
	// Empty endpoints
	_, ok := PickEndpoint([]string{}, nil)
	if ok {
		t.Errorf("PickEndpoint with empty: expected ok=false, got true")
	}

	// With rnd returning 1 (second entry)
	rnd := func(n int) int { return 1 }
	ep, ok := PickEndpoint([]string{"first", "second", "third"}, rnd)
	if !ok {
		t.Errorf("PickEndpoint: expected ok=true, got false")
	}
	if ep != "second" {
		t.Errorf("PickEndpoint = %q, want %q", ep, "second")
	}
}

func TestSanitizeRequestHeaders(t *testing.T) {
	h := http.Header{
		"Authorization":       []string{"Bearer token"},
		"Cookie":              []string{"session=abc"},
		"Proxy-Authorization": []string{"Basic xyz"},
		"X-Ainsel-Credential": []string{"ainselp_test"},
		"X-Ainsel-Other":      []string{"value"},
		"X-Forwarded-For":     []string{"1.2.3.4"},
		"X-Forwarded-Host":    []string{"example.com"},
		"X-Real-Ip":           []string{"5.6.7.8"},
		"Forwarded":           []string{"for=1.2.3.4"},
		"Content-Type":        []string{"application/json"},
		"Accept":              []string{"application/json"},
	}

	SanitizeRequestHeaders(h)

	// Check removed headers
	if h.Get("Authorization") != "" {
		t.Errorf("Authorization should be removed")
	}
	if h.Get("Cookie") != "" {
		t.Errorf("Cookie should be removed")
	}
	if h.Get("Proxy-Authorization") != "" {
		t.Errorf("Proxy-Authorization should be removed")
	}
	if h.Get("X-Ainsel-Credential") != "" {
		t.Errorf("X-Ainsel-Credential should be removed")
	}
	if h.Get("X-Ainsel-Other") != "" {
		t.Errorf("X-Ainsel-Other should be removed")
	}
	if h.Get("X-Forwarded-For") != "" {
		t.Errorf("X-Forwarded-For should be removed")
	}
	if h.Get("X-Forwarded-Host") != "" {
		t.Errorf("X-Forwarded-Host should be removed")
	}
	if h.Get("X-Real-Ip") != "" {
		t.Errorf("X-Real-Ip should be removed")
	}
	if h.Get("Forwarded") != "" {
		t.Errorf("Forwarded should be removed")
	}

	// Check kept headers
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type should be kept")
	}
	if h.Get("Accept") != "application/json" {
		t.Errorf("Accept should be kept")
	}
}
