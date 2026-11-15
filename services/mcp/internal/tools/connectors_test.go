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

func TestListConnectors(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/connectors" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"metadata": map[string]any{"name": "forgejo-connector"}, "spec": map[string]any{"type": "forgejo"}},
			{"metadata": map[string]any{"name": "slack-connector"}, "spec": map[string]any{"type": "slack"}},
		})
	}))
	defer hub.Close()

	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := ct.ListConnectors(context.Background(), mcp.CallToolRequest{})
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

func TestGetConnector(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/connectors/forgejo-connector" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "forgejo-connector"},
			"spec":     map[string]any{"type": "forgejo"},
		})
	}))
	defer hub.Close()

	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "forgejo-connector"}
	result, err := ct.GetConnector(context.Background(), req)
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

func TestUpdateConnector_DisplayName(t *testing.T) {
	var capturedMethod, capturedPath, capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "c-0c4b01e3",
			"name": "connector-forgejo-ainsel",
		})
	}))
	defer hub.Close()

	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name":         "c-0c4b01e3",
		"display_name": "connector-forgejo-ainsel",
	}
	result, err := ct.UpdateConnector(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if capturedMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", capturedMethod)
	}
	if capturedPath != "/api/v1/connectors/c-0c4b01e3" {
		t.Errorf("unexpected path: %s", capturedPath)
	}
	if !strings.Contains(capturedBody, `"name":"connector-forgejo-ainsel"`) {
		t.Errorf("expected display name in request body; got: %s", capturedBody)
	}
	// disabled was not provided — it must not be sent.
	if strings.Contains(capturedBody, "disabled") {
		t.Errorf("expected disabled to be omitted; got: %s", capturedBody)
	}
}

func TestUpdateConnector_Disabled(t *testing.T) {
	var capturedBody string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c-x", "name": "c", "disabled": true})
	}))
	defer hub.Close()

	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "c-x", "disabled": true}
	result, err := ct.UpdateConnector(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	if !strings.Contains(capturedBody, `"disabled":true`) {
		t.Errorf("expected disabled in body; got: %s", capturedBody)
	}
}

func TestUpdateConnector_NoFields(t *testing.T) {
	ct := &ConnectorTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "c-x"}
	result, _ := ct.UpdateConnector(context.Background(), req)
	if !result.IsError {
		t.Error("expected error when no optional fields provided")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "at least one") {
		t.Errorf("expected descriptive error; got: %s", result.Content[0].(mcp.TextContent).Text)
	}
}

func TestUpdateConnector_MissingName(t *testing.T) {
	ct := &ConnectorTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"display_name": "connector-new"}
	result, _ := ct.UpdateConnector(context.Background(), req)
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

// callConnector invokes a connector handler with the given tool arguments.
func callConnector(t *testing.T, ct *ConnectorTools, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

func connectorText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].(mcp.TextContent).Text
}

func TestCreateConnector_FullPayload(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"c-1a2b3c","name":"connector-sentry","signatureHeader":"X-Sentry-Auth-Signature","webhookEndpoint":"https://ainsel.example.com/webhooks/c-1a2b3c","disabled":false}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.CreateConnector, map[string]any{
		"name":             "connector-sentry",
		"groupId":          "my-group",
		"signature_header": "X-Sentry-Auth-Signature",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/connectors" {
		t.Errorf("expected POST /api/v1/connectors, got %s %s", rec.method, rec.path)
	}
	for _, want := range []string{
		`"name":"connector-sentry"`,
		`"groupId":"my-group"`,
		`"signatureHeader":"X-Sentry-Auth-Signature"`,
	} {
		if !strings.Contains(rec.body, want) {
			t.Errorf("expected %s in request body; got: %s", want, rec.body)
		}
	}
}

func TestCreateConnector_OmitsUnsetOptionals(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"c-1","name":"c"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	// Absent arguments are absent keys: the hub applies its own default for the
	// signature header and decides itself whether groupId is required.
	if strings.Contains(rec.body, "groupId") || strings.Contains(rec.body, "signatureHeader") {
		t.Errorf("expected optional keys omitted; got: %s", rec.body)
	}
}

func TestCreateConnector_ExplicitEmptySignatureHeaderIsSent(t *testing.T) {
	// Presence-based emission: an explicit "" reaches the hub, which reads it as
	// "not set" and applies X-Hub-Signature-256. It is not a way to store an
	// empty header, and it is not swallowed into an omitted key either.
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"c-1","name":"c"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": "c", "signature_header": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	if !strings.Contains(rec.body, `"signatureHeader":""`) {
		t.Errorf("expected the explicit empty signature header to be sent; got: %s", rec.body)
	}
}

