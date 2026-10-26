// Package tools implements the MCP tools the service exposes. Each tool
// group (agents, channels, plugins, triggers, ...) is a small struct
// constructed with a hub URL and calls the hub's HTTP API to serve its
// tools; errors.go maps hub errors to MCP tool errors.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ActivityTools rolls up the hub's invocation history into a per-agent
// summary over a configurable window. The default window is the last 24
// hours. Latency aggregation (p50/p95) is intentionally not computed here:
// the hub does not yet expose those quantiles on invocation aggregates and
// the design spec defers any Prometheus fallback to a later project (C).
type ActivityTools struct {
	HubURL     string
	HTTPClient *http.Client
}

func NewActivityTools(hubURL string) *ActivityTools {
	return &ActivityTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *ActivityTools) SummarizeAgentActivityTool() mcp.Tool {
	return mcp.NewTool("summarize_agent_activity",
		mcp.WithDescription("Per-agent rollup of recent activity: invocation count by status and most-recent errors. Default window: last 24 hours. Token usage and cost are not on invocation records yet; use get_token_usage."),
		mcp.WithString("since", mcp.Description("Earliest timestamp (RFC 3339). Defaults to 24 hours ago.")),
		mcp.WithString("until", mcp.Description("Latest timestamp (RFC 3339). Defaults to now.")),
	)
}

// activityInvocationCap is the maximum number of invocations fetched for
// activity summarization. This prevents unbounded queries for very busy
// time windows.
const activityInvocationCap = 1000

type agentActivityStats struct {
	Agent       string         `json:"agent"`
	Invocations map[string]int `json:"invocations"`
	// RecentErrors carries the error message of the newest failing
	// invocations, capped at five per agent.
	RecentErrors []map[string]any `json:"recentErrors,omitempty"`
}

// invocationStatusBuckets are the status keys every rollup reports. They are
// the hub's invocation vocabulary, taken from the status constants in
// services/hub/internal/invocations/types.go. Any other status the hub starts
// emitting is still counted under its own name rather than dropped, so a hub
// change cannot silently empty a bucket again.
var invocationStatusBuckets = []string{"queued", "running", "success", "failure", "timeout"}

func newInvocationCounts() map[string]int {
	counts := map[string]int{"total": 0}
	for _, s := range invocationStatusBuckets {
		counts[s] = 0
	}
	return counts
}

func (a *ActivityTools) SummarizeAgentActivity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	until := now
	if v, ok := args["since"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	if v, ok := args["until"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t
		}
	}

	q := url.Values{}
	q.Set("since", since.Format(time.RFC3339))
	q.Set("until", until.Format(time.RFC3339))
	q.Set("limit", strconv.Itoa(activityInvocationCap))
	body, err := hubGet(ctx, a.HTTPClient, a.HubURL, "/api/v1/invocations?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list invocations: %v", err)), nil
	}

	invocations, err := decodeInvocations(body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse invocations: %v", err)), nil
	}
	invocationsCapped := len(invocations) >= activityInvocationCap

	byAgent := map[string]*agentActivityStats{}
	for _, inv := range invocations {
		// The hub emits agentName (see the JSON tags on
		// invocations.Invocation). Reading a key it never emits used to skip
		// every row here and return a silently empty rollup.
		name, _ := inv["agentName"].(string)
		if name == "" {
			continue
		}
		s, ok := byAgent[name]
		if !ok {
			s = &agentActivityStats{
				Agent:       name,
				Invocations: newInvocationCounts(),
			}
			byAgent[name] = s
		}
		s.Invocations["total"]++
		status, _ := inv["status"].(string)
		if status == "" {
			status = "unknown"
		}
		s.Invocations[status]++
		if errMsg, ok := inv["error"].(string); ok && errMsg != "" && len(s.RecentErrors) < 5 {
			s.RecentErrors = append(s.RecentErrors, map[string]any{
				"invocationId": inv["id"],
				"summary":      errMsg,
			})
		}
	}

	keys := make([]string, 0, len(byAgent))
	for k := range byAgent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	agentsOut := make([]*agentActivityStats, 0, len(keys))
	for _, k := range keys {
		agentsOut = append(agentsOut, byAgent[k])
	}

	out := map[string]any{
		"window": map[string]string{
			"since": since.Format(time.RFC3339),
			"until": until.Format(time.RFC3339),
		},
		"agents":  agentsOut,
		"latency": nil,
		"_note": "latency aggregation (p50/p95) not yet implemented on the hub; this field is intentionally null. " +
			"Token usage and cost are not fields of an invocation record, so they are not rolled up here; " +
			"use get_token_usage for token counts.",
	}
	if invocationsCapped {
		out["invocationsCapped"] = true
		out["invocationsCapNote"] = fmt.Sprintf("underlying fetch was capped at %d invocations; summary may be incomplete for very busy windows", activityInvocationCap)
	}
	body, err = json.MarshalIndent(out, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal output: %v", err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// decodeInvocations accepts both the hub's wrapper response
// ({"invocations":[...], "total":...}) and a bare array. The hub currently
// returns the wrapper; we accept either form so the tool keeps working if the
// hub flattens the shape later. Empty or null invocations is treated as an
// empty result set (see decodeWrapped).
func decodeInvocations(body []byte) ([]map[string]any, error) {
	return decodeWrapped(body, "invocations")
}
