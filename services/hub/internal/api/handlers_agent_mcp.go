package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	"github.com/ainsel-project/ainsel/services/hub/internal/telemetry"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentMCPPath serves the platform's own read-only tool plane to agent
// runtimes.
//
// It lives under /api/internal/ on purpose, for the two reasons the skill
// catalogue documents at skillMCPPath: the hub publishes only /api/v1/* through
// its ingress, and serveHTTPInner runs the user-auth middleware only for
// /api/v1/* paths. So this endpoint is unreachable from outside the cluster and
// is not reachable by a user session either.
//
// Its credential is the cluster-internal bearer plus X-Ainsel-Agent naming the
// calling agent — exactly the contract the plugin agent endpoints
// (handlePluginAgentMCP) already have, and for the same reason: the row the
// operator's renderer turns into MCP_SERVER_TOKENS references
// HUB_INTERNAL_VALIDATE_SECRET, the one credential the operator injects into
// every agent container. Unlike the catalogue, which guards a human-curated
// skill list with its own token, this endpoint guards a read plane over data
// every agent in the cluster can already see the existence of, and it reuses the
// plugin plane's caller identity rather than minting a third one.
const AgentMCPPath = "/api/internal/agent-mcp"

// agentMCPServerName is the MCP identity an agent sees when it lists this
// server's tools. It names the platform rather than an integrator because the
// tool set is the platform's own.
const agentMCPServerName = "ainsel-platform"

// Bounds on one tool response. Every figure here exists because a tool result
// lands in a model's context: an unbounded agent list, log page or series is a
// correct answer that costs more than the question was worth.
const (
	agentMCPDefaultLimit = 25
	agentMCPMaxLimit     = 100
	// agentMCPMaxWindowHours matches the retention of the records these tools
	// read (tasklogs.ConversationRetention, invocations.Retention): asking for
	// more than the hub still keeps is not a deeper answer.
	agentMCPMaxWindowHours = 7 * 24
	agentMCPDefaultHours   = 24
)

// handleAgentMCP serves POST /api/internal/agent-mcp: the platform's read-only
// tools, as seen by an agent.
//
// Auth is the hub's own internal bearer secret plus X-Ainsel-Agent naming the
// calling agent, both checked exactly as handlePluginAgentMCP checks them —
// including the status-code split, because "the feature is off", "you are not
// the platform" and "you did not say who you are" are three different things to
// an operator debugging an agent that cannot connect.
//
// Status codes: plane disabled or no internal secret configured → 503; missing
// or wrong bearer → 401; missing or malformed X-Ainsel-Agent → 400.
//
// The MCP server is built per request and served stateless, for the reason
// handlers_skill_mcp.go spells out: a stateful manager would pin an agent's
// session to one hub replica and break it on the next rollout, which for a long
// task means a mid-run failure.
func (s *Server) handleAgentMCP(w http.ResponseWriter, r *http.Request) {
	if !s.agentMCPEnabled {
		httpx.WriteError(w, http.StatusServiceUnavailable, "platform agent MCP disabled")
		return
	}
	if s.internalValidateSecret == "" {
		httpx.WriteError(w, http.StatusServiceUnavailable, "internal endpoints disabled")
		return
	}
	provided, ok := internalBearerToken(r.Header.Get("Authorization"))
	if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(s.internalValidateSecret)) != 1 {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	caller, ok := agentMCPCallerFrom(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "missing or invalid X-Ainsel-Agent or X-Ainsel-Invocation-Id")
		return
	}
	// Attribution, not authorisation: nothing on this plane is scoped by it
	// (see the endpoint comment), but a read plane whose calls are anonymous is
	// the one thing an operator cannot debug after the fact.
	slog.Debug("platform agent mcp: session opened", "agent", caller.agentName, "invocation", caller.invocationID)

	srv := server.NewMCPServer(agentMCPServerName, "1", server.WithToolCapabilities(false), server.WithRecovery())
	for _, tool := range s.agentMCPTools() {
		srv.AddTool(tool.spec, tool.handler)
	}
	server.NewStreamableHTTPServer(srv, server.WithStateLess(true)).ServeHTTP(w, r)
}

// agentMCPTool is one mounted tool: its schema and the function that answers it.
type agentMCPTool struct {
	spec    mcp.Tool
	handler server.ToolHandlerFunc
}

