package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestListAgents(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"metadata": map[string]any{"name": "olli"}, "spec": map[string]any{"displayName": "Olli"}},
			{"metadata": map[string]any{"name": "difi"}, "spec": map[string]any{"displayName": "Difi"}},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := at.ListAgents(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if len(text) == 0 {
		t.Error("expected non-empty response")
	}
}

func TestGetAgent(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/olli" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "olli"},
			"spec":     map[string]any{"displayName": "Olli"},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "olli"}
	result, err := at.GetAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if len(text) == 0 {
		t.Error("expected non-empty response")
	}
}

func TestUpdateAgent_ModelOnly(t *testing.T) {
	var capturedMethod, capturedPath, capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a-director",
			"name": "Director",
			"llm": map[string]any{
				"model":    "glm-5.1",
				"maxTurns": 10,
			},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":  "a-director",
		"model": "glm-5.1",
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if capturedMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", capturedMethod)
	}
	if capturedPath != "/api/v1/agents/a-director" {
		t.Errorf("unexpected path: %s", capturedPath)
	}
	if !strings.Contains(capturedBody, `"model":"glm-5.1"`) {
		t.Errorf("expected model in request body; got: %s", capturedBody)
	}
	// Should not include maxTurns since it was not provided
	if strings.Contains(capturedBody, "maxTurns") {
		t.Errorf("expected maxTurns to be omitted; got: %s", capturedBody)
	}
}

func TestUpdateAgent_TemperatureOnly(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a-director",
			"name": "Director",
			"llm": map[string]any{
				"model":       "glm-5.1",
				"temperature": 0.7,
			},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":        "a-director",
		"temperature": 0.7,
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(capturedBody, `"temperature":0.7`) {
		t.Errorf("expected temperature in request body; got: %s", capturedBody)
	}
}

func TestUpdateAgent_MultipleFields(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a-director",
			"name": "Director",
			"llm": map[string]any{
				"model":       "qwen3.5:cloud",
				"maxTurns":    5,
				"temperature": 0.3,
			},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":        "a-director",
		"model":       "qwen3.5:cloud",
		"max_turns":   5.0,
		"temperature": 0.3,
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(capturedBody, `"model":"qwen3.5:cloud"`) {
		t.Errorf("expected model in body; got: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, `"maxTurns":5`) {
		t.Errorf("expected maxTurns in body; got: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, `"temperature":0.3`) {
		t.Errorf("expected temperature in body; got: %s", capturedBody)
	}
}

func TestUpdateAgent_ContextWindowOnly(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a-director",
			"name": "Director",
			"llm": map[string]any{
				"model":         "glm-5.1",
				"contextWindow": 131072,
			},
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":           "a-director",
		"context_window": float64(131072),
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(capturedBody, `"contextWindow":131072`) {
		t.Errorf("expected contextWindow in request body; got: %s", capturedBody)
	}
	// context_window alone must not disturb the model or the other LLM fields.
	for _, unwanted := range []string{`"model"`, `"maxTurns"`, `"temperature"`} {
		if strings.Contains(capturedBody, unwanted) {
			t.Errorf("expected %s to be omitted; got: %s", unwanted, capturedBody)
		}
	}
}

func TestUpdateAgent_ContextWindowBelowMinimum(t *testing.T) {
	// The hub forwards the value to the Agent CR, whose schema requires
	// >= 1024; the tool must reject it without issuing the request.
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"below minimum", 512},
		{"zero", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hubCalled := false
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hubCalled = true
			}))
			defer hub.Close()

			at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"name":           "a-director",
				"context_window": tc.value,
			}
			result, err := at.UpdateAgent(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected error for context_window %v", tc.value)
			}
			if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "1024") {
				t.Errorf("expected the minimum in the error; got: %s", result.Content[0].(mcp.TextContent).Text)
			}
			if hubCalled {
				t.Error("expected no hub request for a rejected context_window")
			}
		})
	}
}

func TestUpdateAgent_ContextWindowMinimumAccepted(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "a-director"})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":           "a-director",
		"context_window": float64(minContextWindow),
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(capturedBody, `"contextWindow":1024`) {
		t.Errorf("expected the boundary value 1024 in body; got: %s", capturedBody)
	}
}

