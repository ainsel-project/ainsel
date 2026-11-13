package controller

import (
	"context"
	"encoding/json"
	"testing"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// renderModelsJSON runs the real models.json reconcile against a fake client
// and returns the generated document.
func renderModelsJSON(t *testing.T, a *ainselv1alpha1.Agent) string {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(jobModeScheme()).Build()
	r := &AgentReconciler{Client: c, Scheme: jobModeScheme()}
	if err := r.reconcilePiModelsConfigMap(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcilePiModelsConfigMap: %v", err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: a.Namespace, Name: "agent-" + a.Name + "-pi-models"}, &cm); err != nil {
		t.Fatalf("get models ConfigMap: %v", err)
	}
	return cm.Data["models.json"]
}

func providerAgent(provider string) *ainselv1alpha1.Agent {
	return &ainselv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "golden", Namespace: "default"},
		Spec:       ainselv1alpha1.AgentSpec{LLM: ainselv1alpha1.AgentLLM{Model: "m1", Provider: provider}},
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestLegacyProvidersRenderUnchanged pins what the providers that predate the
// llmprovider registry render, captured from main before the registry
// refactor: an upgrade without a spec change must not alter models.json, the
// key env vars or the secret-hash input (which would roll every agent pod).
func TestLegacyProvidersRenderUnchanged(t *testing.T) {
	golden := []struct {
		provider, models, env, hash string
	}{
		{
			provider: "ollama-cloud",
			models:   "{\n    \"providers\": {\n        \"ollama-api-key\": {\n            \"api\": \"openai-completions\",\n            \"apiKey\": \"$OLLAMA_API_KEY\",\n            \"baseUrl\": \"https://ollama.com/v1\",\n            \"models\": [\n                {\n                    \"id\": \"m1\",\n                    \"contextWindow\": 131072,\n                    \"input\": [\"text\"],\n                    \"reasoning\": true\n                }\n            ]\n        }\n    }\n}",
			env:      `[{"name":"OLLAMA_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-ollama-key","key":"api-key","optional":true}}},{"name":"OPENCODE_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-opencode-key","key":"api-key","optional":true}}},{"name":"ALIBABA_CLOUD_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-alibaba-key","key":"api-key","optional":true}}},{"name":"CUSTOM_PROVIDER_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-custom-provider-key","key":"api-key","optional":true}}}]`,
			hash:     `[{"name":"golden-ollama-key","key":"api-key"},{"name":"golden-opencode-key","key":"api-key"},{"name":"golden-alibaba-key","key":"api-key"},{"name":"golden-custom-provider-key","key":"api-key","optional":true}]`,
		},
		{
			provider: "opencode",
			models:   "{\n    \"providers\": {\n        \"opencode\": {\n            \"api\": \"openai-completions\",\n            \"apiKey\": \"$OPENCODE_API_KEY\",\n            \"baseUrl\": \"https://opencode.ai/zen/go/v1\",\n            \"models\": [\n                {\n                    \"id\": \"m1\",\n                    \"contextWindow\": 131072,\n                    \"input\": [\"text\"],\n                    \"reasoning\": true,\"compat\":{\"requiresReasoningContentOnAssistantMessages\":true}\n                }\n            ]\n        }\n    }\n}",
			env:      `[{"name":"OLLAMA_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-ollama-key","key":"api-key","optional":true}}},{"name":"OPENCODE_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-opencode-key","key":"api-key","optional":true}}},{"name":"ALIBABA_CLOUD_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-alibaba-key","key":"api-key","optional":true}}},{"name":"CUSTOM_PROVIDER_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-custom-provider-key","key":"api-key","optional":true}}}]`,
			hash:     `[{"name":"golden-ollama-key","key":"api-key"},{"name":"golden-opencode-key","key":"api-key"},{"name":"golden-alibaba-key","key":"api-key"},{"name":"golden-custom-provider-key","key":"api-key","optional":true}]`,
		},
		{
			provider: "alibaba-cloud",
			models:   "{\n    \"providers\": {\n        \"alibaba-cloud\": {\n            \"api\": \"openai-completions\",\n            \"apiKey\": \"$ALIBABA_CLOUD_API_KEY\",\n            \"baseUrl\": \"https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1\",\n            \"compat\":{\"supportsDeveloperRole\":false,\"supportsUsageInStreaming\":false,\"maxTokensField\":\"max_tokens\",\"supportsReasoningEffort\":false},\n            \"models\": [\n                {\n                    \"id\": \"m1\",\n                    \"contextWindow\": 131072,\n                    \"input\": [\"text\"],\n                    \"reasoning\": true,\n                    \"thinkingLevelMap\":{\"off\":null,\"minimal\":null,\"low\":null,\"medium\":\"medium\",\"high\":\"high\",\"xhigh\":null},\n                    \"compat\":{\"thinkingFormat\":\"qwen\"}\n                }\n            ]\n        }\n    }\n}",
			env:      `[{"name":"OLLAMA_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-ollama-key","key":"api-key","optional":true}}},{"name":"OPENCODE_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-opencode-key","key":"api-key","optional":true}}},{"name":"ALIBABA_CLOUD_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-alibaba-key","key":"api-key","optional":true}}},{"name":"CUSTOM_PROVIDER_API_KEY","valueFrom":{"secretKeyRef":{"name":"golden-custom-provider-key","key":"api-key","optional":true}}}]`,
			hash:     `[{"name":"golden-ollama-key","key":"api-key"},{"name":"golden-opencode-key","key":"api-key"},{"name":"golden-alibaba-key","key":"api-key"},{"name":"golden-custom-provider-key","key":"api-key","optional":true}]`,
		},
	}
	for _, g := range golden {
		t.Run(g.provider, func(t *testing.T) {
			a := providerAgent(g.provider)
			if got := renderModelsJSON(t, a); got != g.models {
				t.Errorf("models.json changed:\ngot:\n%s\nwant:\n%s", got, g.models)
			}
			if got := mustJSON(t, llmProviderEnv(a)); got != g.env {
				t.Errorf("provider env changed:\ngot:  %s\nwant: %s", got, g.env)
			}
			if got := mustJSON(t, llmSecretHashRefs(a)); got != g.hash {
				t.Errorf("secret hash refs changed:\ngot:  %s\nwant: %s", got, g.hash)
			}
		})
	}
}

func modelsProviders(t *testing.T, doc string) map[string]map[string]any {
	t.Helper()
	var m struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("models.json is not valid JSON: %v\n%s", err, doc)
	}
	return m.Providers
}

