package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// GroupTools wraps the hub's /api/v1/groups endpoints. A group is the
// access-control container resources belong to, and its id is what the
// groupId argument of create_agent, create_trigger and friends expects — so a
// session standing resources up from scratch needs these tools, not just the
// reads.
//
// Read + create only, by design (issue #326, PR-4): group update, delete and
// membership stay on the hub REST API, where the owner checks and the cascade
// semantics are documented. As with every hub-backed tool there is no
// MCP-side permission check — the forwarded bearer token and the hub's own
// authorization decide what the caller may do.
type GroupTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewGroupTools returns GroupTools pointed at hubURL, with a 10 s request
// timeout.
func NewGroupTools(hubURL string) *GroupTools {
	return &GroupTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *GroupTools) ListGroupsTool() mcp.Tool {
	return mcp.NewTool("list_groups",
		mcp.WithDescription("List the groups on the hub. A group is the access-control container agents, triggers, connectors and other resources belong to; its id is the value groupId arguments expect. Hubs running with access control disabled report no groups. Default limit: 50."),
		mcp.WithNumber("limit", mcp.Description("Maximum number of groups to return (default: 50)")),
	)
}

func (t *GroupTools) ListGroups(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	limit := defaultArgInt(args, "limit", 50)
	// The hub lists groups as a bare array and understands no pagination
	// parameters, so the cap is applied here rather than forwarded.
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/groups")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list groups: %v", err)), nil
	}
	body = capBareArray(body, limit, "more groups available — pass limit=N to fetch more")
	return mcp.NewToolResultText(string(body)), nil
}

func (t *GroupTools) GetGroupTool() mcp.Tool {
	return mcp.NewTool("get_group",
		mcp.WithDescription("One group by id, together with its members and roles, as {\"group\": {...}, \"members\": [{\"user\": ..., \"role\": ...}]}. Any authenticated caller may read a group; only an owner may change it."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Group id (as returned by list_groups or create_group)")),
	)
}

func (t *GroupTools) GetGroup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	id, _ := args["id"].(string)
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/groups/"+id)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get group %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (t *GroupTools) CreateGroupTool() mcp.Tool {
	return mcp.NewTool("create_group",
		mcp.WithDescription("Create a group. The calling user becomes its owner. The response carries the new group, whose id is what groupId arguments on create_agent, create_trigger and friends expect. Names are unique — the hub rejects a duplicate. Hubs with access control disabled refuse the request. Updating, deleting or managing membership of a group is not exposed over MCP; use the hub REST API."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique group name")),
		mcp.WithString("description", mcp.Description("Optional description of what belongs in the group")),
	)
}

func (t *GroupTools) CreateGroup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	// Presence-based, not zero-value based: an absent description is omitted
	// (the hub stores ""), a present one — including "" — is sent.
	payload := map[string]any{"name": name}
	if desc, ok := args["description"].(string); ok {
		payload["description"] = desc
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/groups", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create group: %v", err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}
