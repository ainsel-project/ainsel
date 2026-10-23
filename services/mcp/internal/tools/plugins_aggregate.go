package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// DefaultPluginToolsPollInterval is how often the aggregator re-reads the hub's
// operator plugin tool list when PLUGIN_TOOLS_POLL_INTERVAL is unset or invalid.
const DefaultPluginToolsPollInterval = 15 * time.Second

const (
	// pluginToolsListPath is the hub's internal list of operator-facing plugin
	// tools (X-Internal-Token). It already excludes agent-audience tools and
	// plugins that are not enabled and Ready.
	pluginToolsListPath = "/api/internal/plugin-tools"
	// pluginToolsCallPath is the hub's user-authenticated call endpoint. It is
	// called with the caller's own bearer, exactly like every other tool here,
	// so the hub can attach that user's plugin credential.
	pluginToolsCallPath = "/api/v1/plugin-tools/call"

	// emptyPluginToolSchema is the input schema registered for a plugin tool the
	// hub described without one, so the tool never shows up schema-less.
	emptyPluginToolSchema = `{"type":"object"}`
)

// pluginToolEntry is one tool of GET /api/internal/plugin-tools.
type pluginToolEntry struct {
	PluginID    string          `json:"pluginId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// pluginToolsListResponse is the body of GET /api/internal/plugin-tools.
type pluginToolsListResponse struct {
	Tools []pluginToolEntry `json:"tools"`
}

// pluginToolCallRequest is the body of POST /api/v1/plugin-tools/call. Arguments
// is always sent, even when the caller passed none, so the hub sees one shape.
type pluginToolCallRequest struct {
	PluginID  string         `json:"pluginId"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// pluginToolTarget is what a registered plugin tool stands for: which plugin and
// which of its own tools a call routes to, plus the description and schema the
// hub last reported (the diff basis for "did this tool change?").
type pluginToolTarget struct {
	pluginID    string
	tool        string
	description string
	schema      string
}

// PluginToolAggregator keeps the plugin tools of a running AInsel install on
// AInsel's own MCP server. The hub is the only MCP client of a plugin, so this
// aggregator never talks to a plugin: it polls the hub for the operator tool
// list, registers each tool under <pluginId>_<toolName>, and answers a call by
// handing it back to the hub with the caller's bearer.
//
// Audience is decided by the hub alone, so whatever the list contains is what
// gets registered — agent-audience tools are already gone from it.
type PluginToolAggregator struct {
	hubURL        string
	internalToken string
	mcpServer     *server.MCPServer
	builtin       map[string]bool
	interval      time.Duration
	client        *http.Client

	mu         sync.Mutex
	registered map[string]pluginToolTarget
}

// NewPluginToolAggregator builds an aggregator for s. builtin is the set of
// built-in tool names (derived from s.ListTools() at startup) that a plugin tool
// must never shadow; a collision is skipped and logged. interval <= 0 falls back
// to DefaultPluginToolsPollInterval.
func NewPluginToolAggregator(hubURL, internalToken string, s *server.MCPServer, builtin map[string]bool, interval time.Duration) *PluginToolAggregator {
	if interval <= 0 {
		interval = DefaultPluginToolsPollInterval
	}
	if builtin == nil {
		builtin = map[string]bool{}
	}
	return &PluginToolAggregator{
		hubURL:        hubURL,
		internalToken: internalToken,
		mcpServer:     s,
		builtin:       builtin,
		interval:      interval,
		client:        &http.Client{Timeout: 15 * time.Second},
		registered:    map[string]pluginToolTarget{},
	}
}

// Run polls the hub once immediately and then every interval until ctx is done.
// Without an internal token the hub's list endpoint is unreachable, so the
// aggregator stays off: a server without plugin tools is a working server, and
// failing startup over it would take the whole MCP surface down.
func (a *PluginToolAggregator) Run(ctx context.Context) {
	if a.internalToken == "" {
		slog.Info("plugin tools disabled: no internal token")
		return
	}
	for {
		if err := a.Sync(ctx); err != nil {
			slog.Warn("plugin tool sync failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.interval):
		}
	}
}

// Sync performs one poll: tools that appeared or changed (description or input
// schema) are added, tools that disappeared are deleted. A failed poll returns
// an error and leaves the current tool set untouched, so one hub hiccup does not
// empty the tool list for everyone.
func (a *PluginToolAggregator) Sync(ctx context.Context) error {
	entries, err := a.fetchPluginTools(ctx)
	if err != nil {
		return err
	}

	desired := make(map[string]pluginToolTarget, len(entries))
	for _, entry := range entries {
		name := pluginToolPrefixedName(entry.PluginID, entry.Name)
		if a.builtin[name] {
			slog.Warn("plugin tool name collides with a built-in tool",
				"tool", name, "plugin", entry.PluginID, "pluginTool", entry.Name)
			continue
		}
		if prev, taken := desired[name]; taken {
			slog.Warn("plugin tool name collides with another plugin tool",
				"tool", name, "plugin", entry.PluginID, "pluginTool", entry.Name,
				"kept", prev.pluginID+"/"+prev.tool)
			continue
		}
		desired[name] = pluginToolTarget{
			pluginID:    entry.PluginID,
			tool:        entry.Name,
			description: entry.Description,
			schema:      pluginToolSchemaJSON(entry.InputSchema),
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	adds := make([]server.ServerTool, 0, len(desired))
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		target := desired[name]
		if cur, ok := a.registered[name]; ok && cur == target {
			continue
		}
		adds = append(adds, server.ServerTool{
			Tool:    mcp.NewToolWithRawSchema(name, target.description, []byte(target.schema)),
			Handler: a.callHandler(target.pluginID, target.tool),
		})
	}

	var gone []string
	for name := range a.registered {
		if _, ok := desired[name]; !ok {
			gone = append(gone, name)
		}
	}
	slices.Sort(gone)

	// Re-adding an existing name replaces it in the registry, so changed tools
	// are overwritten in place and gone tools are removed after the additions,
	// which keeps the list-change notifications in one consistent state.
	if len(adds) > 0 {
		a.mcpServer.AddTools(adds...)
	}
	if len(gone) > 0 {
		a.mcpServer.DeleteTools(gone...)
	}
	a.registered = desired
	return nil
}

// callHandler routes one tool call back to the hub. The caller's bearer comes
// from the request context, the same way every other tool here forwards it, so
// the hub can attribute the call to that user (and attach their plugin
// credential). The hub's CallToolResult is returned unchanged — a tool-level
// error stays inside it as isError.
func (a *PluginToolAggregator) callHandler(pluginID, toolName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload, err := json.Marshal(pluginToolCallRequest{
			PluginID:  pluginID,
			Tool:      toolName,
			Arguments: req.GetArguments(),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to call plugin tool %s/%s: %v", pluginID, toolName, err)), nil
		}
		body, err := hubPost(ctx, a.client, a.hubURL, pluginToolsCallPath, bytes.NewReader(payload))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to call plugin tool %s/%s: %v", pluginID, toolName, err)), nil
		}
		var result mcp.CallToolResult
		if err := json.Unmarshal(body, &result); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("plugin tool %s/%s returned an invalid result: %v", pluginID, toolName, err)), nil
		}
		return &result, nil
	}
}

// fetchPluginTools reads the hub's operator plugin tool list. Only this request
// carries X-Internal-Token; the call path uses the user's bearer instead.
func (a *PluginToolAggregator) fetchPluginTools(ctx context.Context) ([]pluginToolEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.hubURL+pluginToolsListPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", a.internalToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin tool list request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("plugin tool list read failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub returned %d: %s", resp.StatusCode, string(body))
	}
	var list pluginToolsListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("plugin tool list is not valid JSON: %w", err)
	}
	return list.Tools, nil
}

// pluginToolPrefixedName is the name a plugin tool is registered under: the
// plugin id with its dashes turned into underscores, plus the plugin's own tool
// name. Underscores because the separator has to be unambiguous inside a name
// that MCP allows dashes in.
func pluginToolPrefixedName(pluginID, toolName string) string {
	return strings.ReplaceAll(pluginID, "-", "_") + "_" + toolName
}

// pluginToolSchemaJSON normalises the input schema bytes for both comparison and
// registration: identical bytes mean "no change", and a schema the hub omitted
// becomes a plain object schema so the tool is never registered schema-less.
func pluginToolSchemaJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return emptyPluginToolSchema
	}
	return string(raw)
}
