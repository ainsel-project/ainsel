package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// MCPServerTools wraps the hub's /api/v1/mcp-servers endpoints. The MCP-server
// registry lists every MCP endpoint an agent is allowed to call out to; this
// tool surfaces that catalogue to an AI client. A caller-supplied name is
// always interpolated into a path through url.PathEscape, matching channels.go
// and plugins.go, so a tool argument can never address a row other than the
// one it names.
type MCPServerTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewMCPServerTools returns MCPServerTools pointed at hubURL, with a 10 s
// request timeout.
func NewMCPServerTools(hubURL string) *MCPServerTools {
	return &MCPServerTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *MCPServerTools) ListMCPServersTool() mcp.Tool {
	return mcp.NewTool("list_mcp_servers",
		mcp.WithDescription("List the MCP servers registered in the platform's registry, including their URLs and the tools each one exposes. Default page size: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1)")),
		mcp.WithNumber("pageSize", mcp.Description("Page size (default: 50)")),
	)
}

func (t *MCPServerTools) GetMCPServerTool() mcp.Tool {
	return mcp.NewTool("get_mcp_server",
		mcp.WithDescription("Get detail about one MCP server registry entry: URL, transport, tool names, and any auth config (non-secret fields)."),
		mcp.WithString("name", mcp.Required(), mcp.Description("MCP server name")),
	)
}

func (t *MCPServerTools) ListMCPServers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	q := url.Values{}
	q.Set("page", strconv.Itoa(defaultArgInt(args, "page", 1)))
	q.Set("pageSize", strconv.Itoa(defaultArgInt(args, "pageSize", 50)))
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list MCP servers: %v", err)), nil
	}
	body = annotatePageMeta(body, "more MCP servers available — pass page=N to fetch the next page")
	return mcp.NewToolResultText(string(body)), nil
}

func (t *MCPServerTools) GetMCPServer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := req.Params.Arguments.(map[string]any)["name"].(string)
	if !ok || name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers/"+url.PathEscape(name))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get MCP server %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// mcpServerRow is the registry row as the hub's read shape returns it. Only the
// writable fields are decoded; createdAt/updatedAt/managedBy are read-only and
// are never sent back.
type mcpServerRow struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv"`
}

// currentMCPServer reads the row the hub currently holds. update_mcp_server
// needs it because the hub's PUT is a full replace, not a patch: it assigns
// displayName, description, url and tokenFromEnv from the request body
// unconditionally, so a body that omits a field clears it. Reading first and
// resending what the caller did not change is what makes "omitted arguments are
// left unchanged" true here — the same read-then-merge the agent-image env
// updates already do for the same reason.
func (t *MCPServerTools) currentMCPServer(ctx context.Context, name string) (*mcpServerRow, error) {
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers/"+url.PathEscape(name))
	if err != nil {
		return nil, err
	}
	var row mcpServerRow
	if err := json.Unmarshal(body, &row); err != nil {
		return nil, err
	}
	return &row, nil
}