func envByName(env []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			return &env[i]
		}
	}
	return nil
}

// Custom no longer claims Pi's built-in "openai" provider key; PI_PROVIDER
// and the models.json key must move together or every custom agent breaks.
func TestCustomProviderUsesAinselCustomKey(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderCustom)
	a.Spec.CustomProvider = &ainselv1alpha1.AgentCustomProvider{URL: "https://c.example/v1"}
	providers := modelsProviders(t, renderModelsJSON(t, a))
	if _, ok := providers["ainsel-custom"]; !ok || len(providers) != 1 {
		t.Fatalf("models.json providers = %v, want exactly ainsel-custom", providers)
	}
	env, err := buildAgentEnv(context.Background(), a, "agent-golden", &ainselv1alpha1.AgentImage{}, &podPlan{})
	if err != nil {
		t.Fatal(err)
	}
	if e := envByName(env, "PI_PROVIDER"); e == nil || e.Value != "ainsel-custom" {
		t.Fatalf("PI_PROVIDER = %+v, want ainsel-custom", e)
	}
}

// OpenAI, Anthropic and Google run on Pi's built-in providers: models.json
// only supplies the key, an optional baseUrl and per-model overrides, so Pi's
// own catalog keeps each model's reasoning, compat and thinking settings.
func TestNewProvidersModelsJSON(t *testing.T) {
	vision := true
	window := 400000
	cases := []struct{ provider, keyRef string }{
		{"openai", "$OPENAI_API_KEY"},
		{"anthropic", "$ANTHROPIC_API_KEY"},
		{"google", "$GEMINI_API_KEY"},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			a := providerAgent(c.provider)
			a.Spec.LLM.Vision = &vision
			a.Spec.LLM.ContextWindow = &window
			a.Spec.LLM.Cost = &ainselv1alpha1.AgentLLMCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
			providers := modelsProviders(t, renderModelsJSON(t, a))
			p, ok := providers[c.provider]
			if !ok || len(providers) != 1 {
				t.Fatalf("models.json providers = %v, want exactly Pi's built-in %s", providers, c.provider)
			}
			if p["apiKey"] != c.keyRef {
				t.Errorf("apiKey = %v, want %s", p["apiKey"], c.keyRef)
			}
			for _, k := range []string{"api", "models", "baseUrl"} {
				if _, set := p[k]; set {
					t.Errorf("%s set (%v): it would replace Pi's built-in provider data", k, p[k])
				}
			}
			over := p["modelOverrides"].(map[string]any)["m1"].(map[string]any)
			if over["contextWindow"] != float64(window) {
				t.Errorf("contextWindow override = %v, want %d", over["contextWindow"], window)
			}
			if mustJSON(t, over["input"]) != `["text","image"]` {
				t.Errorf("input override = %v, want text+image", over["input"])
			}
			if mustJSON(t, over["cost"]) != `{"cacheRead":0.3,"cacheWrite":3.75,"input":3,"output":15}` {
				t.Errorf("cost override = %v", over["cost"])
			}
		})
	}
}

