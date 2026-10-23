package plugin

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// validManifest returns a complete, valid manifest as a generic map so tests
// can mutate one field at a time and re-marshal it.
func validManifest() map[string]any {
	return map[string]any{
		"id":          "mem0",
		"displayName": "Mem0",
		"version":     "0.4.2",
		"description": "Long-term memory for agents",
		"pluginApi":   "1.0",
		"ui": map[string]any{
			"entry": "/plugins/mem0/index.js",
			"pages": []any{
				map[string]any{
					"path":    "/memories",
					"label":   "Memories",
					"section": "plugins",
					"icon":    "brain",
				},
			},
			"slots": map[string]any{
				"invocation.panel": "Mem0InvocationPanel",
			},
		},
		"api": map[string]any{
			"basePath": "/plugins/mem0",
		},
		"mcp": map[string]any{
			"path": "/mcp",
		},
		"permissions": []string{"invocations:read", "agents:read"},
	}
}

func marshalManifest(t *testing.T, m map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return data
}

func TestConstants(t *testing.T) {
	if HostAPIVersion != "1.1" {
		t.Errorf("HostAPIVersion = %q, want %q", HostAPIVersion, "1.1")
	}
	if ManifestPath != "/ainsel-plugin.json" {
		t.Errorf("ManifestPath = %q, want %q", ManifestPath, "/ainsel-plugin.json")
	}
	if MaxManifestBytes != 256<<10 {
		t.Errorf("MaxManifestBytes = %d, want %d", MaxManifestBytes, 256<<10)
	}
	if len(Permissions) != 16 {
		t.Errorf("len(Permissions) = %d, want 16", len(Permissions))
	}
	if len(Slots) != 4 {
		t.Errorf("len(Slots) = %d, want 4", len(Slots))
	}
}

// TestPermissionsVocabulary pins the closed permission vocabulary: the exact
// entries and their order (grouped by resource, read before write within each
// resource). Pinning the full list keeps a widening of the vocabulary a
// deliberate change instead of a silent one.
func TestPermissionsVocabulary(t *testing.T) {
	want := []string{
		"agents:read",
		"agents:write",
		"agent-images:read",
		"agent-images:write",
		"connectors:read",
		"connectors:write",
		"invocations:read",
		"mcp-servers:read",
		"mcp-servers:write",
		"observability:read",
		"personas:read",
		"personas:write",
		"skills:read",
		"skills:write",
		"triggers:read",
		"triggers:write",
	}
	if !slices.Equal(Permissions, want) {
		t.Errorf("Permissions = %v, want %v", Permissions, want)
	}

	// The newly widened entries must be declareable: a manifest asking for
	// them parses without error.
	m, err := Parse(marshalManifest(t, func() map[string]any {
		m := validManifest()
		m["permissions"] = []string{"connectors:write", "triggers:read", "mcp-servers:write", "agent-images:write"}
		return m
	}()))
	if err != nil {
		t.Fatalf("Parse with widened permissions: unexpected error: %v", err)
	}
	if len(m.Permissions) != 4 {
		t.Errorf("Permissions = %v, want 4 entries", m.Permissions)
	}

	// The set stays closed: a permission outside the vocabulary — including
	// one that looks like a valid resource with an invented verb — is still
	// rejected.
	_, err = Parse(marshalManifest(t, func() map[string]any {
		m := validManifest()
		m["permissions"] = []string{"connectors:admin"}
		return m
	}()))
	if err == nil {
		t.Fatalf("Parse with permission %q: expected error, got nil", "connectors:admin")
	}
	if !strings.Contains(err.Error(), "connectors:admin") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "connectors:admin")
	}
}

