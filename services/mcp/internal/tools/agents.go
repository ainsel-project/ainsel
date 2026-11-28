package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// minContextWindow mirrors the kubebuilder minimum on spec.llm.contextWindow.
// The hub writes the value straight onto the Agent CR, so anything smaller is
// rejected by the API server with an opaque error; catch it here instead.
const minContextWindow = 1024

// AgentTools wraps the hub's /api/v1/agents endpoints so an MCP client can
// list agents, inspect one, and update it.
type AgentTools struct {
	HubURL     string
	HTTPClient *http.Client
}

// NewAgentTools returns AgentTools pointed at hubURL, with a 10 s request
// timeout.
func NewAgentTools(hubURL string) *AgentTools {
	return &AgentTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *AgentTools) ListAgentsTool() mcp.Tool {
	return mcp.NewTool("list_agents",
		mcp.WithDescription("List agents in the Ainsel platform with their status. Default page size: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1)")),
		mcp.WithNumber("pageSize", mcp.Description("Page size (default: 50)")),
	)
}

func (a *AgentTools) GetAgentTool() mcp.Tool {
	return mcp.NewTool("get_agent",
		mcp.WithDescription("Get detailed information about a specific agent including config, persona, and pod state"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent name")),
	)
}

func (a *AgentTools) ListAgents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	q := url.Values{}
	q.Set("page", strconv.Itoa(defaultArgInt(args, "page", 1)))
	q.Set("pageSize", strconv.Itoa(defaultArgInt(args, "pageSize", 50)))
	body, err := hubGet(ctx, a.HTTPClient, a.HubURL, "/api/v1/agents?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list agents: %v", err)), nil
	}
	body = annotatePageMeta(body, "more agents available — pass page=N to fetch the next page")
	return mcp.NewToolResultText(string(body)), nil
}

func (a *AgentTools) GetAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := req.Params.Arguments.(map[string]any)["name"].(string)
	if !ok || name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	body, err := hubGet(ctx, a.HTTPClient, a.HubURL, "/api/v1/agents/"+name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get agent %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// agentEnvEntry is one environment override in an agent create/update
// request, mirroring the hub's {name, value, secret} shape. The hub never
// returns secret values (they read back as ""), and an entry for a
// currently-secret name resent with secret: true and an empty value keeps
// the stored value — the tool passes the list through unchanged so that
// keep-on-empty rule stays the hub's single definition.
type agentEnvEntry struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// parseJSONArg decodes a JSON-string tool argument into out. MCP clients
// pass structured arguments as JSON strings here, matching the convention
// of the other write tools (create_trigger filters, update_agent_image
// env/tools).
func parseJSONArg[T any](args map[string]any, key string) (*T, string) {
	s, ok := args[key].(string)
	if !ok || s == "" {
		return nil, ""
	}
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Sprintf("invalid %s JSON: %v", key, err)
	}
	return &v, ""
}

