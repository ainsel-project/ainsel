package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// The platform read plane: POST /api/internal/agent-mcp. An agent reaches the
// platform's own state — agents, invocations, logs, metrics, token usage,
// channels, the MCP registry, the skill catalogue — through one endpoint whose
// tool set contains no write at all. These tests cover the three things that
// make that safe: who may open the endpoint, what the endpoint offers, and what
// it refuses to answer.

const (
	// platformMCPSecret is the hub's internal bearer secret for these fixtures.
	platformMCPSecret = "platform-agent-mcp-secret" // #nosec G101 -- test fixture, not a credential
	platformMCPAgent  = "reviewer"
)

// platformMCPServer wires the route on a dev-mode server with the plane enabled
// and the internal bearer secret set.
func platformMCPServer(t *testing.T, objects ...runtime.Object) *Server {
	t.Helper()
	s := testServer(t, objects...)
	s.mux.HandleFunc(AgentMCPPath, s.handleAgentMCP)
	s.SetInternalValidateSecret(platformMCPSecret)
	s.SetAgentMCPEnabled(true)
	return s
}

// platformMCPHeaders is the header set an agent runtime sends.
func platformMCPHeaders(bearer, agent string) map[string]string {
	h := map[string]string{}
	if bearer != "" {
		h["Authorization"] = "Bearer " + bearer
	}
	if agent != "" {
		h["X-Ainsel-Agent"] = agent
	}
	return h
}

func doPlatformMCP(t *testing.T, s *Server, headers map[string]string) (int, string) {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	req, err := http.NewRequest(http.MethodPost, ts.URL+AgentMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, sb.String()
}

func TestPlatformAgentMCPDisabledAnswers503(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc(AgentMCPPath, s.handleAgentMCP)
	s.SetInternalValidateSecret(platformMCPSecret)
	// Enabled is the opt-in; without it the endpoint is configured-off, not open.
	code, body := doPlatformMCP(t, s, platformMCPHeaders(platformMCPSecret, platformMCPAgent))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d (%s), want 503", code, body)
	}
}

func TestPlatformAgentMCPWithoutInternalSecretAnswers503(t *testing.T) {
	s := platformMCPServer(t)
	s.SetInternalValidateSecret("")
	code, body := doPlatformMCP(t, s, platformMCPHeaders(platformMCPSecret, platformMCPAgent))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d (%s), want 503", code, body)
	}
}

func TestPlatformAgentMCPRejectsBadBearer(t *testing.T) {
	for _, bearer := range []string{"", "wrong", "Bearer"} {
		code, body := doPlatformMCP(t, platformMCPServer(t), platformMCPHeaders(bearer, platformMCPAgent))
		if code != http.StatusUnauthorized {
			t.Fatalf("bearer %q: status = %d (%s), want 401", bearer, code, body)
		}
	}
}

// The caller identity is required and validated, not trusted: the same
// agentNamePattern the plugin plane enforces.
func TestPlatformAgentMCPRequiresValidAgentHeader(t *testing.T) {
	for _, agent := range []string{"", "Not A Name", "-lead", "a..b", strings.Repeat("a", 64)} {
		code, body := doPlatformMCP(t, platformMCPServer(t), platformMCPHeaders(platformMCPSecret, agent))
		if code != http.StatusBadRequest {
			t.Fatalf("agent %q: status = %d (%s), want 400", agent, code, body)
		}
	}
}

// --- the plane itself -------------------------------------------------------