// Without spec overrides Pi's catalog values stand: no default context
// window is forced onto a built-in model, only the vision choice.
func TestBuiltInProviderKeepsCatalogDefaults(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderAnthropic)
	a.Spec.Anthropic = &ainselv1alpha1.AgentAnthropic{BaseURL: "https://gw.example"}
	temp := 0.2
	a.Spec.LLM.Temperature = &temp
	doc := renderModelsJSON(t, a)
	p := modelsProviders(t, doc)["anthropic"]
	if p["baseUrl"] != "https://gw.example" {
		t.Errorf("baseUrl = %v, want the block override", p["baseUrl"])
	}
	over := p["modelOverrides"].(map[string]any)["m1"].(map[string]any)
	if _, set := over["contextWindow"]; set {
		t.Errorf("contextWindow forced to %v; Pi's catalog value must stand", over["contextWindow"])
	}
	if mustJSON(t, over["input"]) != `["text"]` {
		t.Errorf("input override = %v, want text only", over["input"])
	}
	// Temperature is only injected for OpenAI-compatible payloads.
	var root map[string]any
	_ = json.Unmarshal([]byte(doc), &root)
	if _, set := root["ainsel"]; set {
		t.Errorf("ainsel temperature block emitted for a native-API provider: %v", root["ainsel"])
	}
}

// A provider selected without its block (e.g. created via kubectl) still gets
// its key env from the fallback secret, optional, like the legacy providers.
func TestSelectedProviderWithoutBlockGetsOptionalEnv(t *testing.T) {
	e := envByName(llmProviderEnv(providerAgent(ainselv1alpha1.AgentLLMProviderGoogle)), "GEMINI_API_KEY")
	if e == nil || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("GEMINI_API_KEY = %+v, want a secret ref", e)
	}
	ref := e.ValueFrom.SecretKeyRef
	if ref.Name != "golden-google-key" || ref.Key != "api-key" || ref.Optional == nil || !*ref.Optional {
		t.Errorf("GEMINI_API_KEY ref = %+v, want golden-google-key/api-key optional", ref)
	}
}