func TestParseValidManifest(t *testing.T) {
	m, err := Parse(marshalManifest(t, validManifest()))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if m.ID != "mem0" {
		t.Errorf("ID = %q, want %q", m.ID, "mem0")
	}
	if m.DisplayName != "Mem0" {
		t.Errorf("DisplayName = %q, want %q", m.DisplayName, "Mem0")
	}
	if m.PluginAPI != "1.0" {
		t.Errorf("PluginAPI = %q, want %q", m.PluginAPI, "1.0")
	}
	if m.UI == nil || m.UI.Entry != "/plugins/mem0/index.js" {
		t.Errorf("UI.Entry = %+v, want %q", m.UI, "/plugins/mem0/index.js")
	}
	if m.UI == nil || len(m.UI.Pages) != 1 || m.UI.Pages[0].Path != "/memories" {
		t.Errorf("UI.Pages = %+v, want one page with path /memories", m.UI)
	}
	if m.UI == nil || m.UI.Slots["invocation.panel"] != "Mem0InvocationPanel" {
		t.Errorf("UI.Slots = %+v, want invocation.panel -> Mem0InvocationPanel", m.UI.Slots)
	}
	if m.API == nil || m.API.BasePath != "/plugins/mem0" {
		t.Errorf("API.BasePath = %+v, want %q", m.API, "/plugins/mem0")
	}
	if m.MCP == nil || m.MCP.Path != "/mcp" {
		t.Errorf("MCP.Path = %+v, want %q", m.MCP, "/mcp")
	}
	if len(m.Permissions) != 2 {
		t.Errorf("Permissions = %v, want 2 entries", m.Permissions)
	}
}

