package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// agentImageWithMCPServers builds an AgentImage with a catalog and the MCP
// servers an agent on it inherits when it pins none of its own.
func agentImageWithMCPServers(name string, tools, mcpServers []string) *agentv1alpha1.AgentImage {
	img := &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: name,
			ImageURL:    "registry/" + name + ":latest",
		},
	}
	for _, t := range tools {
		img.Spec.Tools = append(img.Spec.Tools, agentv1alpha1.AgentImageTool{
			Name: t,
			Kind: agentv1alpha1.AgentImageToolKindContainer,
		})
	}
	for _, s := range mcpServers {
		img.Spec.MCPServers = append(img.Spec.MCPServers, agentv1alpha1.AgentImageMCPServer{
			Name: s,
			URL:  "https://mcp.example/" + s,
		})
	}
	return img
}

// A server-level grant is what `hub migrate-enabled-tools` writes so an agent
// that had no grant keeps the MCP tools it has today (issue #368). The runtime
// honours three spellings; the hub accepts all three, for a server the agent
// effectively has.
func TestValidateAgentImageRef_ServerLevelGrants(t *testing.T) {
	img := agentImageWithMCPServers("img-1", []string{"bash"}, []string{"github"})
	s := testServer(t, img)
	ctx := context.Background()

	tests := []struct {
		name    string
		enabled []string
		mcp     mcpGrantScope
		wantErr string
	}{
		{
			name:    "mcp__ form of an inherited server",
			enabled: []string{"bash", "mcp__github"},
			mcp:     inheritMCPGrantScope(),
		},
		{
			name:    "trailing-underscore form",
			enabled: []string{"mcp__github__"},
			mcp:     inheritMCPGrantScope(),
		},
		{
			name:    "bare server name",
			enabled: []string{"github"},
			mcp:     inheritMCPGrantScope(),
		},
		{
			name:    "server the agent pins",
			enabled: []string{"mcp__linear"},
			mcp:     pinnedMCPGrantScope([]string{"linear"}),
		},
		{
			name:    "server the agent does not have",
			enabled: []string{"mcp__linear"},
			mcp:     inheritMCPGrantScope(),
			wantErr: "enabledTools not in image: mcp__linear",
		},
		{
			name:    "image server the agent pinned away",
			enabled: []string{"mcp__github"},
			mcp:     pinnedMCPGrantScope([]string{}),
			wantErr: "enabledTools not in image: mcp__github",
		},
		{
			name:    "a full MCP tool name is still not a catalog entry",
			enabled: []string{"mcp__github__list_issues"},
			mcp:     inheritMCPGrantScope(),
			wantErr: "enabledTools not in image: mcp__github__list_issues",
		},
		{
			name:    "a catalog tool is unaffected",
			enabled: []string{"bash"},
			mcp:     pinnedMCPGrantScope(nil),
		},
		{
			name:    "the mcp__ prefix alone names nothing",
			enabled: []string{"mcp__"},
			mcp:     inheritMCPGrantScope(),
			wantErr: "enabledTools not in image: mcp__",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, err := s.validateAgentImageRef(ctx, "img-1", tc.enabled, tc.mcp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if name != "img-1" {
					t.Errorf("display name = %q, want img-1", name)
				}
				return
			}
			var ae *apiError
			if !errors.As(err, &ae) {
				t.Fatalf("expected an *apiError, got %v", err)
			}
			if ae.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", ae.Status)
			}
			if ae.Msg != tc.wantErr {
				t.Errorf("message = %q, want %q", ae.Msg, tc.wantErr)
			}
			if name != "" {
				t.Errorf("display name = %q on a rejected grant, want empty", name)
			}
		})
	}
}