func TestNewProviderBlockRendersEnvAndHash(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOllamaCloud)
	a.Spec.Anthropic = &ainselv1alpha1.AgentAnthropic{APIKeySecretRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "my-anthropic"}, Key: "api-key"}}
	e := envByName(llmProviderEnv(a), "ANTHROPIC_API_KEY")
	if e == nil || e.ValueFrom.SecretKeyRef.Name != "my-anthropic" || (e.ValueFrom.SecretKeyRef.Optional != nil && *e.ValueFrom.SecretKeyRef.Optional) {
		t.Fatalf("ANTHROPIC_API_KEY = %+v, want my-anthropic, not optional", e)
	}
	refs := llmSecretHashRefs(a)
	if last := refs[len(refs)-1]; len(refs) != 5 || last.Name != "my-anthropic" {
		t.Errorf("hash refs = %+v, want the four legacy refs then my-anthropic", refs)
	}
}

func TestUnusedNewProviderNotRendered(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOllamaCloud)
	env := llmProviderEnv(a)
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY"} {
		if envByName(env, name) != nil {
			t.Errorf("%s rendered for an agent that does not use it", name)
		}
	}
	if n := len(llmSecretHashRefs(a)); n != 4 {
		t.Errorf("hash refs = %d, want the 4 legacy refs only", n)
	}
}

func TestReferencesSecretNewBlocks(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderAnthropic)
	a.Spec.Anthropic = &ainselv1alpha1.AgentAnthropic{APIKeySecretRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "golden-anthropic-key"}, Key: "api-key"}}
	if !referencesSecret(*a, "golden-anthropic-key") {
		t.Error("referencesSecret ignores the anthropic block's secret")
	}
}

// A new-provider block without a stored key (e.g. only a gateway baseUrl)
// must not make the pod wait on a secret that does not exist yet.
func TestNewProviderBlockWithoutKeyIsOptional(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)
	a.Spec.OpenAI = &ainselv1alpha1.AgentOpenAI{BaseURL: "https://gw.example/v1"}
	e := envByName(llmProviderEnv(a), "OPENAI_API_KEY")
	if e == nil || e.ValueFrom.SecretKeyRef.Name != "golden-openai-key" {
		t.Fatalf("OPENAI_API_KEY = %+v, want fallback secret golden-openai-key", e)
	}
	if o := e.ValueFrom.SecretKeyRef.Optional; o == nil || !*o {
		t.Errorf("OPENAI_API_KEY optional = %v, want true while no key is stored", o)
	}
}

func TestTemperatureCondition(t *testing.T) {
	temp := 0.3
	cases := []struct {
		provider   string
		temp       *float64
		wantNil    bool
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{provider: "ollama-cloud", wantNil: true},
		// Nothing is sent for an agent whose provider is unset or unknown
		// (the reconcile fails with InvalidLLMProvider), so no claim is made.
		{provider: "", temp: &temp, wantNil: true},
		{provider: "ollama", temp: &temp, wantNil: true},
		{provider: "ollama-cloud", temp: &temp, wantStatus: metav1.ConditionTrue, wantReason: "Applied"},
		{provider: "anthropic", temp: &temp, wantStatus: metav1.ConditionFalse, wantReason: "TemperatureNotSupported"},
		{provider: "openai", temp: &temp, wantStatus: metav1.ConditionFalse, wantReason: "TemperatureNotSupported"},
	}
	for _, c := range cases {
		a := providerAgent(c.provider)
		a.Spec.LLM.Temperature = c.temp
		got := temperatureCondition(a)
		if c.wantNil {
			if got != nil {
				t.Errorf("%s without temperature: condition %+v, want none", c.provider, got)
			}
			continue
		}
		if got == nil || got.Type != ainselv1alpha1.AgentConditionTemperatureApplied || got.Status != c.wantStatus || got.Reason != c.wantReason {
			t.Errorf("%s: condition %+v, want %s/%s", c.provider, got, c.wantStatus, c.wantReason)
		}
	}
}