func TestUpdateAgent_Vision(t *testing.T) {
	// vision is tri-state at the tool boundary: present means write it. The
	// false case is the one that matters — the hub reads llm.vision as a
	// pointer, so an omitted flag means "leave unchanged" and a dropped false
	// would leave image input stuck on.
	for _, tc := range []struct {
		name  string
		value bool
		want  string
	}{
		{"enabled", true, `"vision":true`},
		{"disabled", false, `"vision":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedBody string
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				capturedBody = string(b)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "a-director"})
			}))
			defer hub.Close()

			at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"name":   "a-director",
				"vision": tc.value,
			}
			result, err := at.UpdateAgent(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
			}
			if !strings.Contains(capturedBody, tc.want) {
				t.Errorf("expected %s in request body; got: %s", tc.want, capturedBody)
			}
			// vision alone must not disturb the other LLM fields.
			for _, unwanted := range []string{`"model"`, `"maxTurns"`, `"temperature"`, `"contextWindow"`} {
				if strings.Contains(capturedBody, unwanted) {
					t.Errorf("expected %s to be omitted; got: %s", unwanted, capturedBody)
				}
			}
		})
	}
}

func TestUpdateAgent_VisionOmitted(t *testing.T) {
	// A model-only update must leave the flag alone rather than write it off.
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "a-director"})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":  "a-director",
		"model": "glm-5.1",
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if strings.Contains(capturedBody, "vision") {
		t.Errorf("expected vision to be omitted; got: %s", capturedBody)
	}
}

func TestUpdateAgent_NoOptionalFields(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "a-director"}
	result, _ := at.UpdateAgent(context.Background(), req)
	if !result.IsError {
		t.Error("expected error when no optional fields provided")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "at least one") {
		t.Errorf("expected descriptive error; got: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

func TestUpdateAgent_MissingName(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"model": "glm-5.1"}
	result, _ := at.UpdateAgent(context.Background(), req)
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}
func TestUpdateAgent_DisplayNameOnly(t *testing.T) {
	var capturedPath, capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "a-director",
			"name": "agent-director",
		})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":         "a-director",
		"display_name": "agent-director",
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if capturedPath != "/api/v1/agents/a-director" {
		t.Errorf("unexpected path: %s", capturedPath)
	}
	if !strings.Contains(capturedBody, `"name":"agent-director"`) {
		t.Errorf("expected display name in request body; got: %s", capturedBody)
	}
	// A pure rename must not touch the LLM config.
	if strings.Contains(capturedBody, `"llm"`) {
		t.Errorf("expected llm to be omitted; got: %s", capturedBody)
	}
}

func TestUpdateAgent_DisplayNameWithLLM(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "a-director", "name": "agent-director"})
	}))
	defer hub.Close()

	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":         "a-director",
		"display_name": "agent-director",
		"description":  "Plans and coordinates work",
		"model":        "glm-5.1",
	}
	result, err := at.UpdateAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	for _, want := range []string{`"name":"agent-director"`, `"description":"Plans and coordinates work"`, `"model":"glm-5.1"`} {
		if !strings.Contains(capturedBody, want) {
			t.Errorf("expected %s in body; got: %s", want, capturedBody)
		}
	}
}

// hubRecorder captures the last request seen by the stub hub.
type hubRecorder struct {
	method, path, body string
}

// stubHub replies with a fixed status/body and records the request.
func stubHub(t *testing.T, status int, respBody string) (*httptest.Server, *hubRecorder) {
	t.Helper()
	rec := &hubRecorder{}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.method, rec.path, rec.body = r.Method, r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(hub.Close)
	return hub, rec
}

func callAgent(t *testing.T, at *AgentTools, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

func TestCreateAgent_FullPayload(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"a-abc123","name":"reviewer"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name":         "reviewer",
		"groupId":      "platform",
		"image":        "pi",
		"model":        "glm-5.1",
		"provider":     "openai",
		"persona_text": "Review code carefully.",
		"skills":       `["code-review"]`,
		"mcp_servers":  `["forgejo"]`,
		"env":          `[{"name":"FORGEJO_TOKEN","value":"s3cret","secret":true}]`,
		"replicas":     2.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/agents" {
		t.Errorf("expected POST /api/v1/agents, got %s %s", rec.method, rec.path)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(rec.body), &got); err != nil {
		t.Fatalf("bad body captured: %v (%s)", err, rec.body)
	}
	if got["name"] != "reviewer" || got["groupId"] != "platform" {
		t.Errorf("expected name/groupId in body; got: %s", rec.body)
	}
	if ref, _ := got["imageRef"].(map[string]any); ref["name"] != "pi" {
		t.Errorf("expected imageRef.name=pi; got: %s", rec.body)
	}
	llm, _ := got["llm"].(map[string]any)
	if llm["model"] != "glm-5.1" || llm["provider"] != "openai" {
		t.Errorf("expected llm model/provider; got: %s", rec.body)
	}
	if p, _ := got["persona"].(map[string]any); p["text"] != "Review code carefully." {
		t.Errorf("expected inline persona text; got: %s", rec.body)
	}
	if sk, _ := got["skills"].([]any); len(sk) != 1 || sk[0] != "code-review" {
		t.Errorf("expected skills; got: %s", rec.body)
	}
	if sel, _ := got["mcp"].(map[string]any); sel == nil || len(sel["servers"].([]any)) != 1 {
		t.Errorf("expected mcp.servers; got: %s", rec.body)
	}
	env, _ := got["env"].([]any)
	if len(env) != 1 {
		t.Fatalf("expected one env entry; got: %s", rec.body)
	}
	if e, _ := env[0].(map[string]any); e["name"] != "FORGEJO_TOKEN" || e["secret"] != true {
		t.Errorf("expected secret env entry; got: %s", rec.body)
	}
	if got["replicas"].(float64) != 2 {
		t.Errorf("expected replicas=2; got: %s", rec.body)
	}
}

func TestCreateAgent_MissingRequired(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing required args")
	}
}

func TestCreateAgent_SecretEnvRequiresValue(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name": "x", "image": "pi", "model": "m", "provider": "openai",
		"env": `[{"name":"TOKEN","value":"","secret":true}]`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "requires a non-empty value") {
		t.Fatalf("expected secret-env rejection; got: %+v", result)
	}
}

func TestCreateAgent_PersonaMutuallyExclusive(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name": "x", "image": "pi", "model": "m", "provider": "openai",
		"persona_id": "p-1", "persona_text": "hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "not both") {
		t.Fatalf("expected persona exclusivity error; got: %+v", result)
	}
}

func TestCreateAgent_ContextWindowGuard(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name": "x", "image": "pi", "model": "m", "provider": "openai",
		"context_window": 10.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "at least 1024") {
		t.Fatalf("expected context_window guard; got: %+v", result)
	}
}

func TestCreateAgent_ForbiddenPassthrough(t *testing.T) {
	hub, rec := stubHub(t, http.StatusForbidden, `{"error":"admin access required"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name": "x", "image": "pi", "model": "m", "provider": "openai",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error on 403")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "403") || !strings.Contains(text, "admin access required") {
		t.Fatalf("expected hub 403 passthrough; got: %s", text)
	}
	if rec.method != http.MethodPost {
		t.Errorf("expected POST; got %s", rec.method)
	}
}

func TestCreateAgent_SecretNotEchoed(t *testing.T) {
	// The hub response never carries the submitted key back; the tool must
	// not add it either (no request-body echo in results or errors).
	const sentinel = "sentinel-secret-value"
	hub, _ := stubHub(t, http.StatusCreated, `{"id":"a-abc","name":"x","openAI":{"keyStored":true}}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.CreateAgent, map[string]any{
		"name": "x", "image": "pi", "model": "m", "provider": "openai",
		"openai_api_key": sentinel,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if strings.Contains(result.Content[0].(mcp.TextContent).Text, sentinel) {
		t.Fatalf("tool result leaked the API key: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

// --- delete_agent -----------------------------------------------------------

func TestDeleteAgent(t *testing.T) {
	hub, rec := stubHub(t, http.StatusNoContent, "")
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.DeleteAgent, map[string]any{"name": "a-director"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/agents/a-director" {
		t.Errorf("expected DELETE /api/v1/agents/a-director, got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "a-director") {
		t.Errorf("expected confirmation to name the agent; got: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

func TestDeleteAgent_NotFoundPassthrough(t *testing.T) {
	hub, _ := stubHub(t, http.StatusNotFound, `{"error":"agent not found"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.DeleteAgent, map[string]any{"name": "gone"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error on 404")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "404") || !strings.Contains(text, "agent not found") {
		t.Fatalf("expected hub 404 passthrough; got: %s", text)
	}
}

// --- pause_agent / resume_agent ---------------------------------------------

func TestPauseAgent_Duration(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director","paused":true}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.PauseAgent, map[string]any{"name": "a-director", "duration": "30m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/agents/a-director/pause" {
		t.Errorf("expected POST pause subresource, got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(rec.body, `"duration":"30m"`) {
		t.Errorf("expected duration in body; got: %s", rec.body)
	}
}

func TestPauseAgent_IndefiniteSendsEmptyObject(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director","paused":true}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.PauseAgent, map[string]any{"name": "a-director"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/agents/a-director/pause" {
		t.Errorf("expected POST pause subresource, got %s %s", rec.method, rec.path)
	}
	if rec.body != "{}" {
		t.Errorf("expected empty JSON object body; got: %q", rec.body)
	}
}

func TestPauseAgent_DurationAndUntilRejected(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.PauseAgent, map[string]any{
		"name": "a-director", "duration": "30m", "until": "2026-12-31T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "not both") {
		t.Fatalf("expected exclusivity error; got: %+v", result)
	}
}

func TestResumeAgent(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director","paused":false}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.ResumeAgent, map[string]any{"name": "a-director"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/agents/a-director/pause" {
		t.Errorf("expected DELETE pause subresource, got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, `"paused":false`) {
		t.Errorf("expected hub response body returned; got: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

// --- update_agent: presence-based field matrix ------------------------------

func TestUpdateAgent_SkillsEmptyClears(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "skills": "[]",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"skills":[]`) {
		t.Errorf("expected skills:[] (clear) in body; got: %s", rec.body)
	}
	if strings.Contains(rec.body, `"env"`) || strings.Contains(rec.body, `"llm"`) {
		t.Errorf("absent fields must be omitted; got: %s", rec.body)
	}
}

func TestUpdateAgent_SecretEnvEmptyValueKept(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director",
		"env":  `[{"name":"FORGEJO_TOKEN","value":"","secret":true}]`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	// The empty value must reach the hub with secret: true — the hub keeps
	// the stored value only when it sees the entry as secret.
	if !strings.Contains(rec.body, `"value":""`) || !strings.Contains(rec.body, `"secret":true`) {
		t.Errorf("expected keep-existing secret entry sent as-is; got: %s", rec.body)
	}
}

func TestUpdateAgent_WorkingHoursEmptyClears(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "working_hours": "{}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"workingHours":{}`) {
		t.Errorf("expected workingHours:{} (clear) in body; got: %s", rec.body)
	}
}

func TestUpdateAgent_ReplicasZeroSent(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "replicas": 0.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"replicas":0`) {
		t.Errorf("expected replicas:0 (present zero is a real change); got: %s", rec.body)
	}
}

func TestUpdateAgent_ImageAndPersona(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "image": "pi", "persona_id": "p-42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"imageRef":{"name":"pi"}`) {
		t.Errorf("expected imageRef; got: %s", rec.body)
	}
	if !strings.Contains(rec.body, `"persona":{"id":"p-42"}`) {
		t.Errorf("expected persona ref; got: %s", rec.body)
	}
}

func TestUpdateAgent_ProviderKeyRotation(t *testing.T) {
	const sentinel = "sentinel-rotate-key"
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director","openAI":{"keyStored":true}}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "openai_api_key": sentinel,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"openAI"`) {
		t.Errorf("expected openAI block in request; got: %s", rec.body)
	}
	if strings.Contains(result.Content[0].(mcp.TextContent).Text, sentinel) {
		t.Fatalf("tool result leaked the API key: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

func TestUpdateAgent_ChatGPTSignInConflict(t *testing.T) {
	at := &AgentTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "openai_api_key": "k", "openai_auth_method": "ChatGPTSignIn",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "not both") {
		t.Fatalf("expected sign-in/key conflict; got: %+v", result)
	}
}

func TestUpdateAgent_McpServersEmptyConnectsNone(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"id":"a-director"}`)
	at := &AgentTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callAgent(t, at, at.UpdateAgent, map[string]any{
		"name": "a-director", "mcp_servers": "[]",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(rec.body, `"mcp":{"servers":[]}`) {
		t.Errorf("expected explicit empty MCP selection; got: %s", rec.body)
	}
}