// platformMCPClient connects an mcp-go client to the plane, as an agent runtime
// would, so tool names and result shapes are checked as an agent sees them.
func platformMCPClient(t *testing.T, s *Server) *client.Client {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	trans, err := transport.NewStreamableHTTP(ts.URL+AgentMCPPath,
		transport.WithHTTPHeaders(platformMCPHeaders(platformMCPSecret, platformMCPAgent)))
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	c := client.NewClient(trans)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo:      mcp.Implementation{Name: "ainsel-platform-test", Version: "0"},
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		},
	}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func platformMCPToolNames(t *testing.T, s *Server) []string {
	t.Helper()
	c := platformMCPClient(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// platformPlaneTools is the golden capability allow-list of the agent plane.
//
// It is a golden list on purpose: the guarantee the endpoint rests on is that no
// mutating tool is mounted on it, and a list a reviewer has to diff is the only
// form of that guarantee which fails CI when someone adds a write tool. Adding a
// read tool is a deliberate change here; adding a write tool is a different
// feature and needs the self-modification guard and a rate limit with it.
var platformPlaneTools = []string{
	"get_agent",
	"get_agent_logs",
	"get_agent_metrics",
	"get_channel_events",
	"get_invocation",
	"get_platform_health",
	"get_recent_errors",
	"get_skill",
	"get_token_usage",
	"list_agents",
	"list_channels",
	"list_invocations",
	"list_mcp_servers",
	"query_metrics",
	"search_skills",
}

// platformPlaneMutations is the deny-list. It is not exhaustive by construction,
// so it is checked as a substring rule rather than a name set: no tool on the
// plane may be named like one of the writes the operator-facing MCP offers.
var platformPlaneMutations = []string{
	"create_", "update_", "delete_", "assign_", "unassign_", "pause", "resume",
	"dispatch", "trigger", "retry", "ack", "nack", "enable", "disable",
}

func TestPlatformAgentMCPPlaneIsExactlyTheReadOnlyTools(t *testing.T) {
	got := platformMCPToolNames(t, platformMCPServer(t))
	want := slices.Clone(platformPlaneTools)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("plane drifted from the golden allow-list\n got: %v\nwant: %v", got, want)
	}
	for _, name := range got {
		for _, verb := range platformPlaneMutations {
			if strings.Contains(name, verb) {
				t.Errorf("tool %q looks mutating (%q): the agent plane must mount reads only", name, verb)
			}
		}
	}
}

// --- tool behaviour ---------------------------------------------------------

func platformAgentFixture() *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "reviewer", Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName: "Reviewer",
			Description: "reviews pull requests",
			ImageRef:    agentv1alpha1.AgentImageRef{Name: "pi-base"},
			LLM:         agentv1alpha1.AgentLLM{Model: "m1"},
		},
	}
}

