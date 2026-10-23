package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// captureHub is a fake hub that records the last request it received so the
// tests can assert method + path + body exactly as the tool sent them.
type captureHub struct {
	method  string
	path    string
	rawPath string
	query   string
	body    map[string]any
	called  bool
}

func (c *captureHub) handler(status int, resp map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.path = r.URL.Path
		c.rawPath = r.URL.EscapedPath()
		c.query = r.URL.RawQuery
		c.called = true
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &c.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if resp != nil {
			_ = json.NewEncoder(w).Encode(resp)
		}
	}
}

func pluginToolText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("tool returned no content")
	}
	return result.Content[0].(mcp.TextContent).Text
}

func TestListPlugins(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"items": []map[string]any{
			{"id": "linear", "displayName": "Linear", "serviceName": "linear", "servicePort": 8080},
		},
		"total": 1, "page": 2, "pageSize": 10, "totalPages": 2,
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"page": float64(2), "pageSize": float64(10)}
	result, err := pt.ListPlugins(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodGet || hub.path != "/api/v1/plugins" {
		t.Errorf("expected GET /api/v1/plugins, got %s %s", hub.method, hub.path)
	}
	if !strings.Contains(hub.query, "page=2") || !strings.Contains(hub.query, "pageSize=10") {
		t.Errorf("expected page+pageSize forwarded; got: %s", hub.query)
	}
	if !strings.Contains(pluginToolText(t, result), "linear") {
		t.Errorf("expected plugin id in response; got: %s", pluginToolText(t, result))
	}
}

func TestGetPlugin(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear", "serviceName": "linear", "servicePort": float64(8080), "ready": true,
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.GetPlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodGet || hub.path != "/api/v1/plugins/linear" {
		t.Errorf("expected GET /api/v1/plugins/linear, got %s %s", hub.method, hub.path)
	}
	if !strings.Contains(pluginToolText(t, result), `"ready":true`) {
		t.Errorf("expected plugin detail in response; got: %s", pluginToolText(t, result))
	}
}

func TestGetPluginMissingID(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	result, err := pt.GetPlugin(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing id")
	}
	if got := pluginToolText(t, result); got != "id is required" {
		t.Errorf("expected %q, got %q", "id is required", got)
	}
	if hub.called {
		t.Error("expected no hub call when id is missing")
	}
}

func TestInstallPlugin(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusCreated, map[string]any{
		"id": "linear", "displayName": "Linear", "serviceName": "linear", "servicePort": float64(8080),
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":              "linear",
		"displayName":     "Linear",
		"serviceName":     "linear",
		"servicePort":     float64(8080),
		"imageRepository": "registry.example.com/ainsel/plugin-linear",
		"imageDigest":     "sha256:" + strings.Repeat("ab", 32),
	}
	result, err := pt.InstallPlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodPost || hub.path != "/api/v1/plugins" {
		t.Errorf("expected POST /api/v1/plugins, got %s %s", hub.method, hub.path)
	}
	want := map[string]any{
		"id":              "linear",
		"displayName":     "Linear",
		"serviceName":     "linear",
		"servicePort":     float64(8080),
		"imageRepository": "registry.example.com/ainsel/plugin-linear",
		"imageDigest":     "sha256:" + strings.Repeat("ab", 32),
	}
	for k, v := range want {
		if hub.body[k] != v {
			t.Errorf("body[%s]: expected %v, got %v (body: %v)", k, v, hub.body[k], hub.body)
		}
	}
}

func TestInstallPluginMissingRequired(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusCreated, map[string]any{}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":          "linear",
		"serviceName": "linear",
		// servicePort, imageRepository, imageDigest omitted
	}
	result, err := pt.InstallPlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing required args")
	}
	if !strings.Contains(pluginToolText(t, result), "servicePort") {
		t.Errorf("expected servicePort named in error; got: %s", pluginToolText(t, result))
	}
	if hub.called {
		t.Error("expected no hub call when required args are missing")
	}
}

func TestUninstallPlugin(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusNoContent, nil))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.UninstallPlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodDelete || hub.path != "/api/v1/plugins/linear" {
		t.Errorf("expected DELETE /api/v1/plugins/linear, got %s %s", hub.method, hub.path)
	}
	if !strings.Contains(pluginToolText(t, result), "linear") {
		t.Errorf("expected plugin id in confirmation; got: %s", pluginToolText(t, result))
	}
}