func TestParseMinimalManifest(t *testing.T) {
	// Only the required fields: ui / api / mcp / permissions / description all absent.
	data := marshalManifest(t, map[string]any{
		"id":          "mem0",
		"displayName": "Mem0",
		"version":     "0.4.2",
		"pluginApi":   "1.0",
	})
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if m.UI != nil || m.API != nil || m.MCP != nil {
		t.Errorf("optional specs should be nil: ui=%+v api=%+v mcp=%+v", m.UI, m.API, m.MCP)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want []string // substrings that must all appear in the error
	}{
		{
			name: "oversized",
			data: []byte(strings.Repeat("a", MaxManifestBytes+1)),
			want: []string{"too large"},
		},
		{
			name: "not json",
			data: []byte("not json"),
			want: []string{"invalid JSON"},
		},
		{
			name: "unknown top-level field",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["bogus"] = true
				return m
			}()),
			want: []string{"unknown field"},
		},
		{
			name: "unknown nested field",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["ui"] = map[string]any{
					"entry": "/plugins/mem0/index.js",
					"bogus": true,
				}
				return m
			}()),
			want: []string{"unknown field"},
		},
		{
			name: "uppercase id",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["id"] = "Mem0"
				return m
			}()),
			want: []string{"id"},
		},
		{
			name: "too short id",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["id"] = "m"
				return m
			}()),
			want: []string{"id"},
		},
		{
			// A trailing '-' is a usable URL slug but not a valid Kubernetes
			// object name, and the id doubles as the Plugin name — the hub
			// rejects it too, so both sides of the check must agree.
			name: "id with trailing dash",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["id"] = "a-"
				return m
			}()),
			want: []string{"invalid id"},
		},
		{
			name: "id with underscore",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["id"] = "mem_0"
				return m
			}()),
			want: []string{"id"},
		},
		{
			name: "unknown permission",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["permissions"] = []string{"admin:all"}
				return m
			}()),
			want: []string{"admin:all"},
		},
		{
			name: "unknown slot",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["ui"] = map[string]any{
					"entry": "/plugins/mem0/index.js",
					"slots": map[string]any{"sidebar.top": "Thing"},
				}
				return m
			}()),
			want: []string{"sidebar.top"},
		},
		{
			name: "entry escapes with ..",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["ui"] = map[string]any{"entry": "../x.js"}
				return m
			}()),
			want: []string{"entry"},
		},
		{
			name: "entry not absolute",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["ui"] = map[string]any{"entry": "plugins/mem0/index.js"}
				return m
			}()),
			want: []string{"entry"},
		},
		{
			name: "api base path not absolute",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["api"] = map[string]any{"basePath": "plugins/mem0"}
				return m
			}()),
			want: []string{"basePath"},
		},
		{
			name: "mcp path escapes",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["mcp"] = map[string]any{"path": "/../mcp"}
				return m
			}()),
			want: []string{"path"},
		},
		{
			name: "duplicate page paths",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["ui"] = map[string]any{
					"entry": "/plugins/mem0/index.js",
					"pages": []any{
						map[string]any{"path": "/memories", "label": "Memories"},
						map[string]any{"path": "/memories", "label": "Recall"},
					},
				}
				return m
			}()),
			want: []string{"/memories"},
		},
		{
			name: "missing display name",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				delete(m, "displayName")
				return m
			}()),
			want: []string{"displayName"},
		},
		{
			name: "missing version",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				delete(m, "version")
				return m
			}()),
			want: []string{"version"},
		},
		{
			name: "pluginApi not MAJOR.MINOR",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["pluginApi"] = "1"
				return m
			}()),
			want: []string{"pluginApi"},
		},
		{
			name: "two errors at once",
			data: marshalManifest(t, func() map[string]any {
				m := validManifest()
				m["id"] = "Mem0"
				m["permissions"] = []string{"admin:all"}
				return m
			}()),
			want: []string{"id", "admin:all"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.data)
			if err == nil {
				t.Fatalf("Parse: expected error, got nil")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestParseSizeBoundary(t *testing.T) {
	// Exactly at the cap is accepted (padding with insignificant whitespace).
	base := marshalManifest(t, map[string]any{
		"id":          "mem0",
		"displayName": "Mem0",
		"version":     "1.0.0",
		"pluginApi":   "1.0",
	})
	if len(base) >= MaxManifestBytes {
		t.Fatalf("test manifest is already %d bytes, want < %d", len(base), MaxManifestBytes)
	}
	padded := append([]byte{}, base...)
	padded = append(padded, []byte(strings.Repeat(" ", MaxManifestBytes-len(base)))...)
	if _, err := Parse(padded); err != nil {
		t.Errorf("Parse at exactly MaxManifestBytes (%d bytes): unexpected error: %v", len(padded), err)
	}
	if _, err := Parse(append(padded, ' ')); err == nil {
		t.Errorf("Parse at MaxManifestBytes+1: expected error, got nil")
	}
}

func TestValidate(t *testing.T) {
	valid := marshalManifest(t, validManifest())
	var m Manifest
	if err := json.Unmarshal(valid, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("Validate on valid manifest: unexpected error: %v", err)
	}
}

// TestValidateSlotErrorOrderIsDeterministic guards the reconciler's condition
// message: Validate's error is stored verbatim on the Plugin status, so its
// text must not flap between reconciles. Slot errors in particular come from
// a map and must be emitted in a stable (sorted) order.
func TestValidateSlotErrorOrderIsDeterministic(t *testing.T) {
	data := marshalManifest(t, func() map[string]any {
		m := validManifest()
		m["ui"] = map[string]any{
			"entry": "/plugins/mem0/index.js",
			"slots": map[string]any{
				"zzz.unknown": "A",
				"aaa.unknown": "B",
			},
		}
		return m
	}())

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	first := m.Validate()
	if first == nil {
		t.Fatalf("Validate: expected error, got nil")
	}
	want := first.Error()
	if !strings.Contains(want, "aaa.unknown") || !strings.Contains(want, "zzz.unknown") {
		t.Fatalf("Validate error = %q, want both unknown slots reported", want)
	}
	if strings.Index(want, "aaa.unknown") > strings.Index(want, "zzz.unknown") {
		t.Errorf("Validate error not sorted: %q", want)
	}
	for i := 0; i < 50; i++ {
		if got := m.Validate().Error(); got != want {
			t.Fatalf("Validate call %d differs:\n got: %q\nwant: %q", i+2, got, want)
		}
	}
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		pluginAPI string
		host      string
		want      bool
		wantErr   bool
	}{
		{pluginAPI: "1.0", host: "1.0", want: true},
		{pluginAPI: "1.0", host: "1.2", want: true},
		{pluginAPI: "1.3", host: "1.2", want: false},
		{pluginAPI: "2.0", host: "1.9", want: false},
		{pluginAPI: "1", host: "1.0", wantErr: true},
		{pluginAPI: "1.0", host: "1", wantErr: true},
		{pluginAPI: "", host: "1.0", wantErr: true},
		{pluginAPI: "1.x", host: "1.0", wantErr: true},
		{pluginAPI: "1.0.0", host: "1.0", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_vs_%s", tc.pluginAPI, tc.host), func(t *testing.T) {
			got, err := Compatible(tc.pluginAPI, tc.host)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Compatible(%q, %q): expected error, got nil", tc.pluginAPI, tc.host)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compatible(%q, %q): unexpected error: %v", tc.pluginAPI, tc.host, err)
			}
			if got != tc.want {
				t.Errorf("Compatible(%q, %q) = %v, want %v", tc.pluginAPI, tc.host, got, tc.want)
			}
		})
	}
}