func (t *MCPServerTools) CreateMCPServerTool() mcp.Tool {
	return mcp.NewTool("create_mcp_server",
		mcp.WithDescription("Register an MCP endpoint in the platform's registry — the catalogue agents may connect to. Registering writes metadata only; it never deploys or starts anything. Two properties are not validated here and break agent pods later if they are wrong, so check both first: (1) url must be reachable from inside the cluster, because agent pods dial it directly — a public-only, ingress-blocked or localhost URL is accepted by this call and fails at runtime. (2) tokenFromEnv names an environment variable whose value is sent to that endpoint as the bearer token; the registry stores the name and never the value, and the value has to exist on the AgentImage the agent runs from (or on the agent's own env) — the agent operator skips a token whose variable is undefined, so the agent then connects anonymously and the endpoint rejects it. Omit tokenFromEnv for an endpoint that takes no auth; the hub validates the variable-name shape (^[A-Z_][A-Z0-9_]*$, at most 64 characters). Requires a groupId the caller has write access to on hubs with access control enabled."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Registry name (e.g. forgejo). Agents select the row by this name, and it is the URL segment for get/update/delete. Immutable: the hub's PUT cannot rename a row, so a rename means create + update the agents and delete the old row.")),
		mcp.WithString("url", mcp.Required(), mcp.Description("MCP endpoint URL, reachable from inside the cluster (e.g. http://mcp-github.platform.svc.cluster.local:8080/mcp)")),
		mcp.WithString("display_name", mcp.Description("User-facing label for the registry row")),
		mcp.WithString("description", mcp.Description("One line on what the endpoint offers, shown in the console")),
		mcp.WithString("token_from_env", mcp.Description("Name of the environment variable whose value is sent as the bearer token (e.g. GITHUB_MCP_TOKEN). The registry stores the name only, never a secret value. Must name a variable the AgentImage actually defines, or the token is skipped and the endpoint gets an anonymous call. Omit for an anonymous endpoint.")),
		mcp.WithString("groupId", mcp.Description("Group to assign the registry row to; must be a group the caller has write access to. Required on hubs with access control enabled.")),
	)
}

func (t *MCPServerTools) CreateMCPServer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	serverURL, _ := args["url"].(string)
	if strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	// The hub only checks url on create; an empty one would be stored and then
	// fail every attach, so reject it here rather than write a dead row.
	if strings.TrimSpace(serverURL) == "" {
		return mcp.NewToolResultError("url is required"), nil
	}

	payload := map[string]any{"name": name, "url": serverURL}
	if v, ok := args["display_name"].(string); ok {
		payload["displayName"] = v
	}
	if v, ok := args["description"].(string); ok {
		payload["description"] = v
	}
	if v, ok := args["token_from_env"].(string); ok {
		payload["tokenFromEnv"] = v
	}
	if v, _ := args["groupId"].(string); v != "" {
		payload["groupId"] = v
	}

	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create MCP server: %v", appendGroupIDHint(err))), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

func (t *MCPServerTools) UpdateMCPServerTool() mcp.Tool {
	return mcp.NewTool("update_mcp_server",
		mcp.WithDescription("Update an MCP server registry row. Omitted arguments are left unchanged; a present argument is applied, and an empty description or token_from_env clears it. The hub's PUT replaces the whole row rather than patching it, so this tool reads the current row first and resends the fields you did not pass — that read is also where a 404 or 403 on the row surfaces. url cannot be cleared (an empty one would be stored and break every agent attaching the row); send the replacement. The row name is the key and cannot be renamed. Rows owned by an external author (managedBy — helm, a plugin) are refused with 409. Changing url or tokenFromEnv re-points every agent that attaches the row, because the agent CR snapshots the registry at write time and only picks up the new value on its next refresh."),
		mcp.WithString("name", mcp.Required(), mcp.Description("MCP server registry name (e.g. forgejo) — exact match, no fuzzy lookup")),
		mcp.WithString("display_name", mcp.Description("New user-facing label; \"\" clears it")),
		mcp.WithString("description", mcp.Description("New one-line description; \"\" clears it")),
		mcp.WithString("url", mcp.Description("New MCP endpoint URL, reachable from inside the cluster — agent pods dial it directly. Cannot be cleared.")),
		mcp.WithString("token_from_env", mcp.Description("New environment variable name whose value is sent as the bearer token (^[A-Z_][A-Z0-9_]*$, at most 64 characters), or \"\" to make the endpoint anonymous. The registry holds the name only; the value must be defined on the AgentImage the agent runs from.")),
	)
}

func (t *MCPServerTools) UpdateMCPServer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	sent := false
	for _, key := range []string{"display_name", "description", "url", "token_from_env"} {
		if _, ok := args[key]; ok {
			sent = true
			break
		}
	}
	if !sent {
		return mcp.NewToolResultError("at least one field must be provided (display_name, description, url, token_from_env); omit arguments to leave those fields unchanged"), nil
	}

	current, err := t.currentMCPServer(ctx, name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get MCP server %s: %v", name, err)), nil
	}

	// Start from the stored row and overwrite only what the caller passed, so
	// the full-replace PUT cannot blank a field the caller never mentioned.
	displayName, description, serverURL, tokenFromEnv := current.DisplayName, current.Description, current.URL, current.TokenFromEnv
	if v, ok := args["display_name"].(string); ok {
		displayName = v
	}
	if v, ok := args["description"].(string); ok {
		description = v
	}
	if v, ok := args["url"].(string); ok {
		if strings.TrimSpace(v) == "" {
			return mcp.NewToolResultError("url cannot be cleared: the hub stores it verbatim and an empty URL breaks every agent that attaches this server — send the replacement URL"), nil
		}
		serverURL = v
	}
	if v, ok := args["token_from_env"].(string); ok {
		// Empty is a real change here: the hub documents PUT as a full replace,
		// so "" is how a caller makes an endpoint anonymous again.
		tokenFromEnv = v
	}

	payload := map[string]any{
		"name":         current.Name,
		"displayName":  displayName,
		"description":  description,
		"url":          serverURL,
		"tokenFromEnv": tokenFromEnv,
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPut(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers/"+url.PathEscape(name), bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update MCP server %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

func (t *MCPServerTools) DeleteMCPServerTool() mcp.Tool {
	return mcp.NewTool("delete_mcp_server",
		mcp.WithDescription("Delete an MCP server registry row. This is final: the row is gone and any agent still selecting it loses the endpoint on its next refresh. The hub refuses while an AgentImage attaches the row, with the blocking image names in the 409 body (affectedImages) — detach them from the image first rather than deleting around the refusal. Rows owned by an external author (managedBy — helm, a plugin) are refused with 409. The hub's 404/403 pass through unchanged."),
		mcp.WithString("name", mcp.Required(), mcp.Description("MCP server registry name (e.g. forgejo) — exact match, no fuzzy lookup")),
	)
}

func (t *MCPServerTools) DeleteMCPServer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	if _, err := hubDelete(ctx, t.HTTPClient, t.HubURL, "/api/v1/mcp-servers/"+url.PathEscape(name)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete MCP server %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("MCP server %s deleted", name)), nil
}