// agentMCPTools is the platform read plane.
//
// The list is the capability allow-list. There is no capability check in front
// of these handlers because there is nothing to check against: a mutating tool
// is not mounted on this server at all, so "an agent cannot create a trigger
// through the platform plane" is a property of this function rather than a rule
// some later code has to remember to enforce. The golden test in
// handlers_agent_mcp_test.go pins the list, so adding a write tool here
// fails CI instead of shipping.
//
// A tool whose backing store is not wired still appears and answers with a
// plain-language error: an agent that asked for logs from a hub without a log
// store should read "not configured", not "no such tool", because the second
// reads to a model like a typo in its own call.
func (s *Server) agentMCPTools() []agentMCPTool {
	return []agentMCPTool{
		{
			spec: mcp.NewTool("list_agents",
				mcp.WithDescription("List the agents the platform runs: name, display name, image, LLM, enabled tools and skills, pause state. Read-only — this endpoint cannot create, update or delete anything."),
				mcp.WithString("search", mcp.Description("Case-insensitive substring match on name, display name and description")),
			),
			handler: s.agentMCPListAgents,
		},
		{
			spec: mcp.NewTool("get_agent",
				mcp.WithDescription("Read one agent in full, including its MCP servers and env overrides (secret values are masked)."),
				mcp.WithString("name", mcp.Required(), mcp.Description("Agent resource name")),
			),
			handler: s.agentMCPGetAgent,
		},
		{
			spec: mcp.NewTool("list_invocations",
				mcp.WithDescription("Recent invocations (dispatched runs), newest first. Use it to see what actually ran, what triggered it, and how it ended."),
				mcp.WithString("agent", mcp.Description("Restrict to one agent (defaults to every agent)")),
				mcp.WithString("status", mcp.Description("running, success, failure or timeout")),
				mcp.WithString("trigger", mcp.Description("Restrict to invocations dispatched by this trigger name")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max results (default %d, hard max %d)", agentMCPDefaultLimit, agentMCPMaxLimit))),
			),
			handler: s.agentMCPListInvocations,
		},
		{
			spec: mcp.NewTool("get_invocation",
				mcp.WithDescription("Read one invocation by id: agent, trigger, event, timing, status and error."),
				mcp.WithString("id", mcp.Required(), mcp.Description("Invocation id, e.g. inv-1a2b3c4d")),
			),
			handler: s.agentMCPGetInvocation,
		},
		{
			spec: mcp.NewTool("get_agent_logs",
				mcp.WithDescription("Structured log lines agents published, newest first. Filter by agent and level."),
				mcp.WithString("agent", mcp.Description("Restrict to one agent")),
				mcp.WithString("level", mcp.Description("debug, info, warn or error")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max results (default %d, hard max %d)", agentMCPDefaultLimit, agentMCPMaxLimit))),
			),
			handler: s.agentMCPGetLogs,
		},
		{
			spec: mcp.NewTool("get_recent_errors",
				mcp.WithDescription("The error-level log lines of the window — the quickest read on what is currently failing. Pass agent to narrow it to your own."),
				mcp.WithString("agent", mcp.Description("Restrict to one agent (an agent asking about itself passes its own name)")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max results (default %d, hard max %d)", agentMCPDefaultLimit, agentMCPMaxLimit))),
			),
			handler: s.agentMCPGetRecentErrors,
		},
		{
			spec: mcp.NewTool("get_token_usage",
				mcp.WithDescription("Token consumption and priced cost over a window: input, output, cache read/write and cost in USD, in total and per agent. Answers \"what did my last run cost\" without leaving the agent."),
				mcp.WithString("agent", mcp.Description("Restrict to one agent (returns its totals only)")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
			),
			handler: s.agentMCPGetTokenUsage,
		},
		{
			spec: mcp.NewTool("query_metrics",
				mcp.WithDescription("Platform event metrics over a window. Without a metric name it returns the totals of all four; with one, it returns the time series. Metrics: events_consumed, triggers_matched, events_routed, routing_errors."),
				mcp.WithString("metric", mcp.Description("One of events_consumed, triggers_matched, events_routed, routing_errors. Empty returns totals.")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
				mcp.WithNumber("step_minutes", mcp.Description("Series bucket width in minutes (default 60). Ignored for totals.")),
			),
			handler: s.agentMCPQueryMetrics,
		},
		{
			spec: mcp.NewTool("get_agent_metrics",
				mcp.WithDescription("Per-agent operating figures — invocations, failures, average duration and token consumption — from the same backend the console's agents panel reads. Pass agent for one agent's row; without it, every agent, busiest first."),
				mcp.WithString("agent", mcp.Description("Restrict to one agent")),
				mcp.WithString("range", mcp.Description("Window: 1h, 6h, 24h or 7d. Empty means everything the hub still retains.")),
			),
			handler: s.agentMCPGetAgentMetrics,
		},
		{
			spec: mcp.NewTool("get_platform_health",
				mcp.WithDescription("Pod status of the platform's own namespace: phase, ready containers, restarts and waiting/terminated reasons. First stop when a tool or an agent is unreachable."),
			),
			handler: s.agentMCPPlatformHealth,
		},
		{
			spec: mcp.NewTool("list_mcp_servers",
				mcp.WithDescription("The MCP server registry — the tool servers an agent image can attach. Each row names the env var whose value is sent as its bearer; managed rows are owned by the platform or an integrator and cannot be changed from the console."),
			),
			handler: s.agentMCPListMCPServers,
		},
		{
			spec: mcp.NewTool("list_channels",
				mcp.WithDescription("The channel registry: the named event streams and how much has flowed through each in the window."),
				mcp.WithString("kind", mcp.Description("connector, agent or custom. Empty lists every kind.")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Count events from this many hours back (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
			),
			handler: s.agentMCPListChannels,
		},
		{
			spec: mcp.NewTool("get_channel_events",
				mcp.WithDescription("Recent events of one channel, newest first — what arrived, when, and whether it matched an agent."),
				mcp.WithString("id", mcp.Required(), mcp.Description("Channel id from list_channels")),
				mcp.WithString("match", mcp.Description("matched, unmatched or error")),
				mcp.WithNumber("since_hours", mcp.Description(fmt.Sprintf("Look back this many hours (default %d, max %d)", agentMCPDefaultHours, agentMCPMaxWindowHours))),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max results (default %d, hard max %d)", agentMCPDefaultLimit, agentMCPMaxLimit))),
			),
			handler: s.agentMCPGetChannelEvents,
		},
		{
			spec: mcp.NewTool("search_skills",
				mcp.WithDescription("Search the full hub skill catalogue by keyword and/or tags. Returns metadata only (id, name, description, tags) — never the body, so this stays cheap to call. Use it to find something, then load it with get_skill."),
				mcp.WithString("query", mcp.Description("Keyword matched case-insensitively against id, name and description")),
				mcp.WithArray("tags", mcp.Description("Tag names to filter by; a skill matches if it carries any of them"), mcp.WithStringItems()),
				mcp.WithNumber("limit", mcp.Description("Max results (default 20, hard max 50). Check truncated in the response before assuming you saw everything.")),
			),
			handler: s.agentMCPSearchSkills,
		},
		{
			spec: mcp.NewTool("get_skill",
				mcp.WithDescription("Load one skill by id. Returns the complete SKILL.md — YAML frontmatter plus body — exactly as a file-mounted skill would read. Not limited by which skills your agent image has enabled."),
				mcp.WithString("id", mcp.Required(), mcp.Description("Skill id (slug) from search_skills")),
			),
			handler: s.agentMCPGetSkill,
		},
	}
}

// agentMCPEmptyResult is what a read returns when there is nothing to say: an
// empty collection rather than an error, because "no invocations in the window"
// is an answer an agent acts on, not a failure to retry.
func agentMCPEmptyResult[T any](items []T) (*mcp.CallToolResult, error) {
	if items == nil {
		items = []T{}
	}
	return toolResultJSON(items)
}

// toolResultJSON encodes a payload as a text tool result. An encoding failure
// is about this call, so it belongs in the result rather than in a transport
// error the runtime would report as a broken server.
func toolResultJSON(v any) (*mcp.CallToolResult, error) {
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("failed to encode results: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(payload)), nil
}

// agentMCPSince resolves the since_hours argument to a window start, clamped to
// what the stores still retain.
func agentMCPSince(req mcp.CallToolRequest) time.Time {
	hours := req.GetInt("since_hours", agentMCPDefaultHours)
	if hours <= 0 {
		hours = agentMCPDefaultHours
	}
	if hours > agentMCPMaxWindowHours {
		hours = agentMCPMaxWindowHours
	}
	return time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
}

// agentMCPLimit resolves the limit argument, clamped to the per-response bound.
func agentMCPLimit(req mcp.CallToolRequest) int {
	limit := req.GetInt("limit", agentMCPDefaultLimit)
	if limit <= 0 {
		limit = agentMCPDefaultLimit
	}
	if limit > agentMCPMaxLimit {
		limit = agentMCPMaxLimit
	}
	return limit
}

// agentMCPNotConfigured is the answer of a tool whose backing store is absent.
func agentMCPNotConfigured(what string) *mcp.CallToolResult {
	return mcp.NewToolResultError(what + " is not configured on this hub")
}

func (s *Server) agentMCPListAgents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var list agentv1alpha1.AgentList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		return mcp.NewToolResultError("failed to list agents: " + err.Error()), nil
	}
	search := strings.ToLower(strings.TrimSpace(req.GetString("search", "")))

	// One namespace-scoped list for the display names, mirroring the REST list
	// handler: N+1 Gets per agent on a cluster with many agents is the same
	// query cost the console already refused.
	imageDisplayNames := make(map[string]string)
	var imageList agentv1alpha1.AgentImageList
	if err := s.client.List(ctx, &imageList, client.InNamespace(s.ns)); err == nil {
		for _, img := range imageList.Items {
			imageDisplayNames[img.Name] = img.Spec.DisplayName
		}
	}

	items := make([]SimpleAgentResponse, 0, len(list.Items))
	for _, a := range list.Items {
		if search != "" &&
			!strings.Contains(strings.ToLower(a.Name), search) &&
			!strings.Contains(strings.ToLower(a.Spec.DisplayName), search) &&
			!strings.Contains(strings.ToLower(a.Spec.Description), search) {
			continue
		}
		items = append(items, toSimpleAgentResponse(a, imageDisplayNames[a.Spec.ImageRef.Name]))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return agentMCPEmptyResult(items)
}

func (s *Server) agentMCPGetAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strings.TrimSpace(req.GetString("name", ""))
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, client.ObjectKey{Name: name, Namespace: s.ns}, &agent); err != nil {
		return mcp.NewToolResultError("no agent named " + name + " — run list_agents to see the names"), nil
	}
	return toolResultJSON(toSimpleAgentResponse(agent, s.resolveImageDisplayName(ctx, agent.Spec.ImageRef.Name)))
}

func (s *Server) agentMCPListInvocations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.invocations == nil {
		return agentMCPNotConfigured("the invocation history"), nil
	}
	opts := invocations.ListOptions{
		AgentName:   strings.TrimSpace(req.GetString("agent", "")),
		Status:      strings.TrimSpace(req.GetString("status", "")),
		TriggerName: strings.TrimSpace(req.GetString("trigger", "")),
		Since:       agentMCPSince(req),
		// Ask for one more than the page: the extra row is what tells the
		// caller there was more to see, without a second count query.
		Limit: agentMCPLimit(req) + 1,
	}
	found := s.invocations.List(opts)
	limit := opts.Limit - 1
	truncated := len(found) > limit
	if truncated {
		found = found[:limit]
	}
	return toolResultJSON(map[string]any{
		"invocations": found,
		"count":       len(found),
		"truncated":   truncated,
		"since":       opts.Since.UTC().Format(time.RFC3339),
	})
}

func (s *Server) agentMCPGetInvocation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := strings.TrimSpace(req.GetString("id", ""))
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	if s.invocations == nil {
		return agentMCPNotConfigured("the invocation history"), nil
	}
	inv, ok := s.invocations.Get(id)
	if !ok {
		return mcp.NewToolResultError("no invocation " + id + " — it may have aged out, or list_invocations to find the current ones"), nil
	}
	return toolResultJSON(inv)
}

// agentMCPGetAgentMetrics answers from the console's own per-agent figures:
// whichever backend agentsBackend() picks, read through the same two functions
// the /api/v1/observability/metrics/agents handler reads through. A second
// implementation of "an agent's metrics" is how a plane and a console come to
// disagree about the same number.
//
// What is deliberately not here is the operator plane's freeform PromQL and
// LogQL passthroughs. The hub already restricts its own raw-query endpoint to
// admins, because a raw query reads outside the AInsel namespace and can cost
// more than the caller's whole context. An agent-facing plane gets named metrics
// with a bounded window instead.
func (s *Server) agentMCPGetAgentMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agent := strings.TrimSpace(req.GetString("agent", ""))
	rangeKey := strings.TrimSpace(req.GetString("range", ""))
	if rangeKey != "" {
		if _, ok := supportedRanges[rangeKey]; !ok {
			return mcp.NewToolResultError("invalid range: expected 1h, 6h, 24h or 7d"), nil
		}
	}
	// The backend's own reason, not this plane's wording: it names the setting to
	// change, which is what an agent reporting a blocked read has to pass on.
	source, reason := s.agentsBackend()
	if source == "" {
		return mcp.NewToolResultError(reason), nil
	}
	var (
		agents map[string]*AgentMetric
		err    error
	)
	if source == metricsSourcePostgres {
		agents, err = s.agentsFromRecords(ctx, rangeKey)
	} else {
		agents, err = s.agentsFromPrometheus(ctx)
	}
	if err != nil {
		return mcp.NewToolResultError("failed to read agent metrics: " + err.Error()), nil
	}
	if agent != "" {
		found, ok := agents[agent]
		if !ok {
			return mcp.NewToolResultError("no metrics recorded for agent " + agent + " in this window — list_agents shows the agents the platform runs"), nil
		}
		return toolResultJSON(map[string]any{"source": source, "range": rangeKey, "agent": found})
	}
	items := make([]AgentMetric, 0, len(agents))
	for _, m := range agents {
		items = append(items, *m)
	}
	// Busiest first rather than a map's iteration order: a caller that reads the
	// first rows wants the agents that carry traffic, and the answer has to repeat
	// itself for an agent to compare it across calls.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Invocations != items[j].Invocations {
			return items[i].Invocations > items[j].Invocations
		}
		return items[i].Agent < items[j].Agent
	})
	return toolResultJSON(map[string]any{
		"source": source,
		"range":  rangeKey,
		"agents": items,
		"count":  len(items),
	})
}

