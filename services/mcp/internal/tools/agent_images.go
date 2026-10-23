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

// AgentImageTools wraps the hub's /api/v1/agent-images endpoints so an MCP
// client can ask which agent runtime images (and their tool / model catalogues)
// are available.
type AgentImageTools struct {
	HubURL     string
	HTTPClient *http.Client
}

func NewAgentImageTools(hubURL string) *AgentImageTools {
	return &AgentImageTools{
		HubURL:     hubURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *AgentImageTools) ListAgentImagesTool() mcp.Tool {
	return mcp.NewTool("list_agent_images",
		mcp.WithDescription("List the agent runtime images registered in the platform, including the tools and LLM models each one declares. Default page size: 50."),
		mcp.WithNumber("page", mcp.Description("Page number (1-based, default: 1)")),
		mcp.WithNumber("pageSize", mcp.Description("Page size (default: 50)")),
	)
}

func (t *AgentImageTools) GetAgentImageTool() mcp.Tool {
	return mcp.NewTool("get_agent_image",
		mcp.WithDescription("Get detail about one agent runtime image: the tools it ships with, the models it supports, and how to reference it from an Agent."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent image name")),
	)
}

func (t *AgentImageTools) ListAgentImages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	q := url.Values{}
	q.Set("page", strconv.Itoa(defaultArgInt(args, "page", 1)))
	q.Set("pageSize", strconv.Itoa(defaultArgInt(args, "pageSize", 50)))
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images?"+q.Encode())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list agent images: %v", err)), nil
	}
	body = annotatePageMeta(body, "more agent images available — pass page=N to fetch the next page")
	return mcp.NewToolResultText(string(body)), nil
}

func (t *AgentImageTools) GetAgentImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := req.Params.Arguments.(map[string]any)["name"].(string)
	if !ok || name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images/"+name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get agent image %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// envEntry represents a single environment variable for an agent image.
type envEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Secret marks the value as sensitive. The hub never returns the value of
	// a secret entry (it reads back as ""), so a client that resends a secret
	// must set this flag and send an empty value to keep the stored one.
	Secret bool `json:"secret,omitempty"`
}

// toolExample represents an example for a tool declaration.
type toolExample struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// toolEntry represents a single tool declaration for an agent image.
type toolEntry struct {
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Description string        `json:"description"`
	Examples    []toolExample `json:"examples,omitempty"`
}

func (t *AgentImageTools) CreateAgentImageTool() mcp.Tool {
	return mcp.NewTool("create_agent_image",
		mcp.WithDescription("Create a new agent runtime image in the platform. Provide a display name, image URL, optional description, and optional environment variables."),
		mcp.WithString("display_name", mcp.Required(), mcp.Description("Display name for the agent image")),
		mcp.WithString("image_url", mcp.Required(), mcp.Description("Container image URL (e.g. registry.example.com/agent:latest)")),
		mcp.WithString("description", mcp.Description("Optional description of the agent image")),
		mcp.WithString("env", mcp.Description("Optional JSON array of {name, value, secret?} environment variable objects. Set secret to true to store the value as sensitive; a secret entry requires a non-empty value on create.")),
	)
}

