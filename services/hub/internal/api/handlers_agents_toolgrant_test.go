package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