func platformCall(t *testing.T, s *Server, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	c := platformMCPClient(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func TestPlatformAgentMCPListAgents(t *testing.T) {
	s := platformMCPServer(t, platformAgentFixture())
	res := platformCall(t, s, "list_agents", nil)
	if res.IsError {
		t.Fatalf("tool errored: %s", textOf(t, res))
	}
	var items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	// The plane returns a JSON array, not an object: the same shape the
	// operator-facing list tools return, so an agent reads one shape here.
	if err := json.Unmarshal([]byte(textOf(t, res)), &items); err != nil {
		t.Fatalf("decode: %v: %s", err, textOf(t, res))
	}
	if len(items) != 1 || items[0].ID != "reviewer" {
		t.Fatalf("items = %+v, want the one fixture agent", items)
	}

	// The search argument narrows rather than widens.
	res = platformCall(t, s, "list_agents", map[string]any{"search": "nomatch"})
	if txt := textOf(t, res); txt != "[]" {
		t.Fatalf("search=nomatch returned %s, want an empty array", txt)
	}
}

func TestPlatformAgentMCPGetAgentMasksSecretEnv(t *testing.T) {
	agent := platformAgentFixture()
	agent.Spec.Env = []agentv1alpha1.AgentEnvVar{
		{Name: "PLAIN", Value: "visible", Secret: false},
		{Name: "TOKEN", Value: "s3cr3t", Secret: true},
	}
	s := platformMCPServer(t, agent)
	txt := textOf(t, platformCall(t, s, "get_agent", map[string]any{"name": "reviewer"}))
	if !strings.Contains(txt, "PLAIN") {
		t.Errorf("plain env missing: %s", txt)
	}
	if strings.Contains(txt, "s3cr3t") {
		t.Errorf("a secret env value reached the tool result: %s", txt)
	}
}

func TestPlatformAgentMCPGetAgentUnknownNamesTheID(t *testing.T) {
	txt := textOf(t, platformCall(t, platformMCPServer(t), "get_agent", map[string]any{"name": "nope"}))
	if !strings.Contains(txt, "nope") || !strings.Contains(txt, "list_agents") {
		t.Errorf("unknown agent answer = %q, want the id and the way to find one", txt)
	}
}

func TestPlatformAgentMCPListInvocationsTruncatesTruthfully(t *testing.T) {
	s := platformMCPServer(t)
	for i := 0; i < 5; i++ {
		s.invocations.Record(invocations.Invocation{ID: fmt.Sprintf("inv-%d", i), AgentID: "reviewer"})
	}
	res := platformCall(t, s, "list_invocations", map[string]any{"limit": 2})
	var out struct {
		Invocations []struct {
			ID string `json:"id"`
		} `json:"invocations"`
		Count     int  `json:"count"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(textOf(t, res)), &out); err != nil {
		t.Fatalf("decode: %v: %s", err, textOf(t, res))
	}
	if out.Count != 2 || len(out.Invocations) != 2 {
		t.Fatalf("count = %d of %d, want the limit honoured", out.Count, len(out.Invocations))
	}
	// Five rows exist, two were returned: truncated must say so, or a model
	// reads a page as the whole history.
	if !out.Truncated {
		t.Errorf("truncated = false with 5 matching invocations and a limit of 2")
	}
}

func TestPlatformAgentMCPInvocationToolsReportMissingHistory(t *testing.T) {
	s := platformMCPServer(t)
	s.invocations = nil
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"list_invocations", nil},
		{"get_invocation", map[string]any{"id": "inv-1"}},
	} {
		txt := textOf(t, platformCall(t, s, call.tool, call.args))
		if !strings.Contains(txt, "not configured") {
			t.Errorf("%s without a store = %q, want a plain 'not configured' answer", call.tool, txt)
		}
	}
}

// A store the hub does not run is a different fact from a tool that does not
// exist: the second reads to a model like its own mistake, and it would retry.
func TestPlatformAgentMCPUnwiredStoresAnswerNotConfigured(t *testing.T) {
	s := platformMCPServer(t)
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"get_agent_logs", nil},
		{"get_recent_errors", nil},
		{"get_token_usage", nil},
		{"query_metrics", nil},
		{"list_mcp_servers", nil},
		{"list_channels", nil},
		{"get_channel_events", map[string]any{"id": "connector:github"}},
		{"search_skills", nil},
		{"get_skill", map[string]any{"id": "some-skill"}},
	}
	for _, c := range calls {
		txt := textOf(t, platformCall(t, s, c.tool, c.args))
		if !strings.Contains(txt, "not configured") {
			t.Errorf("%s unwired = %q, want a plain 'not configured' answer", c.tool, txt)
		}
	}
}

// get_token_usage answers "what did this cost", and the named-agent branch must
// answer it with the same figures the aggregate answer gives: the priced cost,
// plus the flag saying whether cost was ever reported (issue #75). Dropping the
// two fields there made a priced run read as a measured zero to the one agent
// that asked about itself, while the same tool without `agent` reported the real
// cost — two answers to one question.
func TestPlatformAgentMCPGetTokenUsageReportsCostForOneAgent(t *testing.T) {
	pool := dbtest.Open(t)
	s := platformMCPServer(t)
	store := tasklogs.NewStore(pool)
	s.SetTaskLogStore(store)
	ctx := context.Background()
	seed := func(agent string, cost *float64) {
		t.Helper()
		if err := store.InsertConversation(ctx, &tasklogs.ConversationMessage{
			AgentName: agent, Role: "assistant", Content: "done",
			InputTokens: 100, OutputTokens: 10, CostUSD: cost,
		}); err != nil {
			t.Fatalf("seed %s: %v", agent, err)
		}
	}
	seed("reviewer", fptr(0.25))
	seed("writer", fptr(0.01))
	seed("unpriced", nil)

	type usage struct {
		InputTokens  int64   `json:"inputTokens"`
		TotalTokens  float64 `json:"totalTokens"`
		CostUSD      float64 `json:"costUSD"`
		CostReported bool    `json:"costReported"`
	}
	decode := func(res *mcp.CallToolResult) (string, usage, []map[string]any) {
		t.Helper()
		if res.IsError {
			t.Fatalf("tool errored: %s", textOf(t, res))
		}
		var out struct {
			Agent   string           `json:"agent"`
			Usage   usage            `json:"usage"`
			ByAgent []map[string]any `json:"byAgent"`
		}
		if err := json.Unmarshal([]byte(textOf(t, res)), &out); err != nil {
			t.Fatalf("decode: %v: %s", err, textOf(t, res))
		}
		return out.Agent, out.Usage, out.ByAgent
	}

	agent, u, _ := decode(platformCall(t, s, "get_token_usage", map[string]any{"agent": "reviewer"}))
	if agent != "reviewer" {
		t.Errorf("agent = %q, want the one asked for", agent)
	}
	if u.InputTokens != 100 || u.TotalTokens != 110 {
		t.Errorf("named usage = %+v, want reviewer's 100 input / 110 total only", u)
	}
	if !u.CostReported || u.CostUSD <= 0 {
		t.Errorf("named usage of a priced run = %+v, want costUSD > 0 and costReported true", u)
	}

	// An agent whose runs never reported a cost must read as "not reported", not
	// as a measured zero — the same rule the aggregate path follows.
	_, silent, _ := decode(platformCall(t, s, "get_token_usage", map[string]any{"agent": "unpriced"}))
	if silent.CostReported || silent.CostUSD != 0 {
		t.Errorf("unpriced agent usage = %+v, want costUSD 0 with costReported false", silent)
	}

	// The aggregate answer and the named answer agree: same tool, one question.
	_, total, byAgent := decode(platformCall(t, s, "get_token_usage", nil))
	if !total.CostReported || total.CostUSD <= 0 {
		t.Errorf("aggregate usage = %+v, want the reported cost", total)
	}
	var reviewerRow map[string]any
	for _, row := range byAgent {
		if row["AgentName"] == "reviewer" {
			reviewerRow = row
		}
	}
	if reviewerRow == nil {
		t.Fatalf("byAgent rows missing reviewer: %v", byAgent)
	}
	if cost, _ := reviewerRow["CostUSD"].(float64); cost <= 0 {
		t.Errorf("byAgent reviewer CostUSD = %v, want the reported cost", reviewerRow["CostUSD"])
	}
	if reported, _ := reviewerRow["CostReported"].(bool); !reported {
		t.Errorf("byAgent reviewer CostReported = %v, want true", reviewerRow["CostReported"])
	}
}

func TestPlatformAgentMCPHealthSummarizesPods(t *testing.T) {
	s := platformMCPServer(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hub-backend-0", Namespace: "test-ns"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "hub",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "CrashLoopBackOff",
			}},
		}}},
	})
	txt := textOf(t, platformCall(t, s, "get_platform_health", nil))
	if !strings.Contains(txt, "hub-backend-0") || !strings.Contains(txt, "CrashLoopBackOff") {
		t.Errorf("health summary = %s, want the pod and its waiting reason", txt)
	}
}

func TestPlatformAgentMCPSkillsReuseTheCatalogue(t *testing.T) {
	s := platformMCPServer(t)
	s.SetSkillDiscovery(catalogueFixture())
	res := platformCall(t, s, "search_skills", map[string]any{"query": "rubric"})
	if res.IsError {
		t.Fatalf("tool errored: %s", textOf(t, res))
	}
	txt := textOf(t, res)
	if !strings.Contains(txt, "matched") {
		t.Errorf("search result = %s, want the catalogue's own shape with matched", txt)
	}
	// The catalogue's answer is the contract: same field names, same truncation
	// flag, so an agent learns the skill tools once and they behave the same here.
	if !strings.Contains(txt, "rubric-designer") {
		t.Errorf("search result = %s, want the matching skill", txt)
	}
}

func TestPlatformAgentMCPRejectsInvalidEnumArguments(t *testing.T) {
	s := platformMCPServer(t)
	if txt := textOf(t, platformCall(t, s, "list_channels", map[string]any{"kind": "nonsense"})); !strings.Contains(txt, "invalid kind") {
		t.Errorf("bad kind = %q, want the accepted values named", txt)
	}
	if txt := textOf(t, platformCall(t, s, "query_metrics", map[string]any{"metric": "nope"})); !strings.Contains(txt, "unknown metric") {
		t.Errorf("bad metric = %q, want the accepted names listed", txt)
	}
	if txt := textOf(t, platformCall(t, s, "get_agent_metrics", map[string]any{"range": "2d"})); !strings.Contains(txt, "invalid range") {
		t.Errorf("bad range = %q, want the accepted windows named", txt)
	}
}

// The per-agent figures come from whichever backend the console picks, so on a
// hub with neither the answer is the console's own reason — the sentence that
// names the setting to change, which is what an agent has to report.
func TestPlatformAgentMCPAgentMetricsNamesTheMissingBackend(t *testing.T) {
	txt := textOf(t, platformCall(t, platformMCPServer(t), "get_agent_metrics", nil))
	if !strings.Contains(txt, "metrics backend") && !strings.Contains(txt, "prometheus") {
		t.Errorf("get_agent_metrics with no backend = %q, want the console's own reason", txt)
	}
}
