package plugin

import (
	"encoding/json"
	"fmt"
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
	if HostAPIVersion != "1.0" {
		t.Errorf("HostAPIVersion = %q, want %q", HostAPIVersion, "1.0")
	}
	if ManifestPath != "/ainsel-plugin.json" {
		t.Errorf("ManifestPath = %q, want %q", ManifestPath, "/ainsel-plugin.json")
	}
	if MaxManifestBytes != 256<<10 {
		t.Errorf("MaxManifestBytes = %d, want %d", MaxManifestBytes, 256<<10)
	}
	if len(Permissions) != 9 {
		t.Errorf("len(Permissions) = %d, want 9", len(Permissions))
	}
	if len(Slots) != 4 {
		t.Errorf("len(Slots) = %d, want 4", len(Slots))
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
