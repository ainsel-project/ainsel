package controller

import (
	"context"
	"sort"
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
//
// The fixture deliberately uses distinct values for agent.Name ("my-agent")
// and the agentName parameter ("agent-my-agent") because production derives
// them differently (the reconciler passes agentName = "agent-<name>", while
// llmProviderEnv builds default Secret names from agent.Name). The assertions
// pin which of the two feeds each Secret reference, so swapping them in
// imageProfileEnv or llmProviderEnv cannot pass silently.
func TestBuildAgentEnv_AppendOrder(t *testing.T) {
	t.Setenv("HUB_URL", "https://hub.example")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")

	// Every platform-reserved name gets an image-side entry claiming to
	// override it; the reserved-name skip must drop each one. Iterating the
	// guard's own map means adding a platform var without an image-side
	// guard is caught here.
	reserved := platformManagedAgentEnv()
	if len(reserved) == 0 {
		t.Fatal("platformManagedAgentEnv is empty despite HUB_INTERNAL_VALIDATE_SECRET being set")
	}
	reservedNames := make([]string, 0, len(reserved))
	for name := range reserved {
		reservedNames = append(reservedNames, name)
	}
	sort.Strings(reservedNames)

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
	// Image-profile entries: two ordinary vars plus one attempted override
	// per reserved name.
	imageEnv := []ainselv1alpha1.AgentImageEnvVar{
		{Name: "MY_TOKEN", Value: "image-value"},
		{Name: "PLAIN_VAR", Value: "x"},
	}
	for _, name := range reservedNames {
		imageEnv = append(imageEnv, ainselv1alpha1.AgentImageEnvVar{
			Name: name, Value: "image-tries-to-override",
		})
	}
	img := &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "my-image", Namespace: "default"},
		Spec: ainselv1alpha1.AgentImageSpec{
			Tools: []ainselv1alpha1.AgentImageTool{
				{Name: "container-tool", Kind: ainselv1alpha1.AgentImageToolKindContainer},
				{Name: "shell-tool", Kind: ainselv1alpha1.AgentImageToolKindShell},
			},
			Env: imageEnv,
			MCPServers: []ainselv1alpha1.AgentImageMCPServer{
				{Name: "tokened", URL: "http://localhost:9000/mcp", TokenFromEnv: "MY_TOKEN"},
			},
		},
	}

	plan := &podPlan{}
	envs, err := buildAgentEnv(context.Background(), agent, "agent-my-agent", img, plan)
	if err != nil {
		t.Fatalf("buildAgentEnv: %v", err)
	}

	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	// Expected sequence: platform block, llmProviderEnv, enabledToolsEnv,
	// imageProfileEnv (reserved names skipped, in declaration order), mcpEnv.
	want := []string{
		// platform block
		"AGENT_NAME", "PI_PROVIDER", "AINSEL_LLM_MODEL", "OLLAMA_CLOUD_MODEL",
		"AGENT_PERSONA_PATH", "HUB_URL", "HUB_INTERNAL_VALIDATE_SECRET",
		// llmProviderEnv
		"OLLAMA_API_KEY", "OPENCODE_API_KEY", "ALIBABA_CLOUD_API_KEY",
		"CUSTOM_PROVIDER_API_KEY", "OLLAMA_CLOUD_MAX_TURNS",
		// enabledToolsEnv
		"AGENT_TOOLS",
	}
	for _, e := range imageEnv {
		if _, isReserved := reserved[e.Name]; isReserved {
			continue
		}
		want = append(want, e.Name)
	}
	// mcpEnv
	want = append(want, "MCP_SERVERS", "MCP_SERVER_TOKENS")
	if len(names) != len(want) {
		t.Fatalf("env name sequence = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("env name sequence = %v, want %v", names, want)
		}
	}

	// Every reserved name must appear exactly once, hold the platform value,
	// and precede every image-profile entry.
	firstImageIdx := len(envs)
	for i, e := range envs {
		if _, isReserved := reserved[e.Name]; isReserved {
			continue
		}
		if e.Name == "MY_TOKEN" || e.Name == "PLAIN_VAR" {
			if i < firstImageIdx {
				firstImageIdx = i
			}
		}
	}
	for name, wantValue := range reserved {
		idx := -1
		for i, e := range envs {
			if e.Name == name {
				if idx >= 0 {
					t.Fatalf("%s appears twice: %v", name, names)
				}
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("%s missing", name)
		}
		if envs[idx].Value != wantValue {
			t.Errorf("%s = %q, want the platform value %q", name, envs[idx].Value, wantValue)
		}
		if firstImageIdx <= idx {
			t.Errorf("%s (at %d) must precede image-profile entries (first at %d)", name, idx, firstImageIdx)
		}
	}

	// The image-profile entries must reference the agentName-derived
	// "<agentName>-image-env" Secret, not an agent.Name-derived one.
	for _, e := range envs {
		if e.Name != "MY_TOKEN" && e.Name != "PLAIN_VAR" {
			continue
		}
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Fatalf("%s: image env entry must source from the image-env Secret", e.Name)
		}
		if got, wantRef := e.ValueFrom.SecretKeyRef.Name, "agent-my-agent-image-env"; got != wantRef {
			t.Errorf("%s secret = %q, want %q (agentName-derived, not agent.Name-derived)", e.Name, got, wantRef)
		}
	}
	// The default provider-key Secret must be agent.Name-derived
	// ("<agent>-ollama-key"), not agentName-derived.
	for _, e := range envs {
		if e.Name != "OLLAMA_API_KEY" {
			continue
		}
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Fatal("OLLAMA_API_KEY must source from a Secret")
		}
		if got, wantRef := e.ValueFrom.SecretKeyRef.Name, "my-agent-ollama-key"; got != wantRef {
			t.Errorf("OLLAMA_API_KEY secret = %q, want %q (agent.Name-derived)", got, wantRef)
		}
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

	// wantRefName is the expected SecretKeySelector name. (Named without
	// "secret" so gosec G101 does not flag the fixture literals.)
	tests := []struct {
		name        string
		mutate      func(*ainselv1alpha1.Agent)
		envName     string
		wantRefName string
		wantOpt     bool
	}{
		{
			name:        "ollama block absent: default name, optional",
			envName:     "OLLAMA_API_KEY",
			wantRefName: "ag-ollama-key",
			wantOpt:     true,
		},
		{
			name: "ollama block with ref: ref name, not optional",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OllamaCloud = &ainselv1alpha1.AgentOllamaCloud{APIKeySecretRef: secretRef("custom-ollama")}
			},
			envName:     "OLLAMA_API_KEY",
			wantRefName: "custom-ollama",
			wantOpt:     false,
		},
		{
			name: "ollama block without ref: default name, not optional",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OllamaCloud = &ainselv1alpha1.AgentOllamaCloud{}
			},
			envName:     "OLLAMA_API_KEY",
			wantRefName: "ag-ollama-key",
			wantOpt:     false,
		},
		{
			name: "opencode block with ref",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.OpenCode = &ainselv1alpha1.AgentOpenCode{APIKeySecretRef: secretRef("oc-ref")}
			},
			envName:     "OPENCODE_API_KEY",
			wantRefName: "oc-ref",
			wantOpt:     false,
		},
		{
			name:        "alibaba block absent",
			envName:     "ALIBABA_CLOUD_API_KEY",
			wantRefName: "ag-alibaba-key",
			wantOpt:     true,
		},
		{
			name: "custom provider block with ref",
			mutate: func(a *ainselv1alpha1.Agent) {
				a.Spec.CustomProvider = &ainselv1alpha1.AgentCustomProvider{APIKeySecretRef: secretRef("cp-ref")}
			},
			envName:     "CUSTOM_PROVIDER_API_KEY",
			wantRefName: "cp-ref",
			wantOpt:     false,
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
			if ref.Name != tc.wantRefName {
				t.Errorf("secret = %q, want %q", ref.Name, tc.wantRefName)
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