func TestMissingApprovals(t *testing.T) {
	got := MissingApprovals([]string{"b", "a", "c"}, []string{"a"})
	want := []string{"b", "c"}
	if len(got) != len(want) {
		t.Fatalf("MissingApprovals = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MissingApprovals[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if got := MissingApprovals([]string{"agents:read", "skills:read"}, []string{"skills:read", "agents:read"}); len(got) != 0 {
		t.Errorf("MissingApprovals with equal sets = %v, want empty", got)
	}

	if got := MissingApprovals(nil, []string{"agents:read"}); len(got) != 0 {
		t.Errorf("MissingApprovals with nil declared = %v, want empty", got)
	}

	if got := MissingApprovals([]string{"b", "b", "a"}, nil); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("MissingApprovals dedupes and sorts: got %v, want [a b]", got)
	}

	if got := MissingApprovals([]string{"agents:read"}, nil); len(got) != 1 || got[0] != "agents:read" {
		t.Errorf("MissingApprovals = %v, want [agents:read]", got)
	}
}

func TestValidate_PathCharacters(t *testing.T) {
	cases := []struct {
		name      string
		field     string
		path      string
		shouldErr bool
	}{
		// ui.entry cases
		{"ui.entry encoded traversal", "ui.entry", "/ui/%2e%2e/x.js", true},
		{"ui.entry backslash", "ui.entry", "/ui/a\\b.js", true},
		{"ui.entry dot segment", "ui.entry", "/ui/./x.js", true},
		{"ui.entry empty segment", "ui.entry", "/ui//x.js", true},
		{"ui.entry query string", "ui.entry", "/ui/x.js?v=1", true},
		{"ui.entry fragment", "ui.entry", "/ui/x.js#a", true},
		{"ui.entry non-ascii", "ui.entry", "/ui/ä.js", true},
		{"ui.entry space", "ui.entry", "/ui/a b.js", true},
		{"ui.entry valid", "ui.entry", "/ui/v1.2/index-abc_~.js", false},

		// api.basePath cases
		{"api.basePath percent", "api.basePath", "/api/%2e%2e", true},
		{"api.basePath trailing slash", "api.basePath", "/api/", false},
		{"api.basePath with path", "api.basePath", "/api/v1/", false},

		// mcp.path case
		{"mcp.path backslash", "mcp.path", "/mcp\\api", true},

		// ui.pages[0].path case
		{"ui.pages[0].path dot-dot", "ui.pages[0].path", "/a/../../api", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePath(tc.field, tc.path)
			if tc.shouldErr && err == nil {
				t.Errorf("validatePath(%q, %q): expected error, got nil", tc.field, tc.path)
			}
			if !tc.shouldErr && err != nil {
				t.Errorf("validatePath(%q, %q): unexpected error: %v", tc.field, tc.path, err)
			}
			if err != nil && !strings.Contains(err.Error(), tc.field) {
				t.Errorf("validatePath error = %q, want to contain %q", err.Error(), tc.field)
			}
		})
	}
}