func (s *Server) agentMCPGetLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.taskLogs.Ready() {
		return agentMCPNotConfigured("the task log table"), nil
	}
	level := strings.TrimSpace(req.GetString("level", ""))
	if level != "" && !tasklogs.ValidLevel(level) {
		return mcp.NewToolResultError("invalid level: expected debug, info, warn or error"), nil
	}
	return s.taskLogResult(ctx, req, level)
}

func (s *Server) agentMCPGetRecentErrors(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.taskLogs.Ready() {
		return agentMCPNotConfigured("the task log table"), nil
	}
	return s.taskLogResult(ctx, req, tasklogs.LevelError)
}

// taskLogResult is the shared body of the two log tools: errors is get_agent_logs
// with the level fixed, and the two must not drift in window or limit handling.
func (s *Server) taskLogResult(ctx context.Context, req mcp.CallToolRequest, level string) (*mcp.CallToolResult, error) {
	opts := tasklogs.ListOptions{
		AgentName: strings.TrimSpace(req.GetString("agent", "")),
		Level:     level,
		Since:     agentMCPSince(req),
		Limit:     agentMCPLimit(req),
	}
	entries, err := s.taskLogs.List(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError("failed to read logs: " + err.Error()), nil
	}
	return toolResultJSON(map[string]any{
		"logs":  entries,
		"count": len(entries),
		"level": level,
		"since": opts.Since.UTC().Format(time.RFC3339),
	})
}

