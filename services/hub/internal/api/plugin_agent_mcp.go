package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginproxy"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The plugin tool surface for agents. An agent's runtime lists its configured
// plugins as MCP servers, all pointing at this hub endpoint, so that no plugin
// URL, credential or secret ever lands in the agent pod: the hub opens the MCP
// session to the plugin itself, lists the tools the agent may see, and runs each
// call with a freshly minted agent-kind plugin credential attached.
//
// This is deliberately separate from the operator surface
// (GET /api/internal/plugin-tools and POST /api/v1/plugin-tools/call): the two
// audiences never share an endpoint, so an agent can neither see nor reach an
// operator tool no matter what it asks for.

// agentNamePattern is the DNS-label-shaped agent name the agent runtime puts in
// X-Ainsel-Agent. It is validated here rather than trusted, because the name
// becomes the subject of the credential the plugin sees.
var agentNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// agentMCPCaller is the identity an agent runtime declares on this endpoint.
type agentMCPCaller struct {
	agentName    string
	invocationID string
}

// handlePluginAgentMCP serves POST /api/internal/plugins/<id>/agent-mcp: the MCP
// endpoint of one plugin, as seen by an agent. It is an MCP server in its own
// right — the agent runtime talks MCP to the hub — and a client of the plugin
// behind it.
//
// Auth is the hub's own internal bearer secret, plus X-Ainsel-Agent naming the
// calling agent. The credential the plugin sees is never the caller's: it is
// minted per tool call as kind "agent", subject "agent:<name>", carrying the
// invocation id so the plugin can bind it back to the running invocation.
//
// Status codes: no internal secret configured → 503; missing or wrong bearer →
// 401; missing or malformed agent name → 400; plugin missing, disabled, not
// Ready, or without an MCP surface → 404; plugin unreachable → 502.
func (s *Server) handlePluginAgentMCP(w http.ResponseWriter, r *http.Request, pluginID string) {
	if s.internalValidateSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "internal endpoints disabled")
		return
	}
	provided, ok := internalBearerToken(r.Header.Get("Authorization"))
	if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(s.internalValidateSecret)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	caller, ok := agentMCPCallerFrom(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing or invalid X-Ainsel-Agent")
		return
	}

	ctx := r.Context()

	// An unavailable plugin answers 404 before any MCP session is built, so a
	// disabled plugin is indistinguishable from one that was never installed.
	plugin, err := s.availablePlugin(ctx, pluginID)
	if err != nil {
		if errors.Is(err, errPluginUnavailable) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		slog.Error("agent plugin mcp: plugin lookup failed", "plugin", pluginID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	manifest, ok := pluginManifest(plugin)
	if !ok || manifest.MCP == nil {
		// A plugin that declares no MCP surface serves no tools to anyone.
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	endpoint, ok := pluginproxy.PickEndpoint(plugin.Status.VerifiedEndpoints, nil)
	if !ok {
		writeError(w, http.StatusBadGateway, "plugin unreachable")
		return
	}
	target := pluginMCPTarget(plugin.Name, endpoint, manifest)

	// The tool list is the plugin's own answer, read as the plugin's background
	// identity: a list is not an action on behalf of the agent that happened to
	// ask for it.
	listCredential, _, err := s.MintPluginBackgroundCredential(ctx, plugin.Name)
	if err != nil {
		writePluginMintError(w, err)
		return
	}
	tools, err := s.getPluginMCPClient().ListTools(ctx, target, listCredential)
	if err != nil {
		slog.Warn("agent plugin mcp: plugin unreachable", "plugin", plugin.Name, "error", err)
		writeError(w, http.StatusBadGateway, "plugin unreachable")
		return
	}

	srv := server.NewMCPServer("ainsel-plugin-"+plugin.Name, "1", server.WithToolCapabilities(false))
	for _, tool := range tools {
		audience := manifest.ToolAudience(tool.Name)
		if audience != pluginpkg.AudienceAgent && audience != pluginpkg.AudienceBoth {
			continue
		}
		srv.AddTool(
			mcp.NewToolWithRawSchema(tool.Name, tool.Description, pluginToolInputSchema(tool)),
			s.agentPluginToolHandler(plugin.Name, tool.Name, target, caller),
		)
	}

	server.NewStreamableHTTPServer(srv, server.WithStateLess(true)).ServeHTTP(w, r)
}

// agentPluginToolHandler runs one tool call on a plugin as the agent that asked
// for it. The credential is minted per call — never reused across calls or
// agents — so a long-lived MCP session cannot outlive an agent's access.
func (s *Server) agentPluginToolHandler(pluginID, toolName string, target pluginmcp.Target, caller agentMCPCaller) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		credential, _, err := s.MintAgentPluginCredential(ctx, pluginID, caller.agentName, caller.invocationID)
		if err != nil {
			slog.Warn("agent plugin mcp: mint failed", "plugin", pluginID, "agent", caller.agentName, "error", err)
			// The agent asked for a tool; the failure is about that call, so it
			// belongs in the result rather than in a transport-level error.
			return mcp.NewToolResultError("plugin credential unavailable"), nil
		}

		result, err := s.getPluginMCPClient().CallTool(ctx, target, pluginmcp.CallOptions{
			Credential:   credential,
			InvocationID: caller.invocationID,
		}, toolName, req.GetArguments())
		if err != nil {
			slog.Warn("agent plugin mcp: call failed", "plugin", pluginID, "tool", toolName, "error", err)
			return mcp.NewToolResultError("plugin unreachable"), nil
		}
		return result, nil
	}
}

// agentMCPCallerFrom reads the caller an agent runtime declares. X-Ainsel-Agent
// is required and must be a well-formed agent name; X-Ainsel-Invocation-Id is
// optional and reaches the plugin credential as-is.
func agentMCPCallerFrom(r *http.Request) (agentMCPCaller, bool) {
	agentName := r.Header.Get("X-Ainsel-Agent")
	if !agentNamePattern.MatchString(agentName) {
		return agentMCPCaller{}, false
	}
	return agentMCPCaller{agentName: agentName, invocationID: r.Header.Get("X-Ainsel-Invocation-Id")}, true
}

// internalBearerToken extracts the token from an "Authorization: Bearer <t>"
// header, tolerating a case-insensitive scheme. ok is false when the header is
// absent, not a bearer header, or carries an empty token.
func internalBearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