func TestValidate_PageComponentAndVisibility(t *testing.T) {
	cases := []struct {
		name       string
		component  string
		visibility string
		shouldErr  bool
		errContent string
	}{
		// Component tests
		{"component valid", "MemoryPage", "", false, ""},
		{"component empty", "", "", false, ""},
		{"component with underscore", "_MyComponent", "", false, ""},
		{"component with dollar", "$Component", "", false, ""},
		{"component invalid starts with digit", "1abc", "", true, "component"},
		{"component invalid with dash", "a-b", "", true, "component"},

		// Visibility tests
		{"visibility empty", "", "", false, ""},
		{"visibility admin", "", "admin", false, ""},
		{"visibility invalid public", "", "public", true, "visibility"},
		{"visibility invalid other", "", "private", true, "visibility"},

		// Combined
		{"component and visibility", "Settings", "admin", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m["ui"] = map[string]any{
				"entry": "/plugins/mem0/index.js",
				"pages": []any{
					map[string]any{
						"path":       "/settings",
						"label":      "Settings",
						"component":  tc.component,
						"visibility": tc.visibility,
					},
				},
			}
			_, err := Parse(marshalManifest(t, m))
			if tc.shouldErr && err == nil {
				t.Fatalf("Parse: expected error, got nil")
			}
			if !tc.shouldErr && err != nil {
				t.Fatalf("Parse: unexpected error: %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), tc.errContent) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.errContent)
			}
		})
	}
}

func TestCompatible_HostIs11(t *testing.T) {
	cases := []struct {
		pluginAPI string
		want      bool
	}{
		{pluginAPI: "1.0", want: true},
		{pluginAPI: "1.1", want: true},
		{pluginAPI: "1.2", want: false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("plugin_%s_vs_host_%s", tc.pluginAPI, HostAPIVersion), func(t *testing.T) {
			got, err := Compatible(tc.pluginAPI, HostAPIVersion)
			if err != nil {
				t.Fatalf("Compatible(%q, %q): unexpected error: %v", tc.pluginAPI, HostAPIVersion, err)
			}
			if got != tc.want {
				t.Errorf("Compatible(%q, %q) = %v, want %v", tc.pluginAPI, HostAPIVersion, got, tc.want)
			}
		})
	}
}