// tokenUsageSummary is one window's token figures. The four components are
// reported separately as well as summed because a model that is told only
// "1.2M tokens" cannot tell that most of them were cache reads.
type tokenUsageSummary struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      float64 `json:"totalTokens"`
	CostUSD          float64 `json:"costUSD"`
	CostReported     bool    `json:"costReported"`
}

func tokenSummary(row tasklogs.TokenUsageRow) tokenUsageSummary {
	return tokenUsageSummary{
		InputTokens:      row.InputTokens,
		OutputTokens:     row.OutputTokens,
		CacheReadTokens:  row.CacheReadTokens,
		CacheWriteTokens: row.CacheWriteTokens,
		TotalTokens:      row.Total(),
		CostUSD:          row.CostUSD,
		CostReported:     row.CostReported,
	}
}

func (s *Server) agentMCPGetTokenUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.taskLogs.Ready() {
		return agentMCPNotConfigured("the token usage records"), nil
	}
	since := agentMCPSince(req)
	until := time.Now().UTC()
	agent := strings.TrimSpace(req.GetString("agent", ""))

	out := map[string]any{"since": since.Format(time.RFC3339)}
	if agent != "" {
		rows, err := s.taskLogs.AgentTokenUsageSince(ctx, since, until)
		if err != nil {
			return mcp.NewToolResultError("failed to read token usage: " + err.Error()), nil
		}
		for _, row := range rows {
			if row.AgentName == agent {
				out["agent"] = agent
				out["usage"] = tokenSummary(tasklogs.TokenUsageRow{
					InputTokens:      row.InputTokens,
					OutputTokens:     row.OutputTokens,
					CacheReadTokens:  row.CacheReadTokens,
					CacheWriteTokens: row.CacheWriteTokens,
					CacheReported:    row.CacheReported,
					CostUSD:          row.CostUSD,
					CostReported:     row.CostReported,
				})
				return toolResultJSON(out)
			}
		}
		out["agent"] = agent
		out["usage"] = tokenSummary(tasklogs.TokenUsageRow{})
		return toolResultJSON(out)
	}

	total, err := s.taskLogs.TokenUsageSince(ctx, since, until)
	if err != nil {
		return mcp.NewToolResultError("failed to read token usage: " + err.Error()), nil
	}
	perAgent, err := s.taskLogs.AgentTokenUsageSince(ctx, since, until)
	if err != nil {
		return mcp.NewToolResultError("failed to read token usage: " + err.Error()), nil
	}
	out["usage"] = tokenSummary(total)
	out["byAgent"] = perAgent
	return toolResultJSON(out)
}

