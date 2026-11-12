// Package llmprovider is the single description of every LLM provider an
// Agent can use (spec.llm.provider). The operator, the hub and the console
// derive their per-provider behaviour from it — Pi models.json entries, key
// env vars, secret names, credential blocks, the LLM check — so adding a
// provider means one entry here plus its typed spec block, not a new branch
// in every component.
package llmprovider

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Pi API types written to models.json ("api"). Pi selects the wire protocol
// from this value.
const (
	PiAPIOpenAICompletions  = "openai-completions"
	PiAPIOpenAIResponses    = "openai-responses"
	PiAPIAnthropicMessages  = "anthropic-messages"
	PiAPIGoogleGenerativeAI = "google-generative-ai"
)

// Credential is a provider block's content, independent of which typed spec
// block holds it.
type Credential struct {
	APIKeySecretRef *corev1.SecretKeySelector
	// BaseURL is the block's baseUrl override; for custom it is
	// spec.customProvider.url.
	BaseURL string
}

// Provider describes one spec.llm.provider value.
type Provider struct {
	// ID is the spec.llm.provider value.
	ID    string
	Label string
	// PiName is the provider key in Pi's models.json and the --provider flag
	// value. For a PiBuiltIn provider it is Pi's own provider id; every other
	// provider uses a key no Pi built-in provider has, so its generated entry
	// cannot merge with one.
	PiName string
	// PiBuiltIn marks a provider Pi ships itself, with its own model catalog
	// (per-model reasoning, compat and thinking settings). The operator then
	// writes only the key, an optional baseUrl and per-model overrides from
	// the Agent spec, never a full model entry that would mask the catalog.
	PiBuiltIn bool
	// PiAPI is the Pi API type ("openai-completions", ...).
	PiAPI string
	// DefaultBaseURL is the built-in base URL; empty when the URL must come
	// from the block (custom).
	DefaultBaseURL string
	// APIKeyEnv is the env var the operator fills from the key secret and
	// models.json references as "$<APIKeyEnv>".
	APIKeyEnv string
	// HubSecretSuffix names the secret the hub stores a submitted key in:
	// <agent name><suffix>.
	HubSecretSuffix string
	// FallbackSecretSuffix names the secret the operator reads when the
	// block carries no apiKeySecretRef: <agent name><suffix>.
	FallbackSecretSuffix string
	// AlwaysRender keeps the provider's key env and secret-hash ref in every
	// agent pod, used or not. Only the providers that predate the registry
	// set it, so existing pod templates (and their secret hashes) do not
	// change on upgrade.
	AlwaysRender bool
	// Compat, ProviderCompat, ModelCompat and ThinkingLevelMap are optional
	// JSON fragments spliced into models.json. Compat carries its own
	// leading comma; the others are bare `"key":{...}` members.
	Compat           string
	ProviderCompat   string
	ModelCompat      string
	ThinkingLevelMap string
	// CheckBaseURL is the OpenAI-compatible base URL the hub's LLM check
	// probes (<base>/chat/completions with a Bearer key). Empty means the
	// provider cannot be checked from a bare (provider, model) pair.
	CheckBaseURL string
	// CheckTokenLimitField is the request field the LLM check caps the reply
	// with; empty means "max_tokens". OpenAI's reasoning models reject
	// max_tokens and need "max_completion_tokens".
	CheckTokenLimitField string
	// Credential returns the provider's block from spec; false when the
	// block is absent.
	Credential func(spec *v1alpha1.AgentSpec) (Credential, bool)
	// setBlock writes c into the provider's block as given, creating it if
	// absent. Callers use SetCredential, which keeps unset fields.
	setBlock func(spec *v1alpha1.AgentSpec, c Credential)
}

// SetCredential writes c into the provider's block, creating it if absent.
// Fields c leaves empty keep the block's current value, so storing a new key
// never drops a configured baseUrl — for a gateway agent that would silently
// send the agent, and its key, to the vendor endpoint instead.
func (p Provider) SetCredential(spec *v1alpha1.AgentSpec, c Credential) {
	if old, ok := p.Credential(spec); ok {
		if c.APIKeySecretRef == nil {
			c.APIKeySecretRef = old.APIKeySecretRef
		}
		if c.BaseURL == "" {
			c.BaseURL = old.BaseURL
		}
	}
	p.setBlock(spec, c)
}

// BaseURL returns the block's baseUrl override when set, else the default.
func (p Provider) BaseURL(spec *v1alpha1.AgentSpec) string {
	if c, ok := p.Credential(spec); ok && c.BaseURL != "" {
		return c.BaseURL
	}
	return p.DefaultBaseURL
}

