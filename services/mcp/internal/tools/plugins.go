package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// PluginTools wraps the hub's /api/v1/plugins admin endpoints (plugin system
// phase 1). A plugin is third-party code the platform routes to: it is
// deployed by the operator out of band and *registered* here, then gated by
// two explicit admin decisions — a permission approval and a group-access
// grant. Reads are open to any authenticated caller (the hub narrows the
// shape for non-admins); every write here is admin-only and audit-logged.
type PluginTools struct {
	HubURL     string
	HTTPClient *http.Client
}

func NewPluginTools(hubURL string) *PluginTools {
	return &PluginTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// stringSliceArg reads an array-of-strings argument. It distinguishes "absent"
// (ok=false) from "present but empty" (ok=true, empty slice) — an empty list
// is a meaningful value for group access, where it clears the grant.
func stringSliceArg(args map[string]any, key string) ([]string, bool) {
	raw, present := args[key]
	if !present {
		return nil, false
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...), true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

// pluginIDArg pulls the required id out of a call's arguments.
func pluginIDArg(req mcp.CallToolRequest) (map[string]any, string, *mcp.CallToolResult) {
	args, _ := req.Params.Arguments.(map[string]any)
	id, _ := args["id"].(string)
	if id == "" {
		return args, "", mcp.NewToolResultError("id is required")
	}
	return args, id, nil
}

// --- list / get ---

func (t *PluginTools) ListPluginsTool() mcp.Tool {
	return mcp.NewTool("list_plugins",
		mcp.WithDescription("List plugins registered with the platform, including readiness, declared vs. approved permissions, and group access (admin view; non-admins only see enabled, ready plugins). Default page size: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1)")),
		mcp.WithNumber("pageSize", mcp.Description("Page size (default: 50)")),
	)
}

func (t *PluginTools) GetPluginTool() mcp.Tool {
	return mcp.NewTool("get_plugin",
		mcp.WithDescription("Get one plugin by id: service endpoint, pinned image, Ready condition with reason/message, declared vs. approved permissions, group access."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
	)
}

func (t *PluginTools) ListPlugins(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	q := url.Values{}
	q.Set("page", strconv.Itoa(defaultArgInt(args, "page", 1)))
	q.Set("pageSize", strconv.Itoa(defaultArgInt(args, "pageSize", 50)))
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/plugins?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list plugins: %v", err)), nil
	}
	body = annotatePageMeta(body, "more plugins available — pass page=N to fetch the next page")
	return mcp.NewToolResultText(string(body)), nil
}

func (t *PluginTools) GetPlugin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, id, fail := pluginIDArg(req)
	if fail != nil {
		return fail, nil
	}
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/plugins/"+url.PathEscape(id))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get plugin %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// --- install / uninstall ---

func (t *PluginTools) InstallPluginTool() mcp.Tool {
	return mcp.NewTool("install_plugin",
		mcp.WithDescription("Register an already deployed plugin with the platform so events can be routed to it. Admin-only. This does NOT deploy anything: the plugin's Deployment and Service must already exist in the platform namespace, and the image is pinned by digest. Uninstalling later also leaves the workload running."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id (^[a-z][a-z0-9-]{0,29}[a-z0-9]$)")),
		mcp.WithString("serviceName", mcp.Required(), mcp.Description("Name of the plugin's existing in-cluster Service")),
		mcp.WithNumber("servicePort", mcp.Required(), mcp.Description("Port the plugin's Service listens on (1-65535)")),
		mcp.WithString("imageRepository", mcp.Required(), mcp.Description("Image repository the plugin was deployed from")),
		mcp.WithString("imageDigest", mcp.Required(), mcp.Description("Immutable image digest (sha256:<64 hex>)")),
		mcp.WithString("displayName", mcp.Description("Human-readable name (defaults to the id)")),
	)
}

func (t *PluginTools) UninstallPluginTool() mcp.Tool {
	return mcp.NewTool("uninstall_plugin",
		mcp.WithDescription("Unregister a plugin from the platform so it no longer receives events. Admin-only. This does not remove the running workload — the plugin's Deployment and Service keep running until you delete them yourself."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
	)
}

func (t *PluginTools) InstallPlugin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	id, _ := args["id"].(string)
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	serviceName, _ := args["serviceName"].(string)
	if serviceName == "" {
		return mcp.NewToolResultError("serviceName is required"), nil
	}
	port, _ := args["servicePort"].(float64)
	if port < 1 || port > 65535 {
		return mcp.NewToolResultError("servicePort is required and must be between 1 and 65535"), nil
	}
	imageRepository, _ := args["imageRepository"].(string)
	if imageRepository == "" {
		return mcp.NewToolResultError("imageRepository is required"), nil
	}
	imageDigest, _ := args["imageDigest"].(string)
	if imageDigest == "" {
		return mcp.NewToolResultError("imageDigest is required"), nil
	}

	payload := map[string]any{
		"id":              id,
		"serviceName":     serviceName,
		"servicePort":     int(port),
		"imageRepository": imageRepository,
		"imageDigest":     imageDigest,
	}
	if dn, _ := args["displayName"].(string); dn != "" {
		payload["displayName"] = dn
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/plugins", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to install plugin %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

func (t *PluginTools) UninstallPlugin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, id, fail := pluginIDArg(req)
	if fail != nil {
		return fail, nil
	}
	if _, err := hubDelete(ctx, t.HTTPClient, t.HubURL, "/api/v1/plugins/"+url.PathEscape(id)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to uninstall plugin %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("plugin %s uninstalled; its Deployment and Service are still running", id)), nil
}

// --- enable / disable ---

func (t *PluginTools) EnablePluginTool() mcp.Tool {
	return mcp.NewTool("enable_plugin",
		mcp.WithDescription("Enable a disabled plugin so the platform routes events to it again. Admin-only; the workload itself is unaffected."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
	)
}

func (t *PluginTools) DisablePluginTool() mcp.Tool {
	return mcp.NewTool("disable_plugin",
		mcp.WithDescription("Disable a plugin so the platform stops routing events to it, without touching the running workload. Admin-only."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
	)
}

func (t *PluginTools) EnablePlugin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return t.togglePlugin(ctx, req, "enable")
}

func (t *PluginTools) DisablePlugin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return t.togglePlugin(ctx, req, "disable")
}

// togglePlugin shares the enable/disable path — the hub exposes them as two
// POST subresources with identical request/response shapes.
func (t *PluginTools) togglePlugin(ctx context.Context, req mcp.CallToolRequest, action string) (*mcp.CallToolResult, error) {
	_, id, fail := pluginIDArg(req)
	if fail != nil {
		return fail, nil
	}
	body, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/plugins/"+url.PathEscape(id)+"/"+action, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to %s plugin %s: %v", action, id, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// --- permissions / group access ---

func (t *PluginTools) ApprovePluginPermissionsTool() mcp.Tool {
	return mcp.NewTool("approve_plugin_permissions",
		mcp.WithDescription("Approve exactly the permission set a plugin's manifest declares. Admin-only. The request must match the declared set exactly — a subset or an undeclared permission is rejected with a conflict listing what is declared. New declarations require a fresh approval."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
		mcp.WithArray("permissions", mcp.Required(), mcp.WithStringItems(),
			mcp.Description("The plugin's complete declared permission set")),
	)
}

func (t *PluginTools) SetPluginGroupAccessTool() mcp.Tool {
	return mcp.NewTool("set_plugin_group_access",
		mcp.WithDescription("Set which groups' background identity a plugin may act on behalf of. Admin-only. Replaces the current grant; pass an empty array to revoke all group access."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Plugin id")),
		mcp.WithArray("groups", mcp.Required(), mcp.WithStringItems(),
			mcp.Description("Group ids the plugin may act for (empty array clears the grant)")),
	)
}

func (t *PluginTools) ApprovePluginPermissions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, id, fail := pluginIDArg(req)
	if fail != nil {
		return fail, nil
	}
	permissions, ok := stringSliceArg(args, "permissions")
	if !ok {
		return mcp.NewToolResultError("permissions is required"), nil
	}
	return t.pluginArrayRequest(ctx, http.MethodPost, id, "approve-permissions", "permissions", permissions,
		fmt.Sprintf("approve permissions for plugin %s", id))
}

func (t *PluginTools) SetPluginGroupAccess(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, id, fail := pluginIDArg(req)
	if fail != nil {
		return fail, nil
	}
	groups, ok := stringSliceArg(args, "groups")
	if !ok {
		return mcp.NewToolResultError("groups is required"), nil
	}
	return t.pluginArrayRequest(ctx, http.MethodPut, id, "group-access", "groups", groups,
		fmt.Sprintf("set group access for plugin %s", id))
}

// pluginArrayRequest sends {"<key>": [...]} to a plugin subresource — POST for
// permission approvals, PUT for group access, matching the hub's routes.
func (t *PluginTools) pluginArrayRequest(ctx context.Context, method, id, subresource, key string, values []string, what string) (*mcp.CallToolResult, error) {
	if values == nil {
		values = []string{}
	}
	payload, err := json.Marshal(map[string]any{key: values})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	body, err := hubRequestWithCodes(ctx, t.HTTPClient, method, t.HubURL, "/api/v1/plugins/"+url.PathEscape(id)+"/"+subresource, bytes.NewReader(payload), http.StatusOK)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to %s: %v", what, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
