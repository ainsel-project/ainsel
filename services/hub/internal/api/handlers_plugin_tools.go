package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginproxy"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"github.com/mark3labs/mcp-go/mcp"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The plugin tool surface for AInsel's own MCP server (operators). The hub is the
// only MCP client of a plugin: services/mcp never talks to a plugin, it asks the
// hub which tools exist and hands a call back to the hub, which attaches the
// caller's credential. That keeps the audience rule in one place — an
// agent-audience tool is neither listed nor callable here, for anyone.

// pluginToolEntry is one tool in the operator list. Name is the plugin's own
// tool name; services/mcp prefixes it with the plugin id when it registers the
// tool, so the hub must not rewrite it here.
type pluginToolEntry struct {
	PluginID    string          `json:"pluginId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// pluginToolsListResponse is the body of GET /api/internal/plugin-tools. Tools
// is never nil, so an empty result is an empty array rather than null.
type pluginToolsListResponse struct {
	Tools []pluginToolEntry `json:"tools"`
}

// handleInternalPluginTools serves GET /api/internal/plugin-tools: every tool of
// the operator audience ("operator" or "both") that the usable plugins expose,
// for services/mcp to aggregate. Protected by X-Internal-Token, like the other
// internal routes.
//
// A plugin that cannot be reached contributes nothing and never fails the
// response: one broken plugin must not take operator tools — or the aggregator's
// whole tools/list — down with it.
func (s *Server) handleInternalPluginTools(w http.ResponseWriter, r *http.Request) {
	if !s.requireInternalToken(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()

	var list connectorv1alpha1.PluginList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entries := []pluginToolEntry{}
	for _, item := range list.Items {
		plugin, err := s.availablePlugin(ctx, item.Name)
		if err != nil {
			// Missing, disabled or not Ready: invisible to operators.
			continue
		}
		manifest, ok := pluginManifest(plugin)
		if !ok || manifest.MCP == nil {
			// No MCP endpoint declared: the plugin has no tools.
			continue
		}
		endpoint, ok := pluginproxy.PickEndpoint(plugin.Status.VerifiedEndpoints, nil)
		if !ok {
			slog.Debug("plugin tools: no verified endpoint, skipping plugin", "plugin", plugin.Name)
			continue
		}

		// tools/list runs as the plugin's own background identity: no user is
		// attached to a list that serves every caller.
		credential, _, err := s.MintPluginBackgroundCredential(ctx, plugin.Name)
		if err != nil {
			slog.Debug("plugin tools: background credential failed, skipping plugin", "plugin", plugin.Name, "error", err)
			continue
		}
		tools, err := s.getPluginMCPClient().ListTools(ctx, pluginMCPTarget(plugin.Name, endpoint, manifest), credential)
		if err != nil {
			slog.Debug("plugin tools: plugin unreachable, skipping", "plugin", plugin.Name, "error", err)
			continue
		}

		for _, tool := range tools {
			if manifest.ToolAudience(tool.Name) == pluginpkg.AudienceAgent {
				continue
			}
			entries = append(entries, pluginToolEntry{
				PluginID:    plugin.Name,
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: pluginToolInputSchema(tool),
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].PluginID != entries[j].PluginID {
			return entries[i].PluginID < entries[j].PluginID
		}
		return entries[i].Name < entries[j].Name
	})

	writeJSON(w, http.StatusOK, pluginToolsListResponse{Tools: entries})
}

// pluginToolCallRequest is the body of POST /api/v1/plugin-tools/call.
type pluginToolCallRequest struct {
	PluginID  string         `json:"pluginId"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// handlePluginToolCall serves POST /api/v1/plugin-tools/call: it runs one
// operator-audience tool on a plugin and answers with the plugin's
// CallToolResult unchanged (a tool-level error stays inside it as isError). The
// caller is an authenticated user — this route is on the user-auth surface and
// is deliberately absent from the plugin-credential route table, so an `ainselp_`
// caller cannot call tools back into the hub.
//
// Status codes: plugin missing/disabled/not Ready → 404; tool unknown or of the
// agent audience → 404 "tool not found"; plugin unreachable → 502.
func (s *Server) handlePluginToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := oidc.FromContext(r.Context())
	if !ok || user == nil || user.Sub == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ctx := r.Context()

	var body pluginToolCallRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.PluginID == "" || body.Tool == "" {
		writeError(w, http.StatusBadRequest, "pluginId and tool are required")
		return
	}

	plugin, err := s.availablePlugin(ctx, body.PluginID)
	if err != nil {
		if errors.Is(err, errPluginUnavailable) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	manifest, ok := pluginManifest(plugin)
	if !ok || manifest.MCP == nil {
		// A plugin without an MCP endpoint serves no tools at all.
		writeError(w, http.StatusNotFound, "tool not found")
		return
	}
	endpoint, ok := pluginproxy.PickEndpoint(plugin.Status.VerifiedEndpoints, nil)
	if !ok {
		writeError(w, http.StatusBadGateway, "plugin unreachable")
		return
	}
	target := pluginMCPTarget(plugin.Name, endpoint, manifest)

	// The plugin's own list decides both existence and audience, so a tool the
	// plugin does not serve and a tool it serves only to agents are the same
	// answer here: the endpoint does not disclose which tools a plugin keeps
	// private. It is the cached list, so this costs nothing on a warm hub.
	listCredential, _, err := s.MintPluginBackgroundCredential(ctx, plugin.Name)
	if err != nil {
		writePluginMintError(w, err)
		return
	}
	tools, err := s.getPluginMCPClient().ListTools(ctx, target, listCredential)
	if err != nil {
		slog.Warn("plugin tool call: plugin unreachable", "plugin", plugin.Name, "error", err)
		writeError(w, http.StatusBadGateway, "plugin unreachable")
		return
	}
	if !pluginToolIsKnown(tools, body.Tool) || manifest.ToolAudience(body.Tool) == pluginpkg.AudienceAgent {
		writeError(w, http.StatusNotFound, "tool not found")
		return
	}

	credential, _, err := s.MintUserPluginCredential(ctx, plugin.Name, user)
	if err != nil {
		writePluginMintError(w, err)
		return
	}

	result, err := s.getPluginMCPClient().CallTool(ctx, target, pluginmcp.CallOptions{Credential: credential}, body.Tool, body.Arguments)
	if err != nil {
		slog.Warn("plugin tool call failed", "plugin", plugin.Name, "tool", body.Tool, "error", err)
		writeError(w, http.StatusBadGateway, "plugin unreachable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// pluginManifest parses the manifest the operator published into
// status.manifest. Absent or unparsable means the plugin declares no surfaces.
func pluginManifest(plugin *connectorv1alpha1.Plugin) (*pluginpkg.Manifest, bool) {
	if plugin.Status.Manifest == nil || len(plugin.Status.Manifest.Raw) == 0 {
		return nil, false
	}
	var manifest pluginpkg.Manifest
	if err := json.Unmarshal(plugin.Status.Manifest.Raw, &manifest); err != nil {
		return nil, false
	}
	return &manifest, true
}

// pluginMCPTarget points the plugin MCP client at one plugin: an endpoint the
// hub verified, the path the manifest declares, and the manifest version the
// tool list is cached under.
func pluginMCPTarget(pluginID, endpoint string, manifest *pluginpkg.Manifest) pluginmcp.Target {
	return pluginmcp.Target{
		PluginID:        pluginID,
		Endpoint:        endpoint,
		Path:            manifest.MCP.Path,
		ManifestVersion: manifest.Version,
	}
}

// pluginToolIsKnown reports whether the plugin serves a tool under this name.
func pluginToolIsKnown(tools []mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// pluginToolInputSchema renders a tool's input schema as raw JSON, preferring the
// plugin's own schema object the way mcp-go does when it marshals a tool.
func pluginToolInputSchema(tool mcp.Tool) json.RawMessage {
	if len(tool.RawInputSchema) > 0 {
		return tool.RawInputSchema
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// writePluginMintError maps a credential-minting failure onto the status the
// plugin endpoints agree on: credentials that are switched off are a
// configuration problem, a plugin that stopped being usable is not found, and
// anything else is an internal error.
func writePluginMintError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugincreds.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "plugin credentials are not configured")
	case errors.Is(err, errPluginUnavailable):
		writeError(w, http.StatusNotFound, "plugin not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