// TestValidate_MCPAudience covers the per-tool audience map on the MCP spec:
// only the three closed values are allowed, and every reported field name
// identifies the offending tool so a plugin author knows what to fix.
func TestValidate_MCPAudience(t *testing.T) {
	cases := []struct {
		name     string
		audience map[string]string
		wantErr  string
	}{
		{
			name:     "valid",
			audience: map[string]string{"search": AudienceAgent, "add": AudienceOperator, "list": AudienceBoth},
		},
		{
			name:     "empty map",
			audience: map[string]string{},
		},
		{
			name:     "value admin is invalid",
			audience: map[string]string{"x": "admin"},
			wantErr:  `plugin: invalid mcp.audience["x"] value "admin": must be "agent", "operator", or "both"`,
		},
		{
			name:     "value agent-only capitalised is invalid",
			audience: map[string]string{"search": "Agent"},
			wantErr:  `plugin: invalid mcp.audience["search"] value "Agent": must be "agent", "operator", or "both"`,
		},
		{
			name:     "empty value is invalid",
			audience: map[string]string{"search": ""},
			wantErr:  `plugin: invalid mcp.audience["search"] value "": must be "agent", "operator", or "both"`,
		},
		{
			name:     "empty tool name is invalid",
			audience: map[string]string{"": AudienceAgent},
			wantErr:  `plugin: invalid mcp.audience[""]: tool name is required`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m["mcp"] = map[string]any{"path": "/mcp", "audience": tc.audience}

			_, err := Parse(marshalManifest(t, m))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse with audience %v: unexpected error: %v", tc.audience, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse with audience %v: expected error %q, got nil", tc.audience, tc.wantErr)
			}
			if got := err.Error(); got != tc.wantErr {
				t.Errorf("Parse error = %q, want %q", got, tc.wantErr)
			}
		})
	}

	// Several bad entries are reported together, in a stable order (the
	// audience is a map and Validate's error is stored verbatim on the Plugin
	// status by the reconciler).
	m := validManifest()
	m["mcp"] = map[string]any{
		"path": "/mcp",
		"audience": map[string]string{
			"zzz": "admin",
			"aaa": "root",
		},
	}
	var parsed Manifest
	if err := json.Unmarshal(marshalManifest(t, m), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := parsed.Validate()
	if got == nil {
		t.Fatalf("Validate: expected error, got nil")
	}
	want := "plugin: invalid mcp.audience[\"aaa\"] value \"root\": must be \"agent\", \"operator\", or \"both\"\n" +
		"plugin: invalid mcp.audience[\"zzz\"] value \"admin\": must be \"agent\", \"operator\", or \"both\""
	if got.Error() != want {
		t.Errorf("Validate error =\n%q\nwant\n%q", got.Error(), want)
	}
}

// TestValidate_Skills pins the rules for the top-level skills list: at most 20
// entries, unique slug names, descriptions capped at 500 characters, and paths
// under the same rules as every other manifest path.
func TestValidate_Skills(t *testing.T) {
	skill := func(name, description, path string) map[string]any {
		return map[string]any{"name": name, "description": description, "path": path}
	}

	many := make([]any, 21)
	for i := range many {
		many[i] = skill(fmt.Sprintf("skill-%d", i), "description", "/skills/guide/SKILL.md")
	}

	cases := []struct {
		name    string
		skills  []any
		wantErr string
	}{
		{
			name: "valid",
			skills: []any{
				skill("memory-guide", "How the plugin stores memories", "/skills/memory-guide/SKILL.md"),
				skill("recall", "Recall usage", "/skills/recall/SKILL.md"),
			},
		},
		{
			name:    "21 skills is too many",
			skills:  many,
			wantErr: "plugin: too many skills: 21, maximum is 20",
		},
		{
			name: "duplicate name",
			skills: []any{
				skill("guide", "first", "/skills/a/SKILL.md"),
				skill("guide", "second", "/skills/b/SKILL.md"),
			},
			wantErr: `plugin: duplicate skill name "guide": skill names must be unique`,
		},
		{
			name:    "bad slug with underscore and uppercase",
			skills:  []any{skill("Mem_0", "desc", "/skills/x/SKILL.md")},
			wantErr: "plugin: invalid skills[0].name \"Mem_0\": must match ^[a-z][a-z0-9-]{0,48}[a-z0-9]$",
		},
		{
			name:    "empty name",
			skills:  []any{skill("", "desc", "/skills/x/SKILL.md")},
			wantErr: "plugin: invalid skills[0].name \"\": must match ^[a-z][a-z0-9-]{0,48}[a-z0-9]$",
		},
		{
			name:    "description 501 characters",
			skills:  []any{skill("guide", strings.Repeat("a", 501), "/skills/x/SKILL.md")},
			wantErr: "plugin: invalid skills[0].description: must be at most 500 characters, got 501",
		},
		{
			name:    "path escapes the plugin",
			skills:  []any{skill("guide", "desc", "../x")},
			wantErr: `plugin: invalid skills[0].path "../x": must start with "/"`,
		},
		{
			name:    "path missing leading slash",
			skills:  []any{skill("guide", "desc", "skills/x/SKILL.md")},
			wantErr: `plugin: invalid skills[0].path "skills/x/SKILL.md": must start with "/"`,
		},
		{
			name:    "empty path",
			skills:  []any{skill("guide", "desc", "")},
			wantErr: "plugin: skills[0].path is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m["skills"] = tc.skills

			_, err := Parse(marshalManifest(t, m))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse with %d skills: unexpected error: %v", len(tc.skills), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse with %d skills: expected error %q, got nil", len(tc.skills), tc.wantErr)
			}
			if got := err.Error(); got != tc.wantErr {
				t.Errorf("Parse error = %q, want %q", got, tc.wantErr)
			}
		})
	}

	// Boundaries: 20 skills and a 500-character description are accepted.
	m := validManifest()
	m["skills"] = many[:20]
	if _, err := Parse(marshalManifest(t, m)); err != nil {
		t.Errorf("Parse with exactly 20 skills: unexpected error: %v", err)
	}
	m = validManifest()
	m["skills"] = []any{skill("guide", strings.Repeat("a", 500), "/skills/guide/SKILL.md")}
	parsed, err := Parse(marshalManifest(t, m))
	if err != nil {
		t.Fatalf("Parse with 500-character description: unexpected error: %v", err)
	}
	if len(parsed.Skills) != 1 || parsed.Skills[0].Name != "guide" ||
		parsed.Skills[0].Path != "/skills/guide/SKILL.md" || len(parsed.Skills[0].Description) != 500 {
		t.Errorf("Skills = %+v, want one skill guide with a 500-character description", parsed.Skills)
	}

	// Absent skills parse as nil: the field is optional.
	minimal := map[string]any{
		"id": "mem0", "displayName": "Mem0", "version": "1.0.0", "pluginApi": "1.1",
	}
	parsed, err = Parse(marshalManifest(t, minimal))
	if err != nil {
		t.Fatalf("Parse minimal manifest: unexpected error: %v", err)
	}
	if parsed.Skills != nil {
		t.Errorf("Skills = %v, want nil", parsed.Skills)
	}
}

