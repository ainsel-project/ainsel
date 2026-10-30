package controller

import (
	"context"
	"testing"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// TestBuildAgentEnv_AppendOrder is the golden-order guard for the split of
// buildAgentEnv. The append order is load-bearing: Kubernetes resolves
// duplicate env names last-wins (so the reserved-name skip only works because
// platform vars precede image entries), and MCP_SERVER_TOKENS uses $(VAR)
// substitution that only resolves against vars declared earlier. If this test
// fails, the refactor changed which env entry wins at runtime.
func TestBuildAgentEnv_AppendOrder(t *testing.T) {
	t.Setenv("HUB_URL", "https://hub.example")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")

	agent := &ainselv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "my-agent", Namespace: "default"},
		Spec: ainselv1alpha1.AgentSpec{
			LLM: ainselv1alpha1.AgentLLM{
				Model:    "llama3",
				Provider: ainselv1alpha1.AgentLLMProviderOllamaCloud,
				MaxTurns: 12,
			},
			EnabledTools: []string{"shell-tool", "container-tool"},
		},
	}
	img := &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "my-image", Namespace: "default"},
		Spec: ainselv1alpha1.AgentImageSpec{
			Tools: []ainselv1alpha1.AgentImageTool{
				{Name: "container-tool", Kind: ainselv1alpha1.AgentImageToolKindContainer},
				{Name: "shell-tool", Kind: ainselv1alpha1.AgentImageToolKindShell},
			},
			Env: []ainselv1alpha1.AgentImageEnvVar{
				{Name: "MY_TOKEN", Value: "secret"},
				// Reserved platform name declared on the image: must be
				// dropped so it cannot override the canonical value.
				{Name: "HUB_INTERNAL_VALIDATE_SECRET", Value: "image-tries-to-override"},
				{Name: "PLAIN_VAR", Value: "x"},
			},
			MCPServers: []ainselv1alpha1.AgentImageMCPServer{
				{Name: "tokened", URL: "http://localhost:9000/mcp", TokenFromEnv: "MY_TOKEN"},
			},
		},
	}

	plan := &podPlan{}
	envs, err := buildAgentEnv(context.Background(), agent, "my-agent", img, plan)
	if err != nil {
		t.Fatalf("buildAgentEnv: %v", err)
	}

	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	want := []string{
		// platform block
		"AGENT_NAME", "PI_PROVIDER", "AINSEL_LLM_MODEL", "OLLAMA_CLOUD_MODEL",
		"AGENT_PERSONA_PATH", "HUB_URL", "HUB_INTERNAL_VALIDATE_SECRET",
		// llmProviderEnv
		"OLLAMA_API_KEY", "OPENCODE_API_KEY", "ALIBABA_CLOUD_API_KEY",
		"CUSTOM_PROVIDER_API_KEY", "OLLAMA_CLOUD_MAX_TURNS",
		// enabledToolsEnv
		"AGENT_TOOLS",
		// imageProfileEnv (reserved HUB_INTERNAL_VALIDATE_SECRET skipped)
		"MY_TOKEN", "PLAIN_VAR",
		// mcpEnv
		"MCP_SERVERS", "MCP_SERVER_TOKENS",
	}
	if len(names) != len(want) {
		t.Fatalf("env name sequence = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("env name sequence = %v, want %v", names, want)
		}
	}

	// The reserved-name skip must leave exactly one entry, holding the
	// platform value, and it must precede every image-profile entry.
	platformIdx := -1
	imageIdx := len(envs)
	for i, e := range envs {
		if e.Name == "HUB_INTERNAL_VALIDATE_SECRET" {
			if platformIdx >= 0 {
				t.Fatalf("HUB_INTERNAL_VALIDATE_SECRET appears twice: %v", names)
			}
			if e.Value != "platform-token" {
				t.Errorf("HUB_INTERNAL_VALIDATE_SECRET = %q, want the platform value", e.Value)
			}
			platformIdx = i
		}
		if e.Name == "MY_TOKEN" || e.Name == "PLAIN_VAR" {
			if i < imageIdx {
				imageIdx = i
			}
		}
	}
	if platformIdx < 0 {
		t.Fatal("HUB_INTERNAL_VALIDATE_SECRET missing")
	}
	if imageIdx <= platformIdx {
		t.Fatalf("image-profile entries must come after platform vars (platform at %d, first image entry at %d)", platformIdx, imageIdx)
	}

	// MCP_SERVER_TOKENS is last: its $(VAR) substitution requires the image
	// env entries to be declared before it.
	if names[len(names)-1] != "MCP_SERVER_TOKENS" {
		t.Errorf("MCP_SERVER_TOKENS must be the final entry, got %v", names)
	}
	if tok := envs[len(envs)-1].Value; tok == "" || tok != "tokened=$(MY_TOKEN)" {
		t.Errorf("MCP_SERVER_TOKENS = %q, want %q", tok, "tokened=$(MY_TOKEN)")
	}
	// AGENT_TOOLS keeps only container-kind tools.
	for _, e := range envs {
		if e.Name == "AGENT_TOOLS" && e.Value != "container-tool" {
			t.Errorf("AGENT_TOOLS = %q, want %q", e.Value, "container-tool")
		}
	}
	if len(plan.mcpEntries) == 0 {
		t.Error("plan.mcpEntries not populated")
	}
	if len(plan.missingEnv) != 0 {
		t.Errorf("plan.missingEnv = %v, want empty (MY_TOKEN is defined)", plan.missingEnv)
	}
}

