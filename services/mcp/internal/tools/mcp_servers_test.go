package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestListMCPServers(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mcp-servers" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "ainsel-mcp", "url": "http://ainsel-mcp.ainsel.svc:8080/mcp"},
			},
		})
	}))
	defer hub.Close()

	mt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := mt.ListMCPServers(context.Background(), mcp.CallToolRequest{})
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

func TestGetMCPServer(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mcp-servers/ainsel-mcp" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":  "ainsel-mcp",
			"url": "http://ainsel-mcp.ainsel.svc:8080/mcp",
		})
	}))
	defer hub.Close()

	mt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "ainsel-mcp"}
	result, err := mt.GetMCPServer(context.Background(), req)
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

// stubReply is one canned hub response.
type stubReply struct {
	status int
	body   string
}

// stubHubMethods replies per HTTP method and records the last request seen for
// each. A read-then-write tool (update_mcp_server) issues two requests, and a
// test has to see the body of the PUT without losing the GET that produced it.
func stubHubMethods(t *testing.T, replies map[string]stubReply) (*httptest.Server, map[string]*hubRecorder) {
	t.Helper()
	seen := map[string]*hubRecorder{}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := seen[r.Method]
		if rec == nil {
			rec = &hubRecorder{}
			seen[r.Method] = rec
		}
		b, _ := io.ReadAll(r.Body)
		rec.method, rec.path, rec.body = r.Method, r.URL.Path, string(b)
		reply, ok := replies[r.Method]
		if !ok {
			reply = stubReply{status: http.StatusOK, body: `{}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		if reply.body != "" {
			_, _ = io.WriteString(w, reply.body)
		}
	}))
	t.Cleanup(hub.Close)
	return hub, seen
}

// callMCPServer invokes an MCP-server registry handler with tool arguments.
func callMCPServer(t *testing.T, tt *MCPServerTools, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

func mcpServerText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].(mcp.TextContent).Text
}

// storedRow is what the registry holds for the row these tests exercise: every
// writable field populated, so a PUT that drops one is visible.
const storedRow = `{"name":"forgejo","displayName":"Forgejo","description":"Forgejo MCP endpoint","url":"http://forgejo-mcp.platform.svc.cluster.local:8080/mcp","tokenFromEnv":"FORGEJO_TOKEN","managedBy":"user"}`

// TestMCPServerNameIsPathEscaped pins the convention channels.go and plugins.go
// already follow: a caller-supplied name is escaped into exactly one path
// segment, so an argument like "../connectors" addresses one registry row
// instead of reshaping the hub request path (or misrouting silently).
func TestMCPServerNameIsPathEscaped(t *testing.T) {
	paths := make(chan string, 2)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer hub.Close()

	name := "../connectors"
	want := "/api/v1/mcp-servers/" + url.PathEscape(name)

	mt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	if _, err := callMCPServer(t, mt, mt.DeleteMCPServer, map[string]any{"name": name}); err != nil {
		t.Fatalf("delete_mcp_server: unexpected error: %v", err)
	}
	ct := &ConnectorTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	if _, err := callConnector(t, ct, ct.DeleteConnector, map[string]any{"name": name}); err != nil {
		t.Fatalf("delete_connector: unexpected error: %v", err)
	}

	var got []string
	for range 2 {
		got = append(got, <-paths)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 hub requests, got %d: %v", len(got), got)
	}
	if got[0] != want {
		t.Errorf("mcp-server delete: expected escaped path %q, got %q", want, got[0])
	}
	if want := "/api/v1/connectors/" + url.PathEscape(name); got[1] != want {
		t.Errorf("connector delete: expected escaped path %q, got %q", want, got[1])
	}
}

func TestCreateMCPServer_FullPayload(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"name":"forgejo","displayName":"Forgejo","url":"http://forgejo-mcp.platform.svc.cluster.local:8080/mcp","tokenFromEnv":"FORGEJO_TOKEN","managedBy":"user"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{
		"name":           "forgejo",
		"url":            "http://forgejo-mcp.platform.svc.cluster.local:8080/mcp",
		"display_name":   "Forgejo",
		"description":    "Forgejo MCP endpoint",
		"token_from_env": "FORGEJO_TOKEN",
		"groupId":        "my-group",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", mcpServerText(result))
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/mcp-servers" {
		t.Errorf("expected POST /api/v1/mcp-servers, got %s %s", rec.method, rec.path)
	}
	for _, want := range []string{
		`"name":"forgejo"`,
		`"url":"http://forgejo-mcp.platform.svc.cluster.local:8080/mcp"`,
		`"displayName":"Forgejo"`,
		`"description":"Forgejo MCP endpoint"`,
		`"tokenFromEnv":"FORGEJO_TOKEN"`,
		`"groupId":"my-group"`,
	} {
		if !strings.Contains(rec.body, want) {
			t.Errorf("expected %s in request body; got: %s", want, rec.body)
		}
	}
}

func TestCreateMCPServer_OmitsUnsetOptionals(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"name":"anon","url":"http://anon.svc/mcp"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{
		"name": "anon",
		"url":  "http://anon.svc/mcp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", mcpServerText(result))
	}
	// An anonymous endpoint sends no tokenFromEnv at all, and no groupId on a
	// hub without access control.
	for _, unwanted := range []string{"tokenFromEnv", "groupId", "displayName", "description"} {
		if strings.Contains(rec.body, unwanted) {
			t.Errorf("expected %s omitted; got: %s", unwanted, rec.body)
		}
	}
}

func TestCreateMCPServer_RequiredArgs(t *testing.T) {
	tt := &MCPServerTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	for _, args := range []map[string]any{
		{"url": "http://x.svc/mcp"},
		{"name": "  ", "url": "http://x.svc/mcp"},
		{"name": "x"},
		{"name": "x", "url": "   "},
	} {
		result, _ := callMCPServer(t, tt, tt.CreateMCPServer, args)
		if !result.IsError {
			t.Errorf("expected error for args %v", args)
		}
	}
}

func TestCreateMCPServer_GroupIDHint(t *testing.T) {
	hub, _ := stubHub(t, http.StatusBadRequest, `{"error":"groupId is required"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{"name": "x", "url": "http://x.svc/mcp"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := mcpServerText(result)
	if !strings.Contains(text, "groupId is required") || !strings.Contains(text, "access control enabled") {
		t.Errorf("expected the hub error plus the group hint; got: %s", text)
	}
}

func TestCreateMCPServer_ForbiddenPassthrough(t *testing.T) {
	hub, _ := stubHub(t, http.StatusForbidden, `{"error":"insufficient permissions for group my-group"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{"name": "x", "url": "http://x.svc/mcp", "groupId": "my-group"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := mcpServerText(result)
	if !strings.Contains(text, "403") || !strings.Contains(text, "insufficient permissions") {
		t.Errorf("expected the hub's 403 to pass through; got: %s", text)
	}
}

func TestCreateMCPServer_TokenFromEnvValidationPassthrough(t *testing.T) {
	// The hub owns the env-var-name rule; the tool forwards its 400 unchanged.
	hub, _ := stubHub(t, http.StatusBadRequest, `{"error":"tokenFromEnv must match ^[A-Z_][A-Z0-9_]*$"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{"name": "x", "url": "http://x.svc/mcp", "token_from_env": "forgejo_pat"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(mcpServerText(result), "tokenFromEnv must match") {
		t.Errorf("expected the hub's validation message; got: %s", mcpServerText(result))
	}
}

func TestCreateMCPServer_ErrorDoesNotEchoRequest(t *testing.T) {
	const sentinel = "sentinel-registry-name"
	hub, _ := stubHub(t, http.StatusInternalServerError, `{"error":"db down"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.CreateMCPServer, map[string]any{"name": sentinel, "url": "http://x.svc/mcp"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if strings.Contains(mcpServerText(result), sentinel) {
		t.Errorf("error text must not carry the request body; got: %s", mcpServerText(result))
	}
}

func TestUpdateMCPServer_KeepsUnsentFields(t *testing.T) {
	// The hub's PUT replaces the whole row, so the tool must resend what the
	// caller did not pass — dropping url or tokenFromEnv here would break every
	// agent that attaches the row.
	hub, seen := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusOK, storedRow},
		http.MethodPut: {http.StatusOK, storedRow},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "forgejo", "display_name": "Forgejo EU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", mcpServerText(result))
	}
	get, put := seen[http.MethodGet], seen[http.MethodPut]
	if get == nil || get.path != "/api/v1/mcp-servers/forgejo" {
		t.Fatalf("expected a GET of the row first; got %+v", get)
	}
	if put == nil || put.path != "/api/v1/mcp-servers/forgejo" {
		t.Fatalf("expected PUT of the row; got %+v", put)
	}
	for _, want := range []string{
		`"displayName":"Forgejo EU"`,
		`"description":"Forgejo MCP endpoint"`,
		`"url":"http://forgejo-mcp.platform.svc.cluster.local:8080/mcp"`,
		`"tokenFromEnv":"FORGEJO_TOKEN"`,
	} {
		if !strings.Contains(put.body, want) {
			t.Errorf("expected %s in the PUT body; got: %s", want, put.body)
		}
	}
	// managedBy is read-only; the tool must not try to write it.
	if strings.Contains(put.body, "managedBy") {
		t.Errorf("expected managedBy to stay out of the request; got: %s", put.body)
	}
}

func TestUpdateMCPServer_ExplicitEmptyClears(t *testing.T) {
	hub, seen := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusOK, storedRow},
		http.MethodPut: {http.StatusOK, storedRow},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "forgejo", "description": "", "token_from_env": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", mcpServerText(result))
	}
	body := seen[http.MethodPut].body
	// Present-but-empty is a real change: description clears, and an empty
	// tokenFromEnv makes the endpoint anonymous again.
	if !strings.Contains(body, `"description":""`) || !strings.Contains(body, `"tokenFromEnv":""`) {
		t.Errorf("expected the empty fields to be sent; got: %s", body)
	}
	if !strings.Contains(body, `"url":"http://forgejo-mcp.platform.svc.cluster.local:8080/mcp"`) {
		t.Errorf("expected the untouched url to be preserved; got: %s", body)
	}
}

func TestUpdateMCPServer_EmptyURLRejected(t *testing.T) {
	// The hub stores url verbatim on PUT, so an empty one would be written and
	// break every agent attaching the row. Reject it and issue no PUT.
	hub, seen := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusOK, storedRow},
		http.MethodPut: {http.StatusOK, storedRow},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "forgejo", "url": ""})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(mcpServerText(result), "url cannot be cleared") {
		t.Errorf("expected the guard message; got: %s", mcpServerText(result))
	}
	if seen[http.MethodPut] != nil {
		t.Errorf("expected no PUT request; got: %s", seen[http.MethodPut].body)
	}
}

func TestUpdateMCPServer_NoFields(t *testing.T) {
	tt := &MCPServerTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, _ := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "forgejo"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(mcpServerText(result), "at least one field") {
		t.Errorf("expected a descriptive error; got: %s", mcpServerText(result))
	}
}

func TestUpdateMCPServer_ForbiddenPassthrough(t *testing.T) {
	// The read succeeds (the caller may see the row); the hub's write check is
	// what refuses, and its 403 reaches the caller unchanged.
	hub, _ := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusOK, storedRow},
		http.MethodPut: {http.StatusForbidden, `{"error":"admin access required"}`},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "forgejo", "url": "http://new.svc/mcp"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := mcpServerText(result)
	if !strings.Contains(text, "403") || !strings.Contains(text, "admin access required") {
		t.Errorf("expected the hub's 403 to pass through; got: %s", text)
	}
}

func TestUpdateMCPServer_ManagedConflictPassthrough(t *testing.T) {
	hub, _ := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusOK, `{"name":"hub-chat","url":"http://hub/mcp","managedBy":"hub"}`},
		http.MethodPut: {http.StatusConflict, `{"error":"mcp server is managed by helm and cannot be modified"}`},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "hub-chat", "url": "http://evil.svc/mcp"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := mcpServerText(result)
	if !strings.Contains(text, "409") || !strings.Contains(text, "managed by helm") {
		t.Errorf("expected the hub's 409 to pass through; got: %s", text)
	}
}

func TestUpdateMCPServer_NotFoundOnRead(t *testing.T) {
	hub, seen := stubHubMethods(t, map[string]stubReply{
		http.MethodGet: {http.StatusNotFound, `{"error":"mcp server not found"}`},
		http.MethodPut: {http.StatusOK, `{}`},
	})
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.UpdateMCPServer, map[string]any{"name": "nope", "url": "http://x.svc/mcp"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(mcpServerText(result), "404") {
		t.Errorf("expected the hub's 404; got: %s", mcpServerText(result))
	}
	if seen[http.MethodPut] != nil {
		t.Error("expected no PUT after a failed read")
	}
}

func TestDeleteMCPServer(t *testing.T) {
	hub, rec := stubHub(t, http.StatusNoContent, ``)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, err := callMCPServer(t, tt, tt.DeleteMCPServer, map[string]any{"name": "forgejo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", mcpServerText(result))
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/mcp-servers/forgejo" {
		t.Errorf("expected DELETE /api/v1/mcp-servers/forgejo, got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(mcpServerText(result), "deleted") {
		t.Errorf("expected a deletion confirmation; got: %s", mcpServerText(result))
	}
}

func TestDeleteMCPServer_MissingName(t *testing.T) {
	tt := &MCPServerTools{HubURL: "http://unused", HTTPClient: http.DefaultClient}
	result, _ := callMCPServer(t, tt, tt.DeleteMCPServer, map[string]any{})
	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

func TestDeleteMCPServer_ReferencedByImagePassthrough(t *testing.T) {
	// The hub names the blocking images in the 409 body; the caller needs them
	// to know what to detach first.
	hub, _ := stubHub(t, http.StatusConflict, `{"error":"mcp server referenced by agent image(s)","affectedImages":["pi","claude-code"]}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.DeleteMCPServer, map[string]any{"name": "forgejo"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	text := mcpServerText(result)
	for _, want := range []string{"409", "referenced by agent image", "claude-code"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in the passed-through 409; got: %s", want, text)
		}
	}
}

func TestDeleteMCPServer_NotFoundPassthrough(t *testing.T) {
	hub, _ := stubHub(t, http.StatusNotFound, `{"error":"mcp server not found"}`)
	tt := &MCPServerTools{HubURL: hub.URL, HTTPClient: hub.Client()}
	result, _ := callMCPServer(t, tt, tt.DeleteMCPServer, map[string]any{"name": "nope"})
	if !result.IsError {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(mcpServerText(result), "404") {
		t.Errorf("expected the hub's 404; got: %s", mcpServerText(result))
	}
}
