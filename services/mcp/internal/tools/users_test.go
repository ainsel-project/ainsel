package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// usersStub answers the hub's /api/v1/users surface. count controls the size of
// the (unpaginated) list; the handler records the last request for assertions.
type usersStub struct {
	server   *httptest.Server
	lastReq  *http.Request
	lastBody map[string]any
	users    []map[string]any
}

func newUsersStub(t *testing.T, count int) *usersStub {
	t.Helper()
	s := &usersStub{}
	for i := range count {
		s.users = append(s.users, map[string]any{
			"sub":       fmt.Sprintf("u-%03d", i),
			"username":  fmt.Sprintf("user%03d", i),
			"roles":     []string{"user"},
			"enabled":   true,
			"managedBy": "user",
		})
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastReq = r
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users":
			_ = json.NewEncoder(w).Encode(s.users)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/users":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": "u-new", "username": s.lastBody["username"]})
		case r.Method == http.MethodGet && id == "u-404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"user not found"}`))
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": id, "username": "user"})
		case r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": id, "patched": s.lastBody})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// userToolText calls a tool and fails the test if it returned an error result.
func userToolText(t *testing.T, fn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	res := callTool(t, fn, args)
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	return resultText(res)
}

// userToolError calls a tool and fails the test unless it returned an error
// result, returning that message so the caller can assert on the hub's status.
func userToolError(t *testing.T, fn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	res := callTool(t, fn, args)
	if !res.IsError {
		t.Fatalf("expected an error result, got: %s", resultText(res))
	}
	return resultText(res)
}

func TestListUsersWindowsUnpaginatedHubList(t *testing.T) {
	stub := newUsersStub(t, 120)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}

	text := userToolText(t, ut.ListUsers, map[string]any{"page": float64(2), "limit": float64(10)})

	if stub.lastReq.URL.RawQuery != "" {
		t.Errorf("page/limit must not be sent to the unpaginated hub endpoint; query was %q", stub.lastReq.URL.RawQuery)
	}
	var out struct {
		Items     []map[string]any `json:"items"`
		Total     int              `json:"total"`
		Page      int              `json:"page"`
		PageSize  int              `json:"pageSize"`
		Truncated bool             `json:"truncated"`
		Hint      string           `json:"hint"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not the expected envelope: %v\n%s", err, text)
	}
	if len(out.Items) != 10 || out.Items[0]["sub"] != "u-010" {
		t.Errorf("expected the page-2 window (u-010..u-019), got %d items starting at %v", len(out.Items), out.Items[0])
	}
	if out.Total != 120 || out.Page != 2 || out.PageSize != 10 || !out.Truncated || out.Hint == "" {
		t.Errorf("expected total=120, page=2, pageSize=10, truncated + hint; got %s", text)
	}
}

func TestListUsersDefaultLimit(t *testing.T) {
	stub := newUsersStub(t, 60)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	text := userToolText(t, ut.ListUsers, nil)
	if strings.Contains(text, "u-050") {
		t.Errorf("default limit of 50 exceeded; got: %s", text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Errorf("expected truncated=true for a 60-user list at limit 50; got: %s", text)
	}
}

func TestGetUserPassesThrough404(t *testing.T) {
	stub := newUsersStub(t, 1)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	msg := userToolError(t, ut.GetUser, map[string]any{"id": "u-404"})
	if !strings.Contains(msg, "404") || !strings.Contains(msg, "user not found") {
		t.Errorf("expected the hub's 404 body to pass through; got: %s", msg)
	}
}

func TestGetUserRequiresID(t *testing.T) {
	stub := newUsersStub(t, 1)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	if msg := userToolError(t, ut.GetUser, map[string]any{}); !strings.Contains(msg, "id is required") {
		t.Errorf("expected a missing-id error; got: %s", msg)
	}
}

func TestCreateUserSendsOnlyProvidedFields(t *testing.T) {
	stub := newUsersStub(t, 0)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	text := userToolText(t, ut.CreateUser, map[string]any{
		"username":     "newbie",
		"password":     "correct-horse",
		"display_name": "New Bie",
		"roles":        []any{"user", "admin"},
	})
	if !strings.Contains(text, "newbie") {
		t.Errorf("expected the created user back; got: %s", text)
	}
	if stub.lastBody["username"] != "newbie" || stub.lastBody["password"] != "correct-horse" {
		t.Errorf("expected username+password in the request body, got %v", stub.lastBody)
	}
	if _, ok := stub.lastBody["email"]; ok {
		t.Errorf("an omitted email must not be sent; body was %v", stub.lastBody)
	}
	if roles, ok := stub.lastBody["roles"].([]any); !ok || len(roles) != 2 {
		t.Errorf("expected roles forwarded as a list, got %v", stub.lastBody["roles"])
	}
}

func TestCreateUserRequiresUsernameAndPassword(t *testing.T) {
	stub := newUsersStub(t, 0)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	if msg := userToolError(t, ut.CreateUser, map[string]any{"username": "x"}); !strings.Contains(msg, "username and password are required") {
		t.Errorf("expected a required-fields error; got: %s", msg)
	}
}

func TestUpdateUserPresenceBasedFields(t *testing.T) {
	stub := newUsersStub(t, 0)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}

	userToolText(t, ut.UpdateUser, map[string]any{"id": "u-1", "enabled": false})
	if _, ok := stub.lastBody["email"]; ok {
		t.Errorf("omitted email must not be sent; body was %v", stub.lastBody)
	}
	if v, ok := stub.lastBody["enabled"].(bool); !ok || v {
		t.Errorf(`expected "enabled": false to be sent, got %v`, stub.lastBody)
	}

	if msg := userToolError(t, ut.UpdateUser, map[string]any{"id": "u-1"}); !strings.Contains(msg, "at least one of") {
		t.Errorf("expected an empty-patch error; got: %s", msg)
	}
}

func TestUpdateUserPassesThrough409(t *testing.T) {
	stub := newUsersStub(t, 0)
	stub.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"cannot disable the last admin"}`))
	})
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	msg := userToolError(t, ut.UpdateUser, map[string]any{"id": "u-1", "enabled": false})
	if !strings.Contains(msg, "409") || !strings.Contains(msg, "last admin") {
		t.Errorf("expected the hub's 409 to pass through; got: %s", msg)
	}
}

func TestDeleteUser(t *testing.T) {
	stub := newUsersStub(t, 0)
	ut := &UserTools{HubURL: stub.server.URL, HTTPClient: stub.server.Client()}
	text := userToolText(t, ut.DeleteUser, map[string]any{"id": "u-7"})
	if !strings.Contains(text, "u-7 deleted") {
		t.Errorf("expected a deletion confirmation; got: %s", text)
	}
	if stub.lastReq.Method != http.MethodDelete || stub.lastReq.URL.Path != "/api/v1/users/u-7" {
		t.Errorf("unexpected request %s %s", stub.lastReq.Method, stub.lastReq.URL.Path)
	}
}