func (s *Server) agentMCPQueryMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	since := agentMCPSince(req)
	until := time.Now().UTC()
	metric := strings.TrimSpace(req.GetString("metric", ""))
	// An argument the hub would reject is the caller's mistake whether or not the
	// metrics store is running: reporting it as "not configured" would send an
	// agent looking for a setting to change instead of fixing the call.
	if metric != "" && !telemetry.SupportsMetric(metric) {
		return mcp.NewToolResultError("unknown metric " + metric + " — expected events_consumed, triggers_matched, events_routed or routing_errors"), nil
	}
	if !s.telemetry.Ready() {
		return agentMCPNotConfigured("the event metrics store"), nil
	}

	if metric == "" {
		totals, err := s.telemetry.Totals(ctx, since, until)
		if err != nil {
			return mcp.NewToolResultError("failed to read metrics: " + err.Error()), nil
		}
		return toolResultJSON(map[string]any{
			"since": since.Format(time.RFC3339),
			"totals": map[string]int{
				"events_consumed":  totals.EventsConsumed,
				"triggers_matched": totals.TriggersMatched,
				"events_routed":    totals.EventsRouted,
				"routing_errors":   totals.RoutingErrors,
			},
		})
	}
	if metric != "" && !telemetry.SupportsMetric(metric) {
		return mcp.NewToolResultError("unknown metric " + metric), nil
	}
	stepMinutes := req.GetInt("step_minutes", 60)
	if stepMinutes <= 0 {
		stepMinutes = 60
	}
	buckets, err := s.telemetry.Series(ctx, metric, since, until, time.Duration(stepMinutes)*time.Minute)
	if err != nil {
		if errors.Is(err, telemetry.ErrUnknownMetric) {
			return mcp.NewToolResultError("unknown metric " + metric), nil
		}
		return mcp.NewToolResultError("failed to read metrics: " + err.Error()), nil
	}
	return toolResultJSON(map[string]any{
		"metric":  metric,
		"since":   since.Format(time.RFC3339),
		"buckets": buckets,
	})
}