// TestToolAudience resolves the audience of one tool: a listed tool reports
// its declared value, everything else — including a plugin with no MCP spec at
// all — is visible to both audiences.
func TestToolAudience(t *testing.T) {
	m := &Manifest{
		MCP: &MCPSpec{
			Path:     "/mcp",
			Audience: map[string]string{"search": AudienceAgent, "add": AudienceOperator},
		},
	}

	if got := m.ToolAudience("search"); got != AudienceAgent {
		t.Errorf("ToolAudience(search) = %q, want %q", got, AudienceAgent)
	}
	if got := m.ToolAudience("add"); got != AudienceOperator {
		t.Errorf("ToolAudience(add) = %q, want %q", got, AudienceOperator)
	}
	if got := m.ToolAudience("unlisted"); got != AudienceBoth {
		t.Errorf("ToolAudience(unlisted) = %q, want %q", got, AudienceBoth)
	}

	// MCP present but no audience declared.
	if got := (&Manifest{MCP: &MCPSpec{Path: "/mcp"}}).ToolAudience("search"); got != AudienceBoth {
		t.Errorf("ToolAudience without audience = %q, want %q", got, AudienceBoth)
	}
	// No MCP spec at all.
	if got := (&Manifest{}).ToolAudience("search"); got != AudienceBoth {
		t.Errorf("ToolAudience without MCP = %q, want %q", got, AudienceBoth)
	}
	// A manifest that never parsed.
	var nilManifest *Manifest
	if got := nilManifest.ToolAudience("search"); got != AudienceBoth {
		t.Errorf("ToolAudience on nil manifest = %q, want %q", got, AudienceBoth)
	}

	if AudienceAgent != "agent" || AudienceOperator != "operator" || AudienceBoth != "both" {
		t.Errorf("audience constants = %q %q %q, want agent operator both",
			AudienceAgent, AudienceOperator, AudienceBoth)
	}
}