// TestLLMProviderEnv_SecretRefSemantics pins the per-provider secret
// semantics the table-driven loop must preserve: absent block → default
// <agent>-<provider>-key with Optional=true (pod starts without a key);
// block with ref → ref name, Optional=false; block without ref → default
// name, Optional=false.
func TestLLMProviderEnv_SecretRefSemantics(t *testing.T) {
	base := func() *ainselv1alpha1.Agent {
		return &ainselv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: "ag"},
			Spec: ainselv1alpha1.AgentSpec{
				LLM: ainselv1alpha1.AgentLLM{Model: "m", Provider: ainselv1alpha1.AgentLLMProviderOllamaCloud},
			},
		}
	}
	secretRef := func(n string) *corev1.SecretKeySelector {
		return &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: n}}
	}

	tests := []struct {
		name       string
		mutate     func(*ainselv1alpha1.Agent)
		envName    string
		wantSecret string
		wantOpt    bool
	}{
		{
			name:       "ollama block absent: default name, optional",
			envName:    "OLLAMA_API_KEY",
			wantSecret: "ag-ollama-key",
			wantOpt:    true,
		},
		{
			name: "ollama block with ref: ref name, not optional",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OllamaCloud = &ainselv1alpha1.AgentOllamaCloud{APIKeySecretRef: secretRef("custom-ollama")}
			},
			envName:    "OLLAMA_API_KEY",
			wantSecret: "custom-ollama",
			wantOpt:    false,
		},
		{
			name: "ollama block without ref: default name, not optional",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OllamaCloud = &ainselv1alpha1.AgentOllamaCloud{}
			},
			envName:    "OLLAMA_API_KEY",
			wantSecret: "ag-ollama-key",
			wantOpt:    false,
		},
		{
			name: "opencode block with ref",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OpenCode = &ainselv1alpha1.AgentOpenCode{APIKeySecretRef: secretRef("oc-key")}
			},
			envName:    "OPENCODE_API_KEY",
			wantSecret: "oc-key",
			wantOpt:    false,
		},
		{
			name:       "alibaba block absent",
			envName:    "ALIBABA_CLOUD_API_KEY",
			wantSecret: "ag-alibaba-key",
			wantOpt:    true,
		},
		{
			name: "custom provider block with ref",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.CustomProvider = &ainselv1alpha1.AgentCustomProvider{APIKeySecretRef: secretRef("cp-key")}
			},
			envName:    "CUSTOM_PROVIDER_API_KEY",
			wantSecret: "cp-key",
			wantOpt:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := base()
			if tc.mutate != nil {
				tc.mutate(agent)
			}
			envs := llmProviderEnv(agent)
			if len(envs) != 4 {
				t.Fatalf("llmProviderEnv returned %d entries, want 4 (no MaxTurns set)", len(envs))
			}
			var found *corev1.EnvVar
			for i := range envs {
				if envs[i].Name == tc.envName {
					found = &envs[i]
				}
			}
			if found == nil {
				t.Fatalf("env %s not found", tc.envName)
			}
			ref := found.ValueFrom.SecretKeyRef
			if ref.Name != tc.wantSecret {
				t.Errorf("secret = %q, want %q", ref.Name, tc.wantSecret)
			}
			if ref.Key != "api-key" {
				t.Errorf("secret key = %q, want api-key", ref.Key)
			}
			if !ptr.Equal(ref.Optional, ptr.To(tc.wantOpt)) {
				t.Errorf("optional = %v, want %v", *ref.Optional, tc.wantOpt)
			}
		})
	}
}