func (a *AgentTools) CreateAgentTool() mcp.Tool {
	return mcp.NewTool("create_agent",
		mcp.WithDescription("Create a new agent. The hub assigns the agent ID (a-prefixed) and returns it in the response; the name argument is the display name. Requires an existing agent image (list_agent_images) and, on hubs with access control enabled, a groupId the caller has write access to. Secrets (env values, provider API keys) are stored as Kubernetes secrets and are never returned by any tool."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Display name for the new agent (e.g. code-reviewer)")),
		mcp.WithString("groupId", mcp.Description("Group to assign the agent to; must be a group the caller has write access to. Required on hubs with access control enabled.")),
		mcp.WithString("image", mcp.Required(), mcp.Description("Agent image name to base the agent on (e.g. pi)")),
		mcp.WithString("model", mcp.Required(), mcp.Description("LLM model to use (e.g. unsloth/gpt-oss-120b-GGUF:F16, qwen3.5:cloud)")),
		mcp.WithString("provider", mcp.Required(), mcp.Description("LLM provider: one of ollama-cloud, opencode, alibaba-cloud, openai, anthropic, google, custom. The provider's API key normally comes from the matching *_api_key argument.")),
		mcp.WithString("description", mcp.Description("One-line description shown in the console and on the agent's channel")),
		mcp.WithNumber("max_turns", mcp.Description("Maximum turns per invocation")),
		mcp.WithNumber("temperature", mcp.Description("LLM temperature")),
		mcp.WithNumber("context_window", mcp.Description("Context window in tokens declared for the model. Defaults to 131072 (128k); it must match the limit the endpoint enforces, not the model's paper limit. Minimum 1024.")),
		mcp.WithBoolean("vision", mcp.Description("Whether pi may send image content to the model. The platform never probes the endpoint; set true only for models that accept images.")),
		mcp.WithString("model_license", mcp.Description("Licence the model arrives under, as reviewed by whoever set it (docs/model-licensing.md). Stored verbatim; the hub never verifies it.")),
		mcp.WithString("persona_id", mcp.Description("ID of an existing persona template (list_personas). Mutually exclusive with persona_text.")),
		mcp.WithString("persona_text", mcp.Description("Inline persona instructions; the hub stores them as a persona owned by the new agent. Mutually exclusive with persona_id.")),
		mcp.WithString("enabled_tools", mcp.Description("JSON array of tool names the agent may use: each one exactly as declared on the image (AgentImage.spec.tools[].name), or mcp__<server> for a whole MCP server the agent effectively has. This is a grant — omitting it, or sending [], leaves the agent with no tools at all.")),
		mcp.WithString("skills", mcp.Description("JSON array of skill IDs (list_skills). Skills are chosen per agent: omitted or [] means the agent runs with no skills.")),
		mcp.WithString("mcp_servers", mcp.Description("JSON array of MCP server registry names (list_mcp_servers). Each name must exist and its tokenFromEnv must name a variable the operator actually injects — a created agent whose token cannot resolve fails at runtime. Omit to inherit the image's mcpServers; [] connects to none.")),
		mcp.WithString("env", mcp.Description("JSON array of {name, value, secret} environment overrides on top of the image's env. Set secret to true to store the value as sensitive; a secret entry requires a non-empty value on create.")),
		mcp.WithNumber("replicas", mcp.Description("Max number of task jobs running at once for this agent. 0 stops task delivery; queued tasks stay queued and drain in id order when the cap goes back up.")),
		mcp.WithString("working_hours", mcp.Description("JSON object {timezone, days, start, end}: weekly window inside which the hub hands this agent work. timezone is an IANA zone (e.g. Europe/Berlin); days are cron weekday numbers (0/7 Sunday); start/end are \"HH:MM\", an end earlier than start closes next day.")),
		mcp.WithString("memory", mcp.Description("JSON object {enabled, provider} for the agent's memory backend.")),
		mcp.WithString("ollama_cloud_api_key", mcp.Description("Ollama Cloud API key; stored as a secret, never returned.")),
		mcp.WithString("opencode_api_key", mcp.Description("OpenCode API key; stored as a secret, never returned.")),
		mcp.WithString("alibaba_cloud_api_key", mcp.Description("Alibaba Token Plan API key; stored as a secret, never returned.")),
		mcp.WithString("openai_api_key", mcp.Description("OpenAI API key; stored as a secret, never returned. Not combinable with openai_auth_method: ChatGPTSignIn.")),
		mcp.WithString("openai_auth_method", mcp.Description("OpenAI auth method: APIKey (default) or ChatGPTSignIn. ChatGPT sign-in runs one task at a time (replicas > 1 is rejected).")),
		mcp.WithString("anthropic_api_key", mcp.Description("Anthropic API key; stored as a secret, never returned.")),
		mcp.WithString("google_api_key", mcp.Description("Google API key; stored as a secret, never returned.")),
		mcp.WithString("custom_provider_url", mcp.Description("Custom LLM API base URL (required when provider is custom)")),
		mcp.WithString("custom_provider_api_key", mcp.Description("Custom provider API key; stored as a secret, never returned.")),
	)
}

func (a *AgentTools) CreateAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	image, _ := args["image"].(string)
	model, _ := args["model"].(string)
	provider, _ := args["provider"].(string)
	if name == "" || image == "" || model == "" || provider == "" {
		return mcp.NewToolResultError("name, image, model, and provider are required"), nil
	}
	personaID, _ := args["persona_id"].(string)
	personaText, _ := args["persona_text"].(string)
	if personaID != "" && personaText != "" {
		return mcp.NewToolResultError("persona: set either persona_id or persona_text, not both"), nil
	}

	payload := map[string]any{
		"name":     name,
		"imageRef": map[string]any{"name": image},
		"llm":      map[string]any{"model": model, "provider": provider},
	}
	llm := payload["llm"].(map[string]any)
	if v, ok := args["max_turns"].(float64); ok && v != 0 {
		llm["maxTurns"] = int(v)
	}
	if v, ok := args["temperature"].(float64); ok {
		llm["temperature"] = v
	}
	if v, ok := args["context_window"].(float64); ok {
		if v < minContextWindow {
			return mcp.NewToolResultError(fmt.Sprintf("context_window must be at least %d tokens; omit it to keep the 131072 default", minContextWindow)), nil
		}
		llm["contextWindow"] = int(v)
	}
	if v, ok := args["vision"].(bool); ok {
		llm["vision"] = v
	}
	if v, _ := args["model_license"].(string); v != "" {
		llm["modelLicense"] = v
	}
	if v, _ := args["description"].(string); v != "" {
		payload["description"] = v
	}
	if v, _ := args["groupId"].(string); v != "" {
		payload["groupId"] = v
	}
	if personaID != "" {
		payload["persona"] = map[string]any{"id": personaID}
	} else if personaText != "" {
		payload["persona"] = map[string]any{"text": personaText}
	}

	for _, key := range []string{"enabled_tools", "skills", "mcp_servers"} {
		list, errMsg := parseJSONArg[[]string](args, key)
		if errMsg != "" {
			return mcp.NewToolResultError(errMsg), nil
		}
		if list != nil {
			switch key {
			case "enabled_tools":
				payload["enabledTools"] = *list
			case "skills":
				payload["skills"] = *list
			default:
				payload["mcp"] = map[string]any{"servers": *list}
			}
		}
	}

	env, errMsg := parseJSONArg[[]agentEnvEntry](args, "env")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if env != nil {
		for _, e := range *env {
			if e.Secret && e.Value == "" {
				return mcp.NewToolResultError(fmt.Sprintf("secret env var %s requires a non-empty value on create: there is no stored value to preserve", e.Name)), nil
			}
		}
		payload["env"] = *env
	}

	if v, ok := args["replicas"].(float64); ok {
		payload["replicas"] = int(v)
	}
	wh, errMsg := parseJSONArg[map[string]any](args, "working_hours")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if wh != nil {
		payload["workingHours"] = *wh
	}
	mem, errMsg := parseJSONArg[map[string]any](args, "memory")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if mem != nil {
		payload["memory"] = *mem
	}

	if r := applyProviderKeys(payload, args); r != nil {
		return r, nil
	}

	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPost(ctx, a.HTTPClient, a.HubURL, "/api/v1/agents", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create agent: %v", appendGroupIDHint(err))), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

// applyProviderKeys adds the provider credential blocks the caller supplied
// to an agent create/update payload, mirroring the hub's request shape. An
// empty key string is treated as absent: the hub already reads a blank key
// as "leave the stored key alone", so there is nothing to send. Returns a
// tool error result when the arguments contradict each other, nil otherwise.
func applyProviderKeys(payload map[string]any, args map[string]any) *mcp.CallToolResult {
	block := func(arg, field string) {
		if v, _ := args[arg].(string); v != "" {
			payload[field] = map[string]any{"apiKey": v}
		}
	}
	block("ollama_cloud_api_key", "ollamaCloud")
	block("opencode_api_key", "openCode")
	block("alibaba_cloud_api_key", "alibabaCloud")
	block("anthropic_api_key", "anthropic")
	block("google_api_key", "google")

	openAIKey, _ := args["openai_api_key"].(string)
	openAIAuth, _ := args["openai_auth_method"].(string)
	if openAIAuth != "" && openAIAuth != "APIKey" && openAIAuth != "ChatGPTSignIn" {
		return mcp.NewToolResultError("openai_auth_method must be APIKey or ChatGPTSignIn")
	}
	if openAIAuth == "ChatGPTSignIn" && openAIKey != "" {
		return mcp.NewToolResultError("openAI: send an API key or choose ChatGPT sign-in, not both")
	}
	if openAIKey != "" || openAIAuth != "" {
		b := map[string]any{}
		if openAIKey != "" {
			b["apiKey"] = openAIKey
		}
		if openAIAuth != "" {
			b["authMethod"] = openAIAuth
		}
		payload["openAI"] = b
	}

	customURL, _ := args["custom_provider_url"].(string)
	customKey, _ := args["custom_provider_api_key"].(string)
	if customURL != "" || customKey != "" {
		if customURL == "" {
			return mcp.NewToolResultError("custom_provider_url is required whenever a custom provider API key is set")
		}
		b := map[string]any{"url": customURL}
		if customKey != "" {
			b["apiKey"] = customKey
		}
		payload["customProvider"] = b
	}
	return nil
}

func (a *AgentTools) DeleteAgentTool() mcp.Tool {
	return mcp.NewTool("delete_agent",
		mcp.WithDescription("Delete an agent. This is final: the agent, its pods and its task queue go away, and the agent's owned persona is reclaimed. Agents managed by an external system (managedBy, e.g. GitOps) are refused with 409. The hub's 404/403 pass through unchanged."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent ID (e.g. a-director) — exact match, no fuzzy lookup")),
	)
}

func (a *AgentTools) DeleteAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	if _, err := hubDelete(ctx, a.HTTPClient, a.HubURL, "/api/v1/agents/"+name); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete agent %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("agent %s deleted", name)), nil
}

func (a *AgentTools) UpdateAgentTool() mcp.Tool {
	return mcp.NewTool("update_agent",
		mcp.WithDescription("Update an agent. Omitted arguments are left unchanged; an argument that is present is applied, including empty strings and empty arrays where the hub defines them as clearing (description, skills, env, working_hours: {}). Secrets are accepted on write and never echoed back: an env entry for a currently-secret name sent as {name, value: \"\", secret: true} keeps the stored value, and a provider *_api_key argument with a non-empty value rotates the key. The agent ID (name argument) is the immutable identity; renames go through display_name. replicas: 0 stops task delivery; queued tasks stay queued and drain in id order when the cap goes back up."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent ID (e.g. a-director)")),
		mcp.WithString("display_name", mcp.Description("New user-facing agent name (e.g. agent-developer). Updates the agent's label and its channel name; does not change the agent ID.")),
		mcp.WithString("description", mcp.Description("New one-line description shown in the console and on the agent's channel")),
		mcp.WithString("model", mcp.Description("LLM model to use (e.g. unsloth/gpt-oss-120b-GGUF:F16, qwen3.5:cloud)")),
		mcp.WithNumber("max_turns", mcp.Description("Maximum turns per invocation")),
		mcp.WithNumber("temperature", mcp.Description("LLM temperature")),
		mcp.WithNumber("context_window", mcp.Description("Context window in tokens declared for the model. Defaults to 131072 (128k) on every provider; raise it only if the endpoint actually allows more. It must match the limit the endpoint enforces, not the model's paper limit: a declared window larger than the endpoint's real one silently disables compaction until the endpoint rejects the request, while a smaller one only makes compaction fire early. Minimum 1024; 0 is rejected rather than treated as a reset — omit the argument to leave the window alone (agents that never set it run on the 131072 default).")),
		mcp.WithBoolean("vision", mcp.Description("Whether pi may send image content (screenshots, image files read from disk) to the model. This is a switch, not a capability readout: the platform never probes the endpoint, so while it is false pi sends text even to a model that can see images, and while it is true a model that cannot see them will reject the request. Set it true only for models that accept images. Takes effect when the agent's pod next starts; omit the argument to leave it alone, and send false explicitly to turn image input off.")),
		mcp.WithString("model_license", mcp.Description("Licence the model arrives under, as reviewed by whoever set it (docs/model-licensing.md). Stored verbatim; the hub never verifies it.")),
		mcp.WithString("image", mcp.Description("New agent image name (e.g. pi). Changing it revalidates enabled_tools against the new image's declared tools.")),
		mcp.WithString("persona_id", mcp.Description("ID of a persona template to point the agent at (list_personas). Re-pointing away from the agent's own persona reclaims it. Omit to leave the persona alone.")),
		mcp.WithString("enabled_tools", mcp.Description("JSON array of tool names the agent may use: each one exactly as declared on the image (AgentImage.spec.tools[].name), or mcp__<server> for a whole MCP server the agent effectively has. Replaces the full grant; [] leaves the agent with no tools; omit to preserve.")),
		mcp.WithString("skills", mcp.Description("JSON array of skill IDs (list_skills). Replaces the full list: omit to preserve, [] to clear (the agent then runs with no skills).")),
		mcp.WithString("mcp_servers", mcp.Description("JSON array of MCP server registry names (list_mcp_servers); each must exist and its tokenFromEnv must name a variable the operator actually injects. Replaces the selection: omit to keep inheriting the image's mcpServers, [] to connect to none.")),
		mcp.WithString("env", mcp.Description("JSON array of {name, value, secret} environment overrides. Replaces the full list: omit to preserve, [] to clear (the agent runs on the image's env alone). The API never returns secret values (they read back as \"\"), so resend a secret entry as {name, value: \"\", secret: true} to keep the stored value; an entry without secret: true on a secret name blanks it. Rotating a secret sends a non-empty value.")),
		mcp.WithNumber("replicas", mcp.Description("Max number of task jobs running at once for this agent. 0 stops task delivery; queued tasks stay queued and drain in id order when the cap goes back up.")),
		mcp.WithString("working_hours", mcp.Description("JSON object {timezone, days, start, end}: weekly window inside which the hub hands this agent work (timezone an IANA zone; days are cron weekday numbers, 0/7 Sunday; start/end \"HH:MM\", an end earlier than start closes next day). Send {} to clear the window.")),
		mcp.WithString("memory", mcp.Description("JSON object {enabled, provider} for the agent's memory backend.")),
		mcp.WithString("ollama_cloud_api_key", mcp.Description("Rotate the Ollama Cloud API key; stored as a secret, never returned. Omit to keep the stored key.")),
		mcp.WithString("opencode_api_key", mcp.Description("Rotate the OpenCode API key; stored as a secret, never returned.")),
		mcp.WithString("alibaba_cloud_api_key", mcp.Description("Rotate the Alibaba Token Plan API key; stored as a secret, never returned.")),
		mcp.WithString("openai_api_key", mcp.Description("Rotate the OpenAI API key; stored as a secret, never returned. Not combinable with openai_auth_method: ChatGPTSignIn.")),
		mcp.WithString("openai_auth_method", mcp.Description("OpenAI auth method: APIKey or ChatGPTSignIn. ChatGPT sign-in runs one task at a time (replicas > 1 is rejected).")),
		mcp.WithString("anthropic_api_key", mcp.Description("Rotate the Anthropic API key; stored as a secret, never returned.")),
		mcp.WithString("google_api_key", mcp.Description("Rotate the Google API key; stored as a secret, never returned.")),
		mcp.WithString("custom_provider_url", mcp.Description("Custom LLM API base URL. Required when the agent has no custom provider yet and a custom_provider_api_key is sent.")),
		mcp.WithString("custom_provider_api_key", mcp.Description("Rotate the custom provider API key; stored as a secret, never returned.")),
	)
}

func (a *AgentTools) UpdateAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	// Presence-based emission throughout: an argument that is absent is not
	// sent (the hub reads an absent key as "leave unchanged"), while an
	// argument that is present is sent even when it is a zero value the hub
	// defines as a real change (vision: false, skills: [], working_hours: {}).
	llm := map[string]any{}
	if v, ok := args["model"].(string); ok && v != "" {
		llm["model"] = v
	}
	if v, ok := args["max_turns"].(float64); ok && v != 0 {
		llm["maxTurns"] = int(v)
	}
	if v, ok := args["temperature"].(float64); ok {
		llm["temperature"] = v
	}
	// Unlike max_turns, an explicit 0 is rejected rather than ignored: the CRD
	// schema minimum is 1024, so a 0 written through the hub would fail the
	// API-server validation. Omitting the argument is how the caller leaves
	// the window alone (agents that never set it run on the 131072 default).
	if v, ok := args["context_window"].(float64); ok {
		if v < minContextWindow {
			return mcp.NewToolResultError(fmt.Sprintf("context_window must be at least %d tokens; omit it to keep the 131072 default", minContextWindow)), nil
		}
		llm["contextWindow"] = int(v)
	}
	// Tri-state at the tool boundary: absent leaves the flag alone, and an
	// explicit false is a real change — the hub reads llm.vision as a pointer,
	// so dropping false here would leave image input stuck on.
	if v, ok := args["vision"].(bool); ok {
		llm["vision"] = v
	}
	if v, _ := args["model_license"].(string); v != "" {
		llm["modelLicense"] = v
	}

	payload := map[string]any{}
	// Presence-based for strings too: an explicit "" clears the field on the
	// hub (the update request is all pointers), which is the only way to
	// empty a description from MCP.
	if v, ok := args["display_name"].(string); ok {
		payload["name"] = v
	}
	if v, ok := args["description"].(string); ok {
		payload["description"] = v
	}
	if len(llm) > 0 {
		payload["llm"] = llm
	}
	if v, _ := args["image"].(string); v != "" {
		payload["imageRef"] = map[string]any{"name": v}
	}
	if v, _ := args["persona_id"].(string); v != "" {
		payload["persona"] = map[string]any{"id": v}
	}
	for _, key := range []string{"enabled_tools", "skills", "mcp_servers"} {
		list, errMsg := parseJSONArg[[]string](args, key)
		if errMsg != "" {
			return mcp.NewToolResultError(errMsg), nil
		}
		if list != nil {
			switch key {
			case "enabled_tools":
				payload["enabledTools"] = *list
			case "skills":
				payload["skills"] = *list
			default:
				// Non-nil means an explicit selection, even when empty: []
				// connects to none, while omitting the key inherits the image's
				// mcpServers.
				payload["mcp"] = map[string]any{"servers": *list}
			}
		}
	}
	if env, errMsg := parseJSONArg[[]agentEnvEntry](args, "env"); errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	} else if env != nil {
		// Passed through as given: the hub keeps the stored value of a secret
		// entry resent with an empty value and secret: true, and an empty list
		// clears the overrides back to the image's env.
		payload["env"] = *env
	}
	if v, ok := args["replicas"].(float64); ok {
		payload["replicas"] = int(v)
	}
	if wh, errMsg := parseJSONArg[map[string]any](args, "working_hours"); errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	} else if wh != nil {
		// A present object is sent even when empty: the hub reads {} as
		// "clear the window".
		payload["workingHours"] = *wh
	}
	if mem, errMsg := parseJSONArg[map[string]any](args, "memory"); errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	} else if mem != nil {
		payload["memory"] = *mem
	}
	if r := applyProviderKeys(payload, args); r != nil {
		return r, nil
	}
	if len(payload) == 0 {
		return mcp.NewToolResultError("at least one field must be provided (see the tool parameters); omit arguments to leave those fields unchanged"), nil
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPut(ctx, a.HTTPClient, a.HubURL, "/api/v1/agents/"+name, bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update agent %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}
