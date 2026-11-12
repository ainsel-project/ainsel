package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ainsel-project/ainsel/shared/api/llmprovider"
)

func providerTestServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t, testAgentImage("img-1"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	return s
}

func doAgentJSON(t *testing.T, s *Server, method, path string, body any, want int) []byte {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func storedKey(t *testing.T, s *Server, name string) string {
	t.Helper()
	var sec corev1.Secret
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "test-ns"}, &sec); err != nil {
		t.Fatalf("get secret %s: %v", name, err)
	}
	if v := sec.StringData["api-key"]; v != "" {
		return v
	}
	return string(sec.Data["api-key"])
}

func TestCreateAgentStoresNewProviderKeys(t *testing.T) {
	cases := []struct {
		provider string
		set      func(*SimpleAgentCreateRequest, *AgentAPIKeyInfo)
		resp     func(*SimpleAgentResponse) *AgentAPIKeyInfo
	}{
		{"openai", func(r *SimpleAgentCreateRequest, k *AgentAPIKeyInfo) { r.OpenAI = k }, func(r *SimpleAgentResponse) *AgentAPIKeyInfo { return r.OpenAI }},
		{"anthropic", func(r *SimpleAgentCreateRequest, k *AgentAPIKeyInfo) { r.Anthropic = k }, func(r *SimpleAgentResponse) *AgentAPIKeyInfo { return r.Anthropic }},
		{"google", func(r *SimpleAgentCreateRequest, k *AgentAPIKeyInfo) { r.Google = k }, func(r *SimpleAgentResponse) *AgentAPIKeyInfo { return r.Google }},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			s := providerTestServer(t)
			req := SimpleAgentCreateRequest{
				Name:     "agent",
				ImageRef: AgentImageRefInfo{Name: "img-1"},
				LLM:      AgentLLMInfo{Model: "m", Provider: c.provider},
			}
			c.set(&req, &AgentAPIKeyInfo{APIKey: "sk-" + c.provider})
			var created SimpleAgentResponse
			_ = json.Unmarshal(doAgentJSON(t, s, http.MethodPost, "/api/v1/agents", req, http.StatusCreated), &created)

			p, _ := llmprovider.Lookup(c.provider)
			secretName := created.ID + p.HubSecretSuffix
			if got := storedKey(t, s, secretName); got != "sk-"+c.provider {
				t.Errorf("stored key = %q, want sk-%s", got, c.provider)
			}
			ag := storedAgent(t, s, created.ID)
			cred, ok := p.Credential(&ag.Spec)
			if !ok || cred.APIKeySecretRef == nil || cred.APIKeySecretRef.Name != secretName || cred.APIKeySecretRef.Key != "api-key" {
				t.Errorf("spec block = %+v (present %v), want ref %s/api-key", cred, ok, secretName)
			}
			block := c.resp(&created)
			if block == nil || block.APIKey != "" {
				t.Errorf("response block = %+v, want present with no apiKey", block)
			}
		})
	}
}

func TestUpdateBlankKeyKeepsSecret(t *testing.T) {
	s := providerTestServer(t)
	var created SimpleAgentResponse
	_ = json.Unmarshal(doAgentJSON(t, s, http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name: "agent", ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:       AgentLLMInfo{Model: "m", Provider: "anthropic"},
		Anthropic: &AgentAPIKeyInfo{APIKey: "sk-original"},
	}, http.StatusCreated), &created)

	doAgentJSON(t, s, http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		Anthropic: &AgentAPIKeyInfo{APIKey: ""},
	}, http.StatusOK)

	if got := storedKey(t, s, created.ID+"-anthropic-key"); got != "sk-original" {
		t.Errorf("stored key = %q, want sk-original", got)
	}
	ag := storedAgent(t, s, created.ID)
	if ag.Spec.Anthropic == nil || ag.Spec.Anthropic.APIKeySecretRef == nil {
		t.Errorf("anthropic block ref lost: %+v", ag.Spec.Anthropic)
	}
}

func TestSwitchProviderKeepsOtherBlocks(t *testing.T) {
	s := providerTestServer(t)
	var created SimpleAgentResponse
	_ = json.Unmarshal(doAgentJSON(t, s, http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name: "agent", ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:         AgentLLMInfo{Model: "m", Provider: "ollama-cloud"},
		OllamaCloud: &AgentOllamaCloudInfo{APIKey: "sk-ollama"},
	}, http.StatusCreated), &created)

	doAgentJSON(t, s, http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		LLM:    &AgentLLMInfo{Model: "gpt-5", Provider: "openai"},
		OpenAI: &AgentAPIKeyInfo{APIKey: "sk-openai"},
	}, http.StatusOK)
	ag := storedAgent(t, s, created.ID)
	if ag.Spec.LLM.Provider != "openai" || ag.Spec.OpenAI == nil || ag.Spec.OllamaCloud == nil || ag.Spec.OllamaCloud.APIKeySecretRef == nil {
		t.Fatalf("after switch to openai: provider %q openAI %+v ollamaCloud %+v", ag.Spec.LLM.Provider, ag.Spec.OpenAI, ag.Spec.OllamaCloud)
	}

	doAgentJSON(t, s, http.MethodPut, "/api/v1/agents/"+created.ID, SimpleAgentUpdateRequest{
		LLM: &AgentLLMInfo{Model: "m", Provider: "ollama-cloud"},
	}, http.StatusOK)
	ag = storedAgent(t, s, created.ID)
	if ag.Spec.LLM.Provider != "ollama-cloud" || ag.Spec.OllamaCloud == nil || ag.Spec.OllamaCloud.APIKeySecretRef == nil || ag.Spec.OpenAI == nil {
		t.Fatalf("after switch back: provider %q ollamaCloud %+v openAI %+v", ag.Spec.LLM.Provider, ag.Spec.OllamaCloud, ag.Spec.OpenAI)
	}
	if got := storedKey(t, s, ag.Spec.OllamaCloud.APIKeySecretRef.Name); got != "sk-ollama" {
		t.Errorf("ollama key = %q, want sk-ollama", got)
	}
}

func TestLLMCheckNewProviders(t *testing.T) {
	cases := []struct {
		provider, override, wantBase string
		wantOK                       bool
	}{
		// Native-API providers probe their OpenAI-compatible endpoint; a block
		// baseUrl override points at the native API, so it is not used.
		{"openai", "https://proxy.example/v1", "https://api.openai.com/v1", true},
		{"anthropic", "https://proxy.example", "https://api.anthropic.com/v1", true},
		{"google", "", "https://generativelanguage.googleapis.com/v1beta/openai", true},
		// OpenAI-compatible providers keep honouring the override.
		{"ollama-cloud", "https://ollama.internal/v1", "https://ollama.internal/v1", true},
		{"ollama-cloud", "", "https://ollama.com/v1", true},
		// Custom cannot be checked from a bare pair.
		{"custom", "https://c.example/v1", "", false},
	}
	for _, c := range cases {
		t.Run(c.provider+"/"+c.override, func(t *testing.T) {
			a := llmCheckAgent("a", c.provider, "m", c.override, "a-key")
			base, ref, ok := llmCheckAgentTarget(a)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if base != c.wantBase || ref == nil || ref.Name != "a-key" {
				t.Errorf("target = %q ref %+v, want %q ref a-key", base, ref, c.wantBase)
			}
		})
	}
}
