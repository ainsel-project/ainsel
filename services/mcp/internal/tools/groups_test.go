package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// handlerFunc is a GroupTools tool handler.
type handlerFunc = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// callGroup invokes a GroupTools handler with the given arguments.
func callGroup(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

// groupText returns the tool result text, failing the test if the result is an error.
func groupText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	return result.Content[0].(mcp.TextContent).Text
}

func groupErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("expected error result, got: %s", result.Content[0].(mcp.TextContent).Text)
	}
	return result.Content[0].(mcp.TextContent).Text
}

func groupsJSON(n int) string {
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, fmt.Sprintf(`{"id":"grp-%03d","name":"group-%03d","description":""}`, i, i))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestListGroups_ForwardsGETAndWrapsBareArray(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `[{"id":"grp-1","name":"platform","description":"shared resources"},{"id":"grp-2","name":"apps","description":""}]`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.ListGroups, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/groups" {
		t.Errorf("expected GET /api/v1/groups; got %s %s", rec.method, rec.path)
	}
	var out struct {
		Items     []map[string]any `json:"items"`
		Truncated bool             `json:"truncated"`
		Returned  int              `json:"returned"`
	}
	if err := json.Unmarshal([]byte(groupText(t, result)), &out); err != nil {
		t.Fatalf("response is not the wrapped list shape: %v", err)
	}
	if len(out.Items) != 2 || out.Items[1]["name"] != "apps" {
		t.Errorf("expected both groups under items; got: %s", groupText(t, result))
	}
	if out.Truncated || out.Returned != 2 {
		t.Errorf("expected truncated=false returned=2; got %+v", out)
	}
}

func TestListGroups_AppliesDefaultLimit(t *testing.T) {
	hub, _ := stubHub(t, http.StatusOK, groupsJSON(51))
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.ListGroups, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out struct {
		Items     []map[string]any `json:"items"`
		Truncated bool             `json:"truncated"`
		Returned  int              `json:"returned"`
		Total     int              `json:"total"`
		Hint      string           `json:"hint"`
	}
	if err := json.Unmarshal([]byte(groupText(t, result)), &out); err != nil {
		t.Fatalf("response is not the wrapped list shape: %v", err)
	}
	if out.Returned != 50 || len(out.Items) != 50 {
		t.Errorf("expected the default cap of 50 groups; got returned=%d items=%d", out.Returned, len(out.Items))
	}
	if !out.Truncated || out.Total != 51 {
		t.Errorf("expected truncated=true total=51; got %+v", out)
	}
	if !strings.Contains(out.Hint, "limit=") {
		t.Errorf("expected a truncation hint naming limit; got: %q", out.Hint)
	}
}

func TestListGroups_HonoursLimitArg(t *testing.T) {
	hub, _ := stubHub(t, http.StatusOK, groupsJSON(5))
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.ListGroups, map[string]any{"limit": float64(2)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out struct {
		Returned int `json:"returned"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal([]byte(groupText(t, result)), &out); err != nil {
		t.Fatalf("response is not the wrapped list shape: %v", err)
	}
	if out.Returned != 2 || out.Total != 5 {
		t.Errorf("expected returned=2 total=5; got %+v", out)
	}
}

func TestGetGroup(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{"group":{"id":"grp-1","name":"apps","description":"app teams"},"members":[{"user":{"id":"u-1","name":"Dominik"},"role":"owner"}]}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.GetGroup, map[string]any{"id": "grp-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/groups/grp-1" {
		t.Errorf("expected GET /api/v1/groups/grp-1; got %s %s", rec.method, rec.path)
	}
	text := groupText(t, result)
	if !strings.Contains(text, `"name":"apps"`) || !strings.Contains(text, `"role":"owner"`) {
		t.Errorf("expected group and members verbatim from the hub; got: %s", text)
	}
}

func TestGetGroup_MissingID(t *testing.T) {
	hub, rec := stubHub(t, http.StatusOK, `{}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.GetGroup, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(groupErrorText(t, result), "id is required") {
		t.Errorf("expected id-is-required error")
	}
	if rec.method != "" {
		t.Errorf("expected no hub call; got %s %s", rec.method, rec.path)
	}
}

func TestCreateGroup_SendsRequestBody(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"grp-42","name":"apps","description":"app teams"}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.CreateGroup, map[string]any{"name": "apps", "description": "app teams"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/groups" {
		t.Errorf("expected POST /api/v1/groups; got %s %s", rec.method, rec.path)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %q", rec.body)
	}
	if len(sent) != 2 || sent["name"] != "apps" || sent["description"] != "app teams" {
		t.Errorf("expected exactly name+description in the request body; got: %s", rec.body)
	}
	if text := groupText(t, result); !strings.Contains(text, "grp-42") {
		t.Errorf("expected the created group returned verbatim; got: %s", text)
	}
}

func TestCreateGroup_OmitsAbsentDescription(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"grp-43","name":"apps","description":""}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	if _, err := callGroup(t, gt.CreateGroup, map[string]any{"name": "apps"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %q", rec.body)
	}
	if _, ok := sent["description"]; ok {
		t.Errorf("expected description omitted when the argument is absent; got: %s", rec.body)
	}
}

func TestCreateGroup_SendsExplicitEmptyDescription(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{"id":"grp-44","name":"apps","description":""}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	if _, err := callGroup(t, gt.CreateGroup, map[string]any{"name": "apps", "description": ""}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %q", rec.body)
	}
	v, ok := sent["description"]
	if !ok || v != "" {
		t.Errorf(`expected description sent as "" when the argument is present; got: %s`, rec.body)
	}
}

func TestCreateGroup_MissingName(t *testing.T) {
	hub, rec := stubHub(t, http.StatusCreated, `{}`)
	gt := NewGroupTools(hub.URL)
	gt.HTTPClient = hub.Client()

	result, err := callGroup(t, gt.CreateGroup, map[string]any{"description": "no name"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(groupErrorText(t, result), "name is required") {
		t.Errorf("expected name-is-required error")
	}
	if rec.method != "" {
		t.Errorf("expected no hub call; got %s %s", rec.method, rec.path)
	}
}

// The hub is the only authorizer: whatever status it answers with reaches the
// caller as the tool error text, so a non-admin sees the same 403 here as over
// REST (issue #326 acceptance criterion 4).
func TestGroupTools_403Passthrough(t *testing.T) {
	cases := []struct {
		name string
		call func(*GroupTools) handlerFunc
		args map[string]any
	}{
		{"list_groups", func(gt *GroupTools) handlerFunc { return gt.ListGroups }, map[string]any{}},
		{"get_group", func(gt *GroupTools) handlerFunc { return gt.GetGroup }, map[string]any{"id": "grp-1"}},
		{"create_group", func(gt *GroupTools) handlerFunc { return gt.CreateGroup }, map[string]any{"name": "apps"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub, _ := stubHub(t, http.StatusForbidden, `{"error":"admin access required"}`)
			gt := NewGroupTools(hub.URL)
			gt.HTTPClient = hub.Client()

			result, err := callGroup(t, tc.call(gt), tc.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := groupErrorText(t, result)
			if !strings.Contains(text, "403") || !strings.Contains(text, "admin access required") {
				t.Errorf("expected the hub's 403 and message in the tool error; got: %s", text)
			}
		})
	}
}