var providers = []Provider{
	{
		ID:                   v1alpha1.AgentLLMProviderOllamaCloud,
		Label:                "Ollama Cloud",
		PiName:               "ollama-api-key",
		PiAPI:                PiAPIOpenAICompletions,
		DefaultBaseURL:       "https://ollama.com/v1",
		APIKeyEnv:            "OLLAMA_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-llm-key",
		FallbackSecretSuffix: "-ollama-key",
		AlwaysRender:         true,
		CheckBaseURL:         "https://ollama.com/v1",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.OllamaCloud == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.OllamaCloud.APIKeySecretRef, BaseURL: s.OllamaCloud.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.OllamaCloud = &v1alpha1.AgentOllamaCloud{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderOpenCode,
		Label:                "OpenCode",
		PiName:               "opencode",
		PiAPI:                PiAPIOpenAICompletions,
		DefaultBaseURL:       "https://opencode.ai/zen/go/v1",
		APIKeyEnv:            "OPENCODE_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-opencode-key",
		FallbackSecretSuffix: "-opencode-key",
		AlwaysRender:         true,
		Compat:               `,"compat":{"requiresReasoningContentOnAssistantMessages":true}`,
		CheckBaseURL:         "https://opencode.ai/zen/go/v1",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.OpenCode == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.OpenCode.APIKeySecretRef, BaseURL: s.OpenCode.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.OpenCode = &v1alpha1.AgentOpenCode{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderAlibabaCloud,
		Label:                "Alibaba Token Plan",
		PiName:               "alibaba-cloud",
		PiAPI:                PiAPIOpenAICompletions,
		DefaultBaseURL:       "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		APIKeyEnv:            "ALIBABA_CLOUD_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-alibaba-key",
		FallbackSecretSuffix: "-alibaba-key",
		AlwaysRender:         true,
		ProviderCompat:       `"compat":{"supportsDeveloperRole":false,"supportsUsageInStreaming":false,"maxTokensField":"max_tokens","supportsReasoningEffort":false}`,
		ModelCompat:          `"compat":{"thinkingFormat":"qwen"}`,
		ThinkingLevelMap:     `"thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":"medium","high":"high","xhigh":null}`,
		CheckBaseURL:         "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.AlibabaCloud == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.AlibabaCloud.APIKeySecretRef, BaseURL: s.AlibabaCloud.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.AlibabaCloud = &v1alpha1.AgentAlibabaCloud{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderOpenAI,
		Label:                "OpenAI",
		PiName:               "openai",
		PiBuiltIn:            true,
		PiAPI:                PiAPIOpenAIResponses,
		DefaultBaseURL:       "https://api.openai.com/v1",
		APIKeyEnv:            "OPENAI_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-openai-key",
		FallbackSecretSuffix: "-openai-key",
		CheckBaseURL:         "https://api.openai.com/v1",
		CheckTokenLimitField: "max_completion_tokens",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.OpenAI == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.OpenAI.APIKeySecretRef, BaseURL: s.OpenAI.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.OpenAI = &v1alpha1.AgentOpenAI{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderAnthropic,
		Label:                "Anthropic",
		PiName:               "anthropic",
		PiBuiltIn:            true,
		PiAPI:                PiAPIAnthropicMessages,
		DefaultBaseURL:       "https://api.anthropic.com",
		APIKeyEnv:            "ANTHROPIC_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-anthropic-key",
		FallbackSecretSuffix: "-anthropic-key",
		CheckBaseURL:         "https://api.anthropic.com/v1",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.Anthropic == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.Anthropic.APIKeySecretRef, BaseURL: s.Anthropic.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.Anthropic = &v1alpha1.AgentAnthropic{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderGoogle,
		Label:                "Google Gemini",
		PiName:               "google",
		PiBuiltIn:            true,
		PiAPI:                PiAPIGoogleGenerativeAI,
		DefaultBaseURL:       "https://generativelanguage.googleapis.com/v1beta",
		APIKeyEnv:            "GEMINI_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-google-key",
		FallbackSecretSuffix: "-google-key",
		CheckBaseURL:         "https://generativelanguage.googleapis.com/v1beta/openai",
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.Google == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.Google.APIKeySecretRef, BaseURL: s.Google.BaseURL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.Google = &v1alpha1.AgentGoogle{APIKeySecretRef: c.APIKeySecretRef, BaseURL: c.BaseURL}
		},
	},
	{
		ID:                   v1alpha1.AgentLLMProviderCustom,
		Label:                "Custom",
		PiName:               "ainsel-custom",
		PiAPI:                PiAPIOpenAICompletions,
		APIKeyEnv:            "CUSTOM_PROVIDER_API_KEY", // #nosec G101 -- env var name, not a hardcoded value
		HubSecretSuffix:      "-custom-provider-key",
		FallbackSecretSuffix: "-custom-provider-key",
		AlwaysRender:         true,
		Credential: func(s *v1alpha1.AgentSpec) (Credential, bool) {
			if s.CustomProvider == nil {
				return Credential{}, false
			}
			return Credential{APIKeySecretRef: s.CustomProvider.APIKeySecretRef, BaseURL: s.CustomProvider.URL}, true
		},
		setBlock: func(s *v1alpha1.AgentSpec, c Credential) {
			s.CustomProvider = &v1alpha1.AgentCustomProvider{APIKeySecretRef: c.APIKeySecretRef, URL: c.BaseURL}
		},
	},
}

// All returns every provider in display order. The slice is a copy.
func All() []Provider {
	return append([]Provider(nil), providers...)
}

// IDs returns every provider ID in All() order.
func IDs() []string {
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = p.ID
	}
	return ids
}

// Lookup returns the provider with the given ID.
func Lookup(id string) (Provider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}
