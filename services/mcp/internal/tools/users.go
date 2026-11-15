package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// UserTools wraps the hub's /api/v1/users endpoints: the user registry read
// surface (any authenticated caller) plus the locally-managed user writes
// (admin only).
//
// The hub's list endpoint answers a bare array and is not paginated, so
// list_users windows it MCP-side — the page/limit pair is applied here, never
// sent to the hub. Writes carry the caller's forwarded bearer, so the hub's
// admin check and its guards (no self-deletion, no self-disable, the last
// admin cannot be removed or demoted) apply exactly as they do over REST; the
// 403/404/409 pass through unchanged.
//
// A password is accepted on create and on update and is never rendered back:
// the hub's User schema carries no password field, and neither do these tools
// add one.
type UserTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewUserTools returns UserTools pointed at hubURL, with a 10 s request timeout.
func NewUserTools(hubURL string) *UserTools {
	return &UserTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (u *UserTools) ListUsersTool() mcp.Tool {
	return mcp.NewTool("list_users",
		mcp.WithDescription("The user registry: every user with username, email, displayName, roles, enabled state and who manages them (managedBy empty or \"user\" for locally-managed users, anything else for users an external identity provider owns). The hub's list is not paginated, so page/limit are applied here. Default limit: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1); applied MCP-side because the hub returns the full list")),
		mcp.WithNumber("limit", mcp.Description("Users per page (default: 50)")),
	)
}

func (u *UserTools) GetUserTool() mcp.Tool {
	return mcp.NewTool("get_user",
		mcp.WithDescription("One registry user by id: username, email, displayName, roles, enabled, mustChangePassword, managedBy, createdAt/updatedAt. Never a password or a token."),
		mcp.WithString("id", mcp.Required(), mcp.Description("User id (the registry id / subject)")),
	)
}

func (u *UserTools) CreateUserTool() mcp.Tool {
	return mcp.NewTool("create_user",
		mcp.WithDescription("Create a locally-managed user (admin only): username and password are required; email, displayName and roles are optional. The hub applies its password policy and rejects an existing username with 409. Users an external identity provider owns (managedBy) are not created or mutated here. The password is sent once and never returned."),
		mcp.WithString("username", mcp.Required(), mcp.Description("Login name")),
		mcp.WithString("password", mcp.Required(), mcp.Description("Initial password, subject to the hub's password policy. Sent once; no tool returns it.")),
		mcp.WithString("email", mcp.Description("Email address")),
		mcp.WithString("display_name", mcp.Description("User-facing name")),
		mcp.WithArray("roles", mcp.WithStringItems(), mcp.Description("Roles to grant (e.g. [\"admin\"])")),
	)
}

func (u *UserTools) UpdateUserTool() mcp.Tool {
	return mcp.NewTool("update_user",
		mcp.WithDescription("Partial update of a locally-managed user (admin only). Only the arguments you pass are sent; omitted fields keep their current values. `password` resets a local password and invalidates that user's session JWTs; `enabled: false` is refused with 409 for your own account and for the last admin. Roles are replaced wholesale, so pass the full set you want to keep."),
		mcp.WithString("id", mcp.Required(), mcp.Description("User id (the registry id / subject)")),
		mcp.WithString("email", mcp.Description("New email address")),
		mcp.WithString("display_name", mcp.Description("New user-facing name")),
		mcp.WithArray("roles", mcp.WithStringItems(), mcp.Description("Replacement role set (replaces the current roles)")),
		mcp.WithBoolean("enabled", mcp.Description("Enable (true) or disable (false) the account")),
		mcp.WithString("password", mcp.Description("New password (resets the local password and invalidates that user's session JWTs). Sent once; no tool returns it.")),
	)
}

func (u *UserTools) DeleteUserTool() mcp.Tool {
	return mcp.NewTool("delete_user",
		mcp.WithDescription("Delete a registry user (admin only). The hub refuses deleting your own account and the last admin, and refuses externally managed users (managedBy). Its 403/404/409 pass through unchanged."),
		mcp.WithString("id", mcp.Required(), mcp.Description("User id (the registry id / subject)")),
	)
}

func (u *UserTools) ListUsers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	page := defaultArgInt(args, "page", 1)
	limit := defaultArgInt(args, "limit", 50)
	body, err := hubGet(ctx, u.HTTPClient, u.HubURL, "/api/v1/users")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list users: %v", err)), nil
	}
	body = pageBareArray(body, page, limit, "more users available — pass page=N to fetch the next page")
	return mcp.NewToolResultText(string(body)), nil
}

func (u *UserTools) GetUser(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := userToolsIDArg(req)
	if !ok {
		return mcp.NewToolResultError("id is required"), nil
	}
	body, err := hubGet(ctx, u.HTTPClient, u.HubURL, "/api/v1/users/"+url.PathEscape(id))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get user %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (u *UserTools) CreateUser(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	username, _ := args["username"].(string)
	password, _ := args["password"].(string)
	if username == "" || password == "" {
		return mcp.NewToolResultError("username and password are required"), nil
	}
	payload := map[string]any{"username": username, "password": password}
	if v, ok := args["email"].(string); ok && v != "" {
		payload["email"] = v
	}
	if v, ok := args["display_name"].(string); ok && v != "" {
		payload["displayName"] = v
	}
	if roles, ok := stringSliceArg(args, "roles"); ok {
		payload["roles"] = roles
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	body, err := hubPost(ctx, u.HTTPClient, u.HubURL, "/api/v1/users", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create user %s: %v", username, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (u *UserTools) UpdateUser(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	id, _ := args["id"].(string)
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	payload := map[string]any{}
	if v, ok := args["email"].(string); ok && v != "" {
		payload["email"] = v
	}
	if v, ok := args["display_name"].(string); ok && v != "" {
		payload["displayName"] = v
	}
	if roles, ok := stringSliceArg(args, "roles"); ok {
		payload["roles"] = roles
	}
	if v, ok := args["enabled"].(bool); ok {
		payload["enabled"] = v
	}
	if v, ok := args["password"].(string); ok && v != "" {
		payload["password"] = v
	}
	if len(payload) == 0 {
		return mcp.NewToolResultError("at least one of email, display_name, roles, enabled or password must be provided"), nil
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	body, err := hubPatch(ctx, u.HTTPClient, u.HubURL, "/api/v1/users/"+url.PathEscape(id), bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update user %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (u *UserTools) DeleteUser(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := userToolsIDArg(req)
	if !ok {
		return mcp.NewToolResultError("id is required"), nil
	}
	if _, err := hubDelete(ctx, u.HTTPClient, u.HubURL, "/api/v1/users/"+url.PathEscape(id)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete user %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("user %s deleted", id)), nil
}

// userToolsIDArg reads the required user/token id shared by the user and
// user-token tools.
func userToolsIDArg(req mcp.CallToolRequest) (string, bool) {
	args, _ := req.Params.Arguments.(map[string]any)
	v, _ := args["id"].(string)
	return v, v != ""
}