func TestCreateConnector_MissingName(t *testing.T) {
	ct := &ConnectorTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, _ := callConnector(t, ct, ct.CreateConnector, map[string]any{"groupId": "g"})
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

func TestCreateConnector_GroupIDHint(t *testing.T) {
	hub, _ := stubHub(t, http.StatusBadRequest, `{"error":"groupId is required"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": "c"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := connectorText(result)
	if !strings.Contains(text, "groupId is required") || !strings.Contains(text, "access control enabled") {
		t.Errorf("expected the hub error plus the group hint; got: %s", text)
	}
}

func TestCreateConnector_ForbiddenPassthrough(t *testing.T) {
	// Criterion 4: the MCP adds no permission check of its own, so a non-admin
	// sees the hub's 403 and message verbatim.
	hub, _ := stubHub(t, http.StatusForbidden, `{"error":"admin access required"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": "c"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := connectorText(result)
	if !strings.Contains(text, "403") || !strings.Contains(text, "admin access required") {
		t.Errorf("expected the hub's 403 to pass through; got: %s", text)
	}
}

// TestCreateConnector_OneTimeSecretIsHubBodyOnly pins the documented contract:
// the hub generates the webhook HMAC and returns it exactly once, in the create
// response (docs/api-reference.md, openapi/hub.yaml), and the tool forwards the
// hub body unchanged. Every read path masks it — toWebhookConnectorResponse
// never fills webhookSecretValue — so this response is the only place the value
// appears. The tool adds nothing of its own and never echoes the request body.
func TestCreateConnector_OneTimeSecretIsHubBodyOnly(t *testing.T) {
	hub, _ := stubHub(t, http.StatusCreated, `{"id":"c-1","name":"c","webhookSecretValue":"hub-generated-once"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	if !strings.Contains(connectorText(result), "hub-generated-once") {
		t.Errorf("expected the hub's one-time create response unchanged; got: %s", connectorText(result))
	}
}

func TestCreateConnector_ErrorDoesNotEchoRequest(t *testing.T) {
	const sentinel = "sentinel-connector-label"
	hub, _ := stubHub(t, http.StatusInternalServerError, `{"error":"failed to create connector"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callConnector(t, ct, ct.CreateConnector, map[string]any{"name": sentinel, "groupId": "g"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if strings.Contains(connectorText(result), sentinel) {
		t.Errorf("error text must not carry the request body; got: %s", connectorText(result))
	}
}

func TestUpdateConnector_EmptyDisplayNameIsSent(t *testing.T) {
	// Presence-based emission: an explicit "" is sent rather than dropped. The
	// hub reads an empty name as "no change", so this cannot blank a label — but
	// it must not be swallowed into a different request either.
	hub, rec := stubHub(t, http.StatusOK, `{"id":"c-x","name":"c"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.UpdateConnector, map[string]any{"name": "c-x", "display_name": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	if !strings.Contains(rec.body, `"name":""`) {
		t.Errorf("expected the empty display name to be sent; got: %s", rec.body)
	}
}

func TestDeleteConnector(t *testing.T) {
	hub, rec := stubHub(t, http.StatusNoContent, ``)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callConnector(t, ct, ct.DeleteConnector, map[string]any{"name": "c-0c4b01e3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", connectorText(result))
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/connectors/c-0c4b01e3" {
		t.Errorf("expected DELETE /api/v1/connectors/c-0c4b01e3, got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(connectorText(result), "deleted") {
		t.Errorf("expected a deletion confirmation; got: %s", connectorText(result))
	}
}

func TestDeleteConnector_MissingName(t *testing.T) {
	ct := &ConnectorTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, _ := callConnector(t, ct, ct.DeleteConnector, map[string]any{})
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

func TestDeleteConnector_NotFoundPassthrough(t *testing.T) {
	hub, _ := stubHub(t, http.StatusNotFound, `{"error":"connector not found"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callConnector(t, ct, ct.DeleteConnector, map[string]any{"name": "c-nope"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := connectorText(result)
	if !strings.Contains(text, "404") || !strings.Contains(text, "connector not found") {
		t.Errorf("expected the hub's 404 to pass through; got: %s", text)
	}
}

func TestDeleteConnector_ManagedConflictPassthrough(t *testing.T) {
	hub, _ := stubHub(t, http.StatusConflict, `{"error":"connector is managed by plugin-forgejo"}`)
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callConnector(t, ct, ct.DeleteConnector, map[string]any{"name": "c-1"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := connectorText(result)
	if !strings.Contains(text, "409") || !strings.Contains(text, "managed by plugin-forgejo") {
		t.Errorf("expected the hub's 409 to pass through; got: %s", text)
	}
}