func TestServerLevelGrantServer(t *testing.T) {
	tests := []struct {
		entry string
		want  string
	}{
		{"mcp__github", "github"},
		{"mcp__github__", "github"},
		{"github", "github"},
		{"mcp__github__list_issues", ""},
		{"mcp__", ""},
		{"mcp____", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := serverLevelGrantServer(tc.entry); got != tc.want {
			t.Errorf("serverLevelGrantServer(%q) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

func TestUpdateMCPGrantScope(t *testing.T) {
	stored := &agentv1alpha1.AgentMCP{Servers: []agentv1alpha1.AgentMCPServer{
		{Name: "linear", URL: "https://mcp.example/linear"},
	}}

	if got := updateMCPGrantScope(&AgentMCPRequest{Servers: []string{"other"}}, stored); got.inherited || len(got.servers) != 1 || got.servers[0] != "other" {
		t.Errorf("a requested selection must win over the stored pin: %+v", got)
	}
	if got := updateMCPGrantScope(nil, stored); got.inherited || len(got.servers) != 1 || got.servers[0] != "linear" {
		t.Errorf("an untouched MCP section must keep the stored pin: %+v", got)
	}
	if got := updateMCPGrantScope(nil, nil); !got.inherited {
		t.Errorf("no request and no stored pin means the image's servers: %+v", got)
	}
	if got := updateMCPGrantScope(&AgentMCPRequest{Servers: nil}, stored); got.inherited || len(got.servers) != 0 {
		t.Errorf("an explicit empty selection grants no server: %+v", got)
	}
}

// `hub migrate-enabled-tools` writes `mcp__<server>` into every MCP-using agent,
// so an update that drops the server meets a grant that still names it. The stale
// entry leaves with the server instead of failing the whole PUT, while an entry
// that never named one of the agent's servers is still the caller's mistake.
func TestDropStaleMCPServerGrants(t *testing.T) {
	tests := []struct {
		name        string
		grant       []string
		next        []string
		previous    []string
		wantKept    []string
		wantDropped []string
	}{
		{
			name:        "the dropped server takes its entry with it",
			grant:       []string{"bash", "mcp__github"},
			next:        []string{"linear"},
			previous:    []string{"github", "linear"},
			wantKept:    []string{"bash"},
			wantDropped: []string{"mcp__github"},
		},
		{
			name:     "a server the update keeps keeps its entry",
			grant:    []string{"mcp__github"},
			next:     []string{"github"},
			previous: []string{"github"},
			wantKept: []string{"mcp__github"},
		},
		{
			name:     "widening the selection drops nothing",
			grant:    []string{"mcp__github"},
			next:     []string{"github", "linear"},
			previous: []string{"github"},
			wantKept: []string{"mcp__github"},
		},
		{
			name:     "an entry that named no server the agent had is left for validation",
			grant:    []string{"mcp__nope"},
			next:     []string{"linear"},
			previous: []string{"github"},
			wantKept: []string{"mcp__nope"},
		},
		{
			name:     "the bare spelling is also a catalog tool name, so it stays",
			grant:    []string{"github"},
			next:     nil,
			previous: []string{"github"},
			wantKept: []string{"github"},
		},
		{
			name:     "a full tool name is not a server-level entry",
			grant:    []string{"mcp__github__list_issues"},
			next:     nil,
			previous: []string{"github"},
			wantKept: []string{"mcp__github__list_issues"},
		},
		{
			name:        "dropping the only entry leaves no grant",
			grant:       []string{"mcp__github"},
			next:        nil,
			previous:    []string{"github"},
			wantDropped: []string{"mcp__github"},
		},
	}
	for _, tc := range tests {
		kept, dropped := dropStaleMCPServerGrants(tc.grant, tc.next, tc.previous)
		if !slices.Equal(kept, tc.wantKept) {
			t.Errorf("%s: kept = %v, want %v", tc.name, kept, tc.wantKept)
		}
		if !slices.Equal(dropped, tc.wantDropped) {
			t.Errorf("%s: dropped = %v, want %v", tc.name, dropped, tc.wantDropped)
		}
	}
}

// The end-to-end shape the migration leaves behind: an agent whose grant names a
// server, updated so the selection drops it — with `enabledTools` untouched (the
// console's PATCH-style update) and with it echoed back (a plugin reconcile).
func TestUpdateAgent_MCPServerRemovalPrunesServerLevelGrant(t *testing.T) {
	img := agentImageWithMCPServers("img-grant", []string{"bash"}, nil)
	s := testServer(t, img)
	s.mcp = mcpServiceForTest(t)
	ctx := t.Context()
	for _, name := range []string{"github", "linear"} {
		if err := s.mcp.Create(ctx, &mcpservers.MCPServer{Name: name, DisplayName: name, URL: "https://mcp.example/" + name}); err != nil {
			t.Fatalf("create registry server %s: %v", name, err)
		}
	}
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var bodyReader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			bodyReader = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	storedGrant := func(id string) []string {
		t.Helper()
		var a agentv1alpha1.Agent
		if err := s.client.Get(ctx, types.NamespacedName{Name: id, Namespace: "test-ns"}, &a); err != nil {
			t.Fatalf("get stored agent: %v", err)
		}
		return a.Spec.EnabledTools
	}

	rec := do(http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name:         "Grant Agent",
		ImageRef:     AgentImageRefInfo{Name: "img-grant"},
		LLM:          AgentLLMInfo{Model: "glm-5.1:cloud", Provider: "ollama-cloud"},
		EnabledTools: []string{"bash", "mcp__github"},
		MCP:          &AgentMCPRequest{Servers: []string{"github", "linear"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if got := storedGrant(created.ID); !slices.Equal(got, []string{"bash", "mcp__github"}) {
		t.Fatalf("seed grant = %v, want [bash mcp__github]", got)
	}

	// Narrow the selection, grant untouched: the entry that named the removed
	// server goes with it and the update succeeds.
	rec = do(http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		MCP: &AgentMCPRequest{Servers: []string{"linear"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("narrow: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := storedGrant(created.ID); !slices.Equal(got, []string{"bash"}) {
		t.Fatalf("narrow: grant = %v, want [bash]", got)
	}

	// The other direction: re-adding the server restores nothing on its own — the
	// grant is the caller's to write — and drops nothing either.
	rec = do(http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		MCP: &AgentMCPRequest{Servers: []string{"github", "linear"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("widen: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := storedGrant(created.ID); !slices.Equal(got, []string{"bash"}) {
		t.Fatalf("widen: grant = %v, want [bash]", got)
	}

	// A request that echoes the grant it already holds while narrowing the
	// selection — what a plugin reconcile sends — lands the same way, and the
	// response reports the grant the agent ended up with.
	echoed := []string{"bash", "mcp__github"}
	rec = do(http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		EnabledTools: &echoed,
		MCP:          &AgentMCPRequest{Servers: []string{"linear"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("echoed narrow: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if !slices.Equal(updated.EnabledTools, []string{"bash"}) {
		t.Fatalf("echoed narrow response grant = %v, want [bash]", updated.EnabledTools)
	}
	if got := storedGrant(created.ID); !slices.Equal(got, []string{"bash"}) {
		t.Fatalf("echoed narrow: grant = %v, want [bash]", got)
	}

	// An entry naming a server the agent never had is still rejected: the prune
	// only retires grants the narrowing made stale, it does not swallow typos.
	bogus := []string{"bash", "mcp__never-registered"}
	rec = do(http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		EnabledTools: &bogus,
		MCP:          &AgentMCPRequest{Servers: []string{"linear"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus grant: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