func TestEnablePlugin(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear", "disabled": false,
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.EnablePlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodPost || hub.path != "/api/v1/plugins/linear/enable" {
		t.Errorf("expected POST /api/v1/plugins/linear/enable, got %s %s", hub.method, hub.path)
	}
}

func TestDisablePlugin(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear", "disabled": true,
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.DisablePlugin(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodPost || hub.path != "/api/v1/plugins/linear/disable" {
		t.Errorf("expected POST /api/v1/plugins/linear/disable, got %s %s", hub.method, hub.path)
	}
}

func TestApprovePluginPermissions(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear", "approvedPermissions": []string{"read:issues"},
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":          "linear",
		"permissions": []any{"read:issues", "write:comments"},
	}
	result, err := pt.ApprovePluginPermissions(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodPost || hub.path != "/api/v1/plugins/linear/approve-permissions" {
		t.Errorf("expected POST /api/v1/plugins/linear/approve-permissions, got %s %s", hub.method, hub.path)
	}
	perms, ok := hub.body["permissions"].([]any)
	if !ok || len(perms) != 2 || perms[0] != "read:issues" || perms[1] != "write:comments" {
		t.Errorf("expected permissions array forwarded; got: %v", hub.body)
	}
}

func TestApprovePluginPermissionsConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "permissions must match the plugin's declared set exactly (declared: [read:issues])",
		})
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":          "linear",
		"permissions": []any{"read:issues", "write:comments"},
	}
	result, err := pt.ApprovePluginPermissions(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error on 409")
	}
	text := pluginToolText(t, result)
	if !strings.Contains(text, "declared set exactly") || !strings.Contains(text, "409") {
		t.Errorf("expected hub error text surfaced; got: %s", text)
	}
}

func TestApprovePluginPermissionsMissing(t *testing.T) {
	pt := &PluginTools{HubURL: "http://nope", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.ApprovePluginPermissions(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || pluginToolText(t, result) != "permissions is required" {
		t.Errorf("expected permissions-required error; got: %v / %s", result.IsError, pluginToolText(t, result))
	}
}

func TestSetPluginGroupAccess(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear", "groupAccess": []string{"platform-admins"},
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":     "linear",
		"groups": []any{"platform-admins"},
	}
	result, err := pt.SetPluginGroupAccess(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	if hub.method != http.MethodPut || hub.path != "/api/v1/plugins/linear/group-access" {
		t.Errorf("expected PUT /api/v1/plugins/linear/group-access, got %s %s", hub.method, hub.path)
	}
	groups, ok := hub.body["groups"].([]any)
	if !ok || len(groups) != 1 || groups[0] != "platform-admins" {
		t.Errorf("expected groups array forwarded; got: %v", hub.body)
	}
}

func TestSetPluginGroupAccessEmptyClears(t *testing.T) {
	hub := &captureHub{}
	srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{
		"id": "linear",
	}))
	defer srv.Close()

	pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear", "groups": []any{}}
	result, err := pt.SetPluginGroupAccess(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", pluginToolText(t, result))
	}
	groups, ok := hub.body["groups"].([]any)
	if !ok || len(groups) != 0 {
		t.Errorf("expected empty groups array sent; got: %v", hub.body)
	}
}

