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

type AgentTools struct {
	HubURL     string
	HTTPClient *http.Client
}

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

func (a *AgentTools) UpdateAgentTool() mcp.Tool {
	return mcp.NewTool("update_agent",
		mcp.WithDescription("Update an agent's display name, description, or LLM runtime configuration (model, context window, image input). Only provided fields are changed; omitted fields are left unchanged. The agent ID (name argument) is the immutable identity; renames go through display_name."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent ID (e.g. a-director)")),
		mcp.WithString("display_name", mcp.Description("New user-facing agent name (e.g. agent-developer). Updates the agent's label and its channel name; does not change the agent ID.")),
		mcp.WithString("description", mcp.Description("New one-line description shown in the console and on the agent's channel")),
		mcp.WithString("model", mcp.Description("LLM model to use (e.g. unsloth/gpt-oss-120b-GGUF:F16, qwen3.5:cloud)")),
		mcp.WithNumber("max_turns", mcp.Description("Maximum turns per invocation")),
		mcp.WithNumber("temperature", mcp.Description("LLM temperature")),
		mcp.WithNumber("context_window", mcp.Description("Context window in tokens declared for the model. Defaults to 131072 (128k) on every provider; raise it only if the endpoint actually allows more. It must match the limit the endpoint enforces, not the model's paper limit: a declared window larger than the endpoint's real one silently disables compaction until the endpoint rejects the request, while a smaller one only makes compaction fire early. Minimum 1024; 0 is rejected rather than treated as a reset — omit the argument to leave the window alone (agents that never set it run on the 131072 default).")),
		mcp.WithBoolean("vision", mcp.Description("Whether pi may send image content (screenshots, image files read from disk) to the model. This is a switch, not a capability readout: the platform never probes the endpoint, so while it is false pi sends text even to a model that can see images, and while it is true a model that cannot see them will reject the request. Set it true only for models that accept images. Takes effect when the agent's pod next starts; omit the argument to leave it alone, and send false explicitly to turn image input off.")),
	)
}

func (a *AgentTools) UpdateAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

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

	payload := map[string]any{}
	if v, ok := args["display_name"].(string); ok && v != "" {
		payload["name"] = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		payload["description"] = v
	}
	if len(llm) > 0 {
		payload["llm"] = llm
	}
	if len(payload) == 0 {
		return mcp.NewToolResultError("at least one of display_name, description, model, max_turns, temperature, context_window, or vision must be provided"), nil
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
