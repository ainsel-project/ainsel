package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// The roster at /agents is paginated server-side, so search and groupId must
// be applied by the hub before paging: filtering a single page client-side
// would leak matches on other pages and leave Pager totals wrong. These tests
// pin the filter semantics — filters compose with pagination, and a groupId
// the caller cannot read yields nothing rather than another tenant's agents.

// agentFilterAuthzFake is a small in-memory authz graph for the list filters.
// It embeds the full authzStore interface so any method it does not implement
// panics loudly instead of returning a zero value a test could misread.
type agentFilterAuthzFake struct {
	authzStore

	// admins maps a subject to its admin flag.
	admins map[string]bool
	// roles maps subject -> groupID -> role.
	roles map[string]map[string]authz.GroupRole
	// resources maps "agent/<name>" -> groupID.
	resources map[string]string
	// groups maps groupID -> display name.
	groups map[string]string
}

func (f *agentFilterAuthzFake) GetUser(_ context.Context, id string) (*authz.User, error) {
	return &authz.User{ID: id, Username: id, IsAdmin: f.admins[id]}, nil
}

func (f *agentFilterAuthzFake) UserGroupIDs(_ context.Context, sub string) ([]string, error) {
	var out []string
	for groupID := range f.roles[sub] {
		out = append(out, groupID)
	}
	sort.Strings(out)
	return out, nil
}

func (f *agentFilterAuthzFake) UserGroupRoles(_ context.Context, sub string) (map[string]authz.GroupRole, error) {
	return f.roles[sub], nil
}

func (f *agentFilterAuthzFake) ListResourcesByGroups(_ context.Context, resourceType string, groupIDs []string, _ bool) ([]string, error) {
	wanted := make(map[string]bool, len(groupIDs))
	for _, g := range groupIDs {
		wanted[g] = true
	}
	var out []string
	for key, groupID := range f.resources {
		rt, rn, found := strings.Cut(key, "/")
		if !found || rt != resourceType {
			continue
		}
		if wanted[groupID] {
			out = append(out, rn)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *agentFilterAuthzFake) ListResourceGroupsByType(_ context.Context, resourceType string) (map[string]string, error) {
	out := make(map[string]string)
	for key, groupID := range f.resources {
		rt, rn, found := strings.Cut(key, "/")
		if found && rt == resourceType {
			out[rn] = groupID
		}
	}
	return out, nil
}

func (f *agentFilterAuthzFake) ListGroups(_ context.Context) ([]authz.Group, error) {
	out := make([]authz.Group, 0, len(f.groups))
	for id, name := range f.groups {
		out = append(out, authz.Group{ID: id, Name: name})
	}
	return out, nil
}

// filterTestAgent builds a minimal Agent CR for the filter tests.
func filterTestAgent(name, displayName, description string) *agentv1alpha1.Agent {
	a := &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
	}
	a.APIVersion = "ainsel.dev/v1alpha1"
	a.Kind = "Agent"
	a.Spec.DisplayName = displayName
	a.Spec.Description = description
	a.Spec.LLM.Model = "test-model"
	return a
}

// newAgentFilterEnv wires a Server over the fake graph above. No Postgres:
// the fake states the permissions directly, and the filter logic under test
// lives entirely in the handler.
func newAgentFilterEnv(t *testing.T, agents []*agentv1alpha1.Agent, fake *agentFilterAuthzFake) *Server {
	t.Helper()
	objs := make([]runtime.Object, 0, len(agents))
	for _, a := range agents {
		objs = append(objs, a)
	}
	s := testServer(t, objs...)
	if fake != nil {
		s.authzStore = fake
		checker := authz.NewChecker(fake, authz.NewGroupCache(
			func(userID string) (map[string]authz.GroupRole, error) {
				return fake.UserGroupRoles(context.Background(), userID)
			}, time.Minute))
		s.authzChecker = checker
	}
	s.SetAuthMiddleware(func(next http.Handler) http.Handler { return next })
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	return s
}

type agentListPage struct {
	Items      []SimpleAgentResponse `json:"items"`
	Total      int                   `json:"total"`
	Page       int                   `json:"page"`
	PageSize   int                   `json:"pageSize"`
	TotalPages int                   `json:"totalPages"`
}

// listAgentsAs issues GET /api/v1agents?<query> as the given subject (empty =
// anonymous, which the no-authz-server path treats as unscoped).
func listAgentsAs(t *testing.T, s *Server, sub, query string) (int, agentListPage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents?"+query, nil)
	if sub != "" {
		req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: sub, Username: sub}))
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	var page agentListPage
	if rec.Body.Len() > 0 && rec.Code/100 == 2 {
		if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
			t.Fatalf("decode list response: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, page
}

func itemNames(page agentListPage) []string {
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it.Name)
	}
	return out
}

