package v1alpha1

import (
	"testing"
)

func TestAgentSpecDeepCopyEnv(t *testing.T) {
	src := &AgentSpec{
		DisplayName: "test",
		ImageRef:    AgentImageRef{Name: "img"},
		Runtime:     AgentRuntime{},
		LLM:         AgentLLM{Model: "qwen"},
		Persona:     AgentPersona{ID: "01HXTESTPERSONA00000000000"},
		Env: []AgentEnvVar{
			{Name: "LOG_LEVEL", Value: "info"},
			{Name: "SESSION_TTL", Value: "3600", Secret: true},
		},
	}
	dst := src.DeepCopy()

	if dst == src {
		t.Fatal("DeepCopy returned same pointer")
	}
	if len(dst.Env) != 2 || dst.Env[0].Name != "LOG_LEVEL" || !dst.Env[1].Secret {
		t.Fatalf("Env not copied: %+v", dst.Env)
	}

	dst.Env[0].Value = "debug"
	if src.Env[0].Value == "debug" {
		t.Fatal("Env slice was aliased, not deep-copied")
	}

	dst.Env = append(dst.Env, AgentEnvVar{Name: "EXTRA"})
	if len(src.Env) != 2 {
		t.Fatalf("appending to copy changed original len: got %d want 2", len(src.Env))
	}
}

func TestAgentSpecDeepCopyEnvNil(t *testing.T) {
	src := &AgentSpec{
		DisplayName: "test",
		ImageRef:    AgentImageRef{Name: "img"},
		Runtime:     AgentRuntime{},
		LLM:         AgentLLM{Model: "qwen"},
		Persona:     AgentPersona{ID: "01HXTESTPERSONA00000000000"},
	}
	dst := src.DeepCopy()
	if dst.Env != nil {
		t.Fatalf("nil Env became non-nil after DeepCopy: %+v", dst.Env)
	}
}

func TestUsesChatGPTSignIn(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		openAI   *AgentOpenAI
		want     bool
	}{
		{"no openAI block", "openai", nil, false},
		{"api key method", "openai", &AgentOpenAI{AuthMethod: OpenAIAuthMethodAPIKey}, false},
		{"empty method defaults to api key", "openai", &AgentOpenAI{}, false},
		{"sign-in on openai", "openai", &AgentOpenAI{AuthMethod: OpenAIAuthMethodChatGPTSignIn}, true},
		{"sign-in block on another provider", "anthropic", &AgentOpenAI{AuthMethod: OpenAIAuthMethodChatGPTSignIn}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{Spec: AgentSpec{LLM: AgentLLM{Provider: tc.provider}, OpenAI: tc.openAI}}
			if got := a.UsesChatGPTSignIn(); got != tc.want {
				t.Fatalf("UsesChatGPTSignIn() = %v, want %v", got, tc.want)
			}
		})
	}
}
