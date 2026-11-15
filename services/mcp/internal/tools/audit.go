package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuditTools wraps the hub's /api/v1/audit/auth-events endpoint: the persisted
// authz audit trail (admin only).
//
// Rows carry no secrets — the hub's write path never sees a password or a token
// value — so the whole list is safe to render. In `oidc` mode only the three
// user-management event types appear, because OIDC logins complete at the
// issuer and never reach the hub backend; an empty result there is expected, not
// a broken filter.
//
// The endpoint is not SQL-paginated yet (issue #269), so the hub's own page and
// pageSize are passed through with a 50-row default rather than being lifted.
type AuditTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewAuditTools returns AuditTools pointed at hubURL, with a 10 s request timeout.
func NewAuditTools(hubURL string) *AuditTools {
	return &AuditTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *AuditTools) ListAuthEventsTool() mcp.Tool {
	return mcp.NewTool("list_auth_events",
		mcp.WithDescription("The authz audit trail, newest first: occurredAt, actorSub, eventType, target, sourceIP, outcome. Admin-only; rows contain no secrets. In OIDC mode only user-management events appear, because logins complete at the issuer. Default pageSize: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1)")),
		mcp.WithNumber("pageSize", mcp.Description("Rows per page (default: 50)")),
		mcp.WithString("user", mcp.Description("Match on actorSub or target")),
		mcp.WithArray("type", mcp.WithStringItems(), mcp.Description("Event type(s), exact match; several values are OR-ed")),
		mcp.WithString("from", mcp.Description("RFC3339 inclusive lower bound on occurredAt")),
		mcp.WithString("to", mcp.Description("RFC3339 exclusive upper bound on occurredAt")),
	)
}

func (a *AuditTools) ListAuthEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	q := url.Values{}
	q.Set("page", strconv.Itoa(defaultArgInt(args, "page", 1)))
	q.Set("pageSize", strconv.Itoa(defaultArgInt(args, "pageSize", 50)))
	for _, k := range []string{"user", "from", "to"} {
		if v, ok := args[k].(string); ok && v != "" {
			q.Set(k, v)
		}
	}
	if types, ok := stringSliceArg(args, "type"); ok {
		for _, t := range types {
			if t != "" {
				q.Add("type", t)
			}
		}
	} else if v, ok := args["type"].(string); ok && v != "" {
		q.Set("type", v)
	}
	body, err := hubGet(ctx, a.HTTPClient, a.HubURL, "/api/v1/audit/auth-events?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list auth events: %v", err)), nil
	}
	body = annotatePageMeta(body, "more auth events available — pass page=N for the next page, or narrow with from/to")
	return mcp.NewToolResultText(string(body)), nil
}