func TestSetPluginGroupAccessMissing(t *testing.T) {
	pt := &PluginTools{HubURL: "http://nope", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "linear"}
	result, err := pt.SetPluginGroupAccess(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || pluginToolText(t, result) != "groups is required" {
		t.Errorf("expected groups-required error; got: %v / %s", result.IsError, pluginToolText(t, result))
	}
}

// A crafted id must stay a single path segment: unescaped, an id like "x/../y"
// would make the MCP server issue a path-traversal request to the hub carrying
// the caller's bearer token. channels.go established the escaping convention;
// the plugin tools follow it.
func TestPluginToolsEscapeIDInPath(t *testing.T) {
	idRequest := func(extra map[string]any) mcp.CallToolRequest {
		args := map[string]any{"id": "x/../y"}
		for k, v := range extra {
			args[k] = v
		}
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		return req
	}

	tests := []struct {
		name string
		call func(*PluginTools) (*mcp.CallToolResult, error)
	}{
		{"get", func(pt *PluginTools) (*mcp.CallToolResult, error) {
			return pt.GetPlugin(context.Background(), idRequest(nil))
		}},
		{"uninstall", func(pt *PluginTools) (*mcp.CallToolResult, error) {
			return pt.UninstallPlugin(context.Background(), idRequest(nil))
		}},
		{"enable", func(pt *PluginTools) (*mcp.CallToolResult, error) {
			return pt.EnablePlugin(context.Background(), idRequest(nil))
		}},
		{"approve-permissions", func(pt *PluginTools) (*mcp.CallToolResult, error) {
			return pt.ApprovePluginPermissions(context.Background(), idRequest(map[string]any{"permissions": []any{"agents:read"}}))
		}},
		{"group-access", func(pt *PluginTools) (*mcp.CallToolResult, error) {
			return pt.SetPluginGroupAccess(context.Background(), idRequest(map[string]any{"groups": []any{"platform"}}))
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hub := &captureHub{}
			srv := httptest.NewServer(hub.handler(http.StatusOK, map[string]any{"id": "x/../y"}))
			defer srv.Close()

			pt := &PluginTools{HubURL: srv.URL, HTTPClient: srv.Client()}
			result, err := tc.call(pt)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("tool returned error: %s", pluginToolText(t, result))
			}
			// The id must arrive as one escaped segment, not as extra path
			// segments the hub's router would resolve elsewhere.
			if !strings.Contains(hub.rawPath, "x%2F..%2Fy") {
				t.Errorf("escaped path = %q, want the id escaped as x%%2F..%%2Fy (path %q)", hub.rawPath, hub.path)
			}
		})
	}
}

func TestPluginToolDefinitions(t *testing.T) {
	pt := NewPluginTools("http://hub")
	want := map[string]string{
		"install_plugin":   "already deployed",
		"uninstall_plugin": "does not remove",
	}
	defs := map[string]mcp.Tool{
		"list_plugins":               pt.ListPluginsTool(),
		"get_plugin":                 pt.GetPluginTool(),
		"install_plugin":             pt.InstallPluginTool(),
		"uninstall_plugin":           pt.UninstallPluginTool(),
		"enable_plugin":              pt.EnablePluginTool(),
		"disable_plugin":             pt.DisablePluginTool(),
		"approve_plugin_permissions": pt.ApprovePluginPermissionsTool(),
		"set_plugin_group_access":    pt.SetPluginGroupAccessTool(),
	}
	for name, tool := range defs {
		if tool.Name != name {
			t.Errorf("expected tool name %s, got %s", name, tool.Name)
		}
	}
	if !strings.Contains(defs["install_plugin"].Description, want["install_plugin"]) {
		t.Errorf("install_plugin description must say the plugin is already deployed; got: %s", defs["install_plugin"].Description)
	}
	if !strings.Contains(defs["uninstall_plugin"].Description, want["uninstall_plugin"]) {
		t.Errorf("uninstall_plugin description must say the workload keeps running; got: %s", defs["uninstall_plugin"].Description)
	}
	for _, name := range []string{"install_plugin", "uninstall_plugin", "enable_plugin", "disable_plugin", "approve_plugin_permissions", "set_plugin_group_access"} {
		if !strings.Contains(strings.ToLower(defs[name].Description), "admin") {
			t.Errorf("%s description must say admin-only; got: %s", name, defs[name].Description)
		}
	}
	// Required-arg wiring: the schema must mark the args the hub requires.
	for name, required := range map[string][]string{
		"get_plugin":                 {"id"},
		"install_plugin":             {"id", "serviceName", "servicePort", "imageRepository", "imageDigest"},
		"approve_plugin_permissions": {"id", "permissions"},
		"set_plugin_group_access":    {"id", "groups"},
	} {
		got := map[string]bool{}
		for _, r := range defs[name].InputSchema.Required {
			got[r] = true
		}
		for _, r := range required {
			if !got[r] {
				t.Errorf("%s: expected %s to be required; required=%v", name, r, defs[name].InputSchema.Required)
			}
		}
	}
}