// Without authz the search filter still applies, before paging.
func TestListAgents_SearchFiltersBeforePaging(t *testing.T) {
	s := newAgentFilterEnv(t, []*agentv1alpha1.Agent{
		filterTestAgent("a-1", "Alpha Writer", "writes docs"),
		filterTestAgent("a-2", "Triage Bot", "triages incoming tasks"),
		filterTestAgent("a-3", "alpha-reader", "reads docs back"),
	}, nil)

	code, page := listAgentsAs(t, s, "", "search=alpha")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	// Case-insensitive on display name; "alpha-reader" matches too.
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("expected 2 matches, got total=%d items=%v", page.Total, itemNames(page))
	}

	// Description match.
	_, page = listAgentsAs(t, s, "", "search=triage")
	if page.Total != 1 || page.Items[0].Name != "Triage Bot" {
		t.Fatalf("expected description match, got %v", itemNames(page))
	}

	// Search composes with pagination: total reflects the filtered set and
	// page 2 of the filtered set is the second match.
	_, page = listAgentsAs(t, s, "", "search=alpha&page=1&pageSize=1")
	if page.Total != 2 || page.TotalPages != 2 || len(page.Items) != 1 {
		t.Fatalf("expected filtered total=2 totalPages=2, got %+v", page)
	}
	_, page = listAgentsAs(t, s, "", "search=alpha&page=2&pageSize=1")
	if len(page.Items) != 1 || page.Items[0].Name != "alpha-reader" {
		t.Fatalf("expected page 2 = alpha-reader, got %v", itemNames(page))
	}

	// Empty result.
	code, page = listAgentsAs(t, s, "", "search=zzz")
	if code != http.StatusOK || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("expected empty result, got code=%d page=%+v", code, page)
	}
}

func newFilterFake() *agentFilterAuthzFake {
	f := &agentFilterAuthzFake{
		admins:    map[string]bool{},
		roles:     map[string]map[string]authz.GroupRole{},
		resources: map[string]string{},
		groups:    map[string]string{"g-alice": "Platform", "g-bob": "Data"},
	}
	for _, who := range []string{"alice", "bob"} {
		f.roles[who] = map[string]authz.GroupRole{"g-" + who: authz.RoleReader}
		f.resources["agent/a-"+who] = "g-" + who
	}
	return f
}

func TestListAgents_GroupFilterScopesAndEnriches(t *testing.T) {
	fake := newFilterFake()
	s := newAgentFilterEnv(t, []*agentv1alpha1.Agent{
		filterTestAgent("a-alice", "Alice Agent", "one"),
		filterTestAgent("a-bob", "Bob Agent", "two"),
	}, fake)

	// alice may narrow to her own group; the row carries groupId + groupName.
	code, page := listAgentsAs(t, s, "alice", "groupId=g-alice")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "a-alice" {
		t.Fatalf("expected only a-alice, got %+v", page)
	}
	if page.Items[0].GroupID != "g-alice" || page.Items[0].GroupName != "Platform" {
		t.Errorf("expected group enrichment g-alice/Platform, got %q/%q",
			page.Items[0].GroupID, page.Items[0].GroupName)
	}

	// An unauthorized groupId yields nothing — not bob's agents.
	_, page = listAgentsAs(t, s, "alice", "groupId=g-bob")
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("expected empty result for unauthorized group, got %+v", page)
	}

	// Filters combine: group + search.
	_, page = listAgentsAs(t, s, "alice", "groupId=g-alice&search=bob")
	if page.Total != 0 {
		t.Fatalf("expected group+search to exclude bob, got %+v", page)
	}
	_, page = listAgentsAs(t, s, "alice", "groupId=g-alice&search=alice")
	if page.Total != 1 || page.Items[0].ID != "a-alice" {
		t.Fatalf("expected group+search to keep alice, got %+v", page)
	}

	// Unknown group id: empty, not an error.
	code, page = listAgentsAs(t, s, "alice", "groupId=g-nope")
	if code != http.StatusOK || page.Total != 0 {
		t.Fatalf("expected 200 + empty for unknown group, got code=%d page=%+v", code, page)
	}
}

func TestListAgents_GroupFilterAdminSeesAnyGroup(t *testing.T) {
	fake := newFilterFake()
	fake.admins["root"] = true
	s := newAgentFilterEnv(t, []*agentv1alpha1.Agent{
		filterTestAgent("a-alice", "Alice Agent", "one"),
		filterTestAgent("a-bob", "Bob Agent", "two"),
	}, fake)

	// An admin may narrow to any group, including one they are not a member of.
	code, page := listAgentsAs(t, s, "root", "groupId=g-bob")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if page.Total != 1 || page.Items[0].ID != "a-bob" || page.Items[0].GroupName != "Data" {
		t.Fatalf("expected admin to see a-bob in g-bob, got %+v", page)
	}

	// Without a groupId filter the admin still sees everything, each row
	// enriched with its group.
	_, page = listAgentsAs(t, s, "root", "")
	if page.Total != 2 {
		t.Fatalf("expected 2 agents for admin, got %+v", page)
	}
	byID := map[string]SimpleAgentResponse{}
	for _, it := range page.Items {
		byID[it.ID] = it
	}
	if byID["a-alice"].GroupName != "Platform" || byID["a-bob"].GroupName != "Data" {
		t.Errorf("expected group names on all rows, got %+v", page.Items)
	}
}

// A group with no matching agents must page cleanly, not error.
func TestListAgents_GroupFilterEmptyGroup(t *testing.T) {
	fake := newFilterFake()
	fake.groups["g-empty"] = "Empty"
	fake.roles["alice"]["g-empty"] = authz.RoleReader
	s := newAgentFilterEnv(t, []*agentv1alpha1.Agent{
		filterTestAgent("a-alice", "Alice Agent", "one"),
	}, fake)

	code, page := listAgentsAs(t, s, "alice", "groupId=g-empty")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if page.Total != 0 || page.TotalPages != 0 || len(page.Items) != 0 {
		t.Fatalf("expected clean empty page, got %+v", page)
	}
}