// platformPodSummary is one pod's health, in the same shape the console's
// platform health view uses: an agent reading it should not have to learn a
// second pod representation.
type platformPodSummary struct {
	Name     string   `json:"name"`
	Phase    string   `json:"phase"`
	Ready    string   `json:"ready"`
	Restarts int32    `json:"restarts"`
	Problems []string `json:"problems,omitempty"`
}

func (s *Server) agentMCPPlatformHealth(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var pods corev1.PodList
	if err := s.client.List(ctx, &pods, client.InNamespace(s.ns)); err != nil {
		return mcp.NewToolResultError("failed to list pods: " + err.Error()), nil
	}
	summaries := make([]platformPodSummary, 0, len(pods.Items))
	for _, pod := range pods.Items {
		var restarts int32
		readyCount := 0
		var problems []string
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += cs.RestartCount
			if cs.Ready {
				readyCount++
				continue
			}
			switch {
			case cs.State.Waiting != nil:
				problems = append(problems, cs.Name+": waiting ("+cs.State.Waiting.Reason+")")
			case cs.State.Terminated != nil:
				problems = append(problems, cs.Name+": terminated ("+cs.State.Terminated.Reason+")")
			default:
				problems = append(problems, cs.Name+": not ready")
			}
		}
		summaries = append(summaries, platformPodSummary{
			Name:     pod.Name,
			Phase:    string(pod.Status.Phase),
			Ready:    fmt.Sprintf("%d/%d", readyCount, len(pod.Status.ContainerStatuses)),
			Restarts: restarts,
			Problems: problems,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	return toolResultJSON(summaries)
}

// mcpServerSummary is one registry row. tokenFromEnv is reported as a name,
// never a value: the row's credential is resolved on the agent pod from its own
// env, and a tool result that carried the value would put it in a model's
// context for no reason.
type mcpServerSummary struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName,omitempty"`
	Description  string `json:"description,omitempty"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv,omitempty"`
	ManagedBy    string `json:"managedBy,omitempty"`
	Managed      bool   `json:"managed"`
}

func (s *Server) agentMCPListMCPServers(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.mcp == nil {
		return agentMCPNotConfigured("the MCP server registry"), nil
	}
	rows, err := s.mcp.List(ctx)
	if err != nil {
		return mcp.NewToolResultError("failed to list MCP servers: " + err.Error()), nil
	}
	items := make([]mcpServerSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, mcpServerSummary{
			Name:         row.Name,
			DisplayName:  row.DisplayName,
			Description:  row.Description,
			URL:          row.URL,
			TokenFromEnv: row.TokenFromEnv,
			ManagedBy:    row.ManagedBy,
			Managed:      mcpservers.IsManaged(row.ManagedBy),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return agentMCPEmptyResult(items)
}

func (s *Server) agentMCPListChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	kind := channels.Kind(strings.TrimSpace(req.GetString("kind", "")))
	if kind != "" && !kind.Valid() {
		return mcp.NewToolResultError("invalid kind: expected connector, agent or custom"), nil
	}
	if s.channelSvc == nil {
		return agentMCPNotConfigured("the channel store"), nil
	}
	views, err := s.channelSvc.List(ctx, kind, agentMCPSince(req))
	if err != nil {
		return mcp.NewToolResultError("failed to list channels: " + err.Error()), nil
	}
	// Sorted by activity then id, the same order the console list uses, so an
	// agent and an operator reading "the busiest channels" see the same list.
	sort.Slice(views, func(i, j int) bool {
		if views[i].Counts.Events != views[j].Counts.Events {
			return views[i].Counts.Events > views[j].Counts.Events
		}
		return views[i].ID < views[j].ID
	})
	return agentMCPEmptyResult(views)
}

func (s *Server) agentMCPGetChannelEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := strings.TrimSpace(req.GetString("id", ""))
	if id == "" {
		return mcp.NewToolResultError("id is required — run list_channels to see the ids"), nil
	}
	if s.channelSvc == nil {
		return agentMCPNotConfigured("the channel store"), nil
	}
	events, total, err := s.channelSvc.Events(ctx, id, channels.EventQuery{
		Since: agentMCPSince(req),
		Limit: agentMCPLimit(req),
		Match: strings.TrimSpace(req.GetString("match", "")),
	})
	if err != nil {
		return mcp.NewToolResultError("failed to read channel events: " + err.Error()), nil
	}
	return toolResultJSON(map[string]any{
		"channel": id,
		"events":  events,
		"count":   len(events),
		"total":   total,
	})
}

// agentMCPSearchSkills and agentMCPGetSkill reuse the catalogue's own tool
// bodies, so the two surfaces that answer the same question cannot drift: one
// renderer, one truncation rule, one error wording.
func (s *Server) agentMCPSearchSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.skillDiscovery == nil {
		return agentMCPNotConfigured("the skill catalogue"), nil
	}
	return searchSkills(ctx, s.skillDiscovery, req)
}

func (s *Server) agentMCPGetSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.skillDiscovery == nil {
		return agentMCPNotConfigured("the skill catalogue"), nil
	}
	return getSkill(ctx, s.skillDiscovery, req)
}