func (t *AgentImageTools) CreateAgentImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	displayName, _ := args["display_name"].(string)
	if displayName == "" {
		return mcp.NewToolResultError("display_name is required"), nil
	}
	imageURL, _ := args["image_url"].(string)
	if imageURL == "" {
		return mcp.NewToolResultError("image_url is required"), nil
	}

	payload := map[string]any{
		"displayName": displayName,
		"imageUrl":    imageURL,
	}
	if desc, _ := args["description"].(string); desc != "" {
		payload["description"] = desc
	}
	if envStr, _ := args["env"].(string); envStr != "" {
		var envVars []envEntry
		if err := json.Unmarshal([]byte(envStr), &envVars); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid env JSON: %v", err)), nil
		}
		for _, e := range envVars {
			if e.Secret && e.Value == "" {
				return mcp.NewToolResultError(fmt.Sprintf("secret env var %s requires a non-empty value on create: there is no stored value to preserve", e.Name)), nil
			}
		}
		payload["env"] = envVars
	}

	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPost(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images", bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create agent image: %v", err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

func (t *AgentImageTools) UpdateAgentImageTool() mcp.Tool {
	return mcp.NewTool("update_agent_image",
		mcp.WithDescription("Update an existing agent runtime image. Omitting optional fields preserves their current values. Send an empty env array [] to clear all environment variables, or use env_patch to upsert individual variables by name. Send a tools array to replace the declared tool list; send an empty tools array [] to clear all tools."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent image name (the resource ID)")),
		mcp.WithString("display_name", mcp.Description("New display name for the agent image")),
		mcp.WithString("image_url", mcp.Description("New container image URL")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("env", mcp.Description("JSON array of {name, value, secret?} environment variable objects. Replaces the full list: omit to preserve existing env vars, send [] to clear them. The API never returns secret values, so an entry for a currently-secret name must be sent as {name, value: \"\", secret: true} to keep the stored value; the tool applies this automatically. Mutually exclusive with env_patch.")),
		mcp.WithString("env_patch", mcp.Description("JSON array of {name, value, secret?} objects upserted onto the current env list by name: matching entries are updated in place, new names are appended, nothing is removed (use env to remove or clear). An entry with value \"\" on a currently-secret name keeps the stored value. Mutually exclusive with env.")),
		mcp.WithString("tools", mcp.Description("JSON array of tool declarations {name, kind, description, examples[]}. Each example is {title, snippet}. Omit to preserve existing tools; send [] to clear them.")),
	)
}

func (t *AgentImageTools) UpdateAgentImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	_, hasEnv := args["env"]
	_, hasEnvPatch := args["env_patch"]
	if hasEnv && hasEnvPatch {
		return mcp.NewToolResultError("env and env_patch are mutually exclusive — use env to replace the full list, env_patch to upsert by name"), nil
	}

	payload := map[string]any{}
	if v, _ := args["display_name"].(string); v != "" {
		payload["displayName"] = v
	}
	if v, _ := args["image_url"].(string); v != "" {
		payload["imageUrl"] = v
	}
	if v, _ := args["description"].(string); v != "" {
		payload["description"] = v
	}
	// env uses nil-means-preserve semantics: omitting the key preserves existing
	// env vars, sending an explicit [] clears them.
	if envStr, ok := args["env"].(string); ok {
		if envStr == "[]" {
			payload["env"] = []envEntry{}
		} else if envStr != "" {
			var envVars []envEntry
			if err := json.Unmarshal([]byte(envStr), &envVars); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid env JSON: %v", err)), nil
			}
			// The GET view redacts secret values, so a full-list resend would
			// drop the secret flag and let the hub blank every secret entry.
			// Stamp the flag back onto entries whose name is currently stored
			// as a secret and whose value is empty.
			secretNames, err := t.currentSecretEnvNames(ctx, name)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to read current env of agent image %s: %v", name, err)), nil
			}
			for i := range envVars {
				if envVars[i].Value == "" && secretNames[envVars[i].Name] {
					envVars[i].Secret = true
				}
			}
			payload["env"] = envVars
		}
		// If envStr is empty string, we don't set it to preserve existing vars.
	}

	// env_patch upserts by name against the current list, so callers never have
	// to resend the full list (the workflow that used to wipe secrets). An empty
	// or [] patch is a no-op.
	if envPatchStr, ok := args["env_patch"].(string); ok && envPatchStr != "" && envPatchStr != "[]" {
		var patch []envEntry
		if err := json.Unmarshal([]byte(envPatchStr), &patch); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid env_patch JSON: %v", err)), nil
		}
		for i, e := range patch {
			if e.Name == "" {
				return mcp.NewToolResultError(fmt.Sprintf("env_patch entry %d is missing name", i)), nil
			}
		}
		currentEnv, err := t.currentEnv(ctx, name)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read current env of agent image %s: %v", name, err)), nil
		}
		payload["env"] = upsertEnv(currentEnv, patch)
	}

	// tools uses the same nil-means-preserve semantics as env.
	if toolsStr, ok := args["tools"].(string); ok {
		if toolsStr == "[]" {
			payload["tools"] = []toolEntry{}
		} else if toolsStr != "" {
			var toolDefs []toolEntry
			if err := json.Unmarshal([]byte(toolsStr), &toolDefs); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid tools JSON: %v", err)), nil
			}
			payload["tools"] = toolDefs
		}
	}

	if len(payload) == 0 {
		return mcp.NewToolResultError("at least one of display_name, image_url, description, env, env_patch, or tools must be provided"), nil
	}

	bodyData, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode request: %v", err)), nil
	}
	respBody, err := hubPut(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images/"+name, bytes.NewReader(bodyData))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update agent image %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(string(respBody)), nil
}

// currentEnv reads the image's current env list. Secret values come back
// redacted as "", but the secret flags are intact.
func (t *AgentImageTools) currentEnv(ctx context.Context, name string) ([]envEntry, error) {
	body, err := hubGet(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images/"+name)
	if err != nil {
		return nil, err
	}
	var current struct {
		Env []envEntry `json:"env"`
	}
	if err := json.Unmarshal(body, &current); err != nil {
		return nil, err
	}
	return current.Env, nil
}

// currentSecretEnvNames returns the names of env vars currently stored as
// secrets on the image.
func (t *AgentImageTools) currentSecretEnvNames(ctx context.Context, name string) (map[string]bool, error) {
	currentEnv, err := t.currentEnv(ctx, name)
	if err != nil {
		return nil, err
	}
	secrets := make(map[string]bool, len(currentEnv))
	for _, e := range currentEnv {
		if e.Secret {
			secrets[e.Name] = true
		}
	}
	return secrets, nil
}

// upsertEnv merges patch entries into the current list by name: existing
// entries are updated in place, new names appended. A patch entry with an
// empty value over a currently-secret name keeps the stored value — it is
// resent as {name, value: "", secret: true} so the hub's preserve path fires.
// A non-empty value replaces the entry wholesale, which is also how a caller
// rotates (secret: true) or declassifies (secret: false) a variable.
func upsertEnv(current, patch []envEntry) []envEntry {
	merged := make([]envEntry, 0, len(current)+len(patch))
	index := make(map[string]int, len(current))
	for _, e := range current {
		index[e.Name] = len(merged)
		merged = append(merged, e)
	}
	for _, p := range patch {
		if i, ok := index[p.Name]; ok {
			if p.Value == "" && merged[i].Secret {
				merged[i] = envEntry{Name: p.Name, Value: "", Secret: true}
			} else {
				merged[i] = p
			}
			continue
		}
		index[p.Name] = len(merged)
		merged = append(merged, p)
	}
	return merged
}

func (t *AgentImageTools) DeleteAgentImageTool() mcp.Tool {
	return mcp.NewTool("delete_agent_image",
		mcp.WithDescription("Delete an agent runtime image from the platform. Returns an error if any Agent still references the image via imageRef."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Agent image name to delete")),
	)
}

func (t *AgentImageTools) DeleteAgentImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := req.Params.Arguments.(map[string]any)["name"].(string)
	if !ok || name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	_, err := hubDelete(ctx, t.HTTPClient, t.HubURL, "/api/v1/agent-images/"+name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete agent image %s: %v", name, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("agent image %s deleted", name)), nil
}
