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

// UserTokenTools wraps the hub's /api/v1/user-tokens endpoints: the caller's own
// personal access tokens (`ainsel_…`).
//
// These are the caller's tokens, not an admin's view of everyone's: the hub
// scopes every route to the forwarded bearer. A token grants the same
// permissions as its owner, so revoking one is a security-relevant write and is
// a separate tool rather than a flag on a list.
//
// The secret itself exists in exactly one response — create_user_token. The hub
// stores only a hash, so list_user_tokens cannot and does not render it, and
// neither tool ever echoes a value back from a read path.
type UserTokenTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewUserTokenTools returns UserTokenTools pointed at hubURL, with a 10 s
// request timeout.
func NewUserTokenTools(hubURL string) *UserTokenTools {
	return &UserTokenTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *UserTokenTools) ListUserTokensTool() mcp.Tool {
	return mcp.NewTool("list_user_tokens",
		mcp.WithDescription("The calling user's personal access tokens: id, label, createdAt, lastUsedAt, expiresAt, revokedAt. Values are never listed — the hub stores only a hash. Results capped at 50 rows."),
	)
}

func (t *UserTokenTools) CreateUserTokenTool() mcp.Tool {
	return mcp.NewTool("create_user_token",
		mcp.WithDescription("Create a personal access token (`ainsel_…`) for the calling user. It grants the same permissions as that user, so give it a name that says what it is for. `expires_in_days` is optional; the hub applies its default lifetime when unset. The response carries the token value exactly once — it cannot be retrieved again, only revoked. Treat it as a secret: store it now and do not paste it into other tools' arguments or logs."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Label to recognise the token by")),
		mcp.WithNumber("expires_in_days", mcp.Description("Lifetime in days; the hub's default applies when unset")),
	)
}

func (t *UserTokenTools) RevokeUserTokenTool() mcp.Tool {
	return mcp.NewTool("revoke_user_token",
		mcp.WithDescription("Revoke one of the calling user's tokens by id (list_user_tokens gives the ids; names are not resolved). Anything already holding the value stops authenticating. The hub's 403/404 pass through unchanged."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Token id (e.g. ut-1a2b3c)")),
	)
}

func (t *UserTokenTools) ListUserTokens(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/user-tokens")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list user tokens: %v", err)), nil
	}
	body = capBareArray(body, 50, "more tokens available — revoke the ones you no longer use")
	return mcp.NewToolResultText(string(body)), nil
}

func (t *UserTokenTools) CreateUserToken(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	payload := map[string]any{"name": name}
	if v, ok := args["expires_in_days"].(float64); ok && v > 0 {
		payload["expiresInDays"] = int(v)
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	body, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/user-tokens", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create user token %q: %v", name, err)), nil
	}
	// The value is in this response and nowhere else, so it is passed through
	// verbatim — with a note that says so, and without any echo of the value.
	body = annotateJSON(body, map[string]any{
		"note": "token is shown once and cannot be retrieved again; revoke_user_token is the only way to undo it",
	})
	return mcp.NewToolResultText(string(body)), nil
}

func (t *UserTokenTools) RevokeUserToken(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := userToolsIDArg(req)
	if !ok {
		return mcp.NewToolResultError("id is required"), nil
	}
	if _, err := hubDelete(ctx, t.HTTPClient, t.HubURL, "/api/v1/user-tokens/"+url.PathEscape(id)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to revoke user token %s: %v", id, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("user token %s revoked", id)), nil
}
