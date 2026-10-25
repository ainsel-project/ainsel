package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// groupResAuthzFake models a small authz graph for the group-resources
// endpoint. It implements only what the handler reaches; the embedded nil
// interface makes any other call panic rather than silently pass.
type groupResAuthzFake struct {
	authzStore

	groups    map[string]bool
	admins    map[string]bool
	roles     map[string]map[string]authz.GroupRole // subject -> groupID -> role
	resources map[string]string                     // "type/name" -> groupID
	public    map[string]bool
}

func newGroupResAuthzFake() *groupResAuthzFake {
	return &groupResAuthzFake{
		groups:    map[string]bool{},
		admins:    map[string]bool{},
		roles:     map[string]map[string]authz.GroupRole{},
		resources: map[string]string{},
		public:    map[string]bool{},
	}
}

func (f *groupResAuthzFake) add(groupID, resourceType, name string, memberSubs ...string) {
	f.groups[groupID] = true
	f.resources[resourceType+"/"+name] = groupID
	for _, sub := range memberSubs {
		if f.roles[sub] == nil {
			f.roles[sub] = map[string]authz.GroupRole{}
		}
		f.roles[sub][groupID] = authz.RoleReader
	}
}

func (f *groupResAuthzFake) setPublic(resourceType, name string) {
	f.public[resourceType+"/"+name] = true
}

func (f *groupResAuthzFake) GetGroup(_ context.Context, id string) (*authz.Group, error) {
	if !f.groups[id] {
		return nil, authz.ErrNotFound
	}
	return &authz.Group{ID: id, Name: id}, nil
}

func (f *groupResAuthzFake) GetUser(_ context.Context, id string) (*authz.User, error) {
	return &authz.User{ID: id, Username: id, IsAdmin: f.admins[id]}, nil
}

func (f *groupResAuthzFake) UserGroupIDs(_ context.Context, sub string) ([]string, error) {
	var out []string
	for gid := range f.roles[sub] {
		out = append(out, gid)
	}
	sort.Strings(out)
	return out, nil
}

func (f *groupResAuthzFake) UserGroupRoles(_ context.Context, sub string) (map[string]authz.GroupRole, error) {
	return f.roles[sub], nil
}

func (f *groupResAuthzFake) GetResourceGroup(_ context.Context, resourceType, name string) (*authz.ResourceGroup, error) {
	key := resourceType + "/" + name
	groupID, ok := f.resources[key]
	if !ok {
		return nil, authz.ErrNotFound
	}
	return &authz.ResourceGroup{ResourceType: resourceType, ResourceName: name, GroupID: groupID, Public: f.public[key]}, nil
}

func (f *groupResAuthzFake) ListResourcesByGroups(_ context.Context, resourceType string, groupIDs []string, includePublic bool) ([]string, error) {
	wanted := map[string]bool{}
	for _, g := range groupIDs {
		wanted[g] = true
	}
	var out []string
	for key, gid := range f.resources {
		rt, rn, found := strings.Cut(key, "/")
		if !found || rt != resourceType {
			continue
		}
		if wanted[gid] || (includePublic && f.public[key]) {
			out = append(out, rn)
		}
	}
	sort.Strings(out)
	return out, nil
}

// groupResPersonaFake implements PersonaService. Like the real store, List
// returns only shared template personas — agent-owned personas are excluded.
type groupResPersonaFake struct {
	shared []personas.PersonaSummary
}

func (f *groupResPersonaFake) List(_ context.Context) ([]personas.PersonaSummary, error) {
	return f.shared, nil
}

func (f *groupResPersonaFake) Create(context.Context, personas.CreateRequest) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) Get(context.Context, string) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) Update(context.Context, string, personas.UpdateRequest) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) Delete(context.Context, string) error { panic("unused") }
func (f *groupResPersonaFake) ListVersions(context.Context, string) ([]personas.VersionSummary, error) {
	panic("unused")
}
func (f *groupResPersonaFake) GetVersion(context.Context, string, int) (*personas.Version, error) {
	panic("unused")
}
func (f *groupResPersonaFake) Rollback(context.Context, string, int) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) OwnedBy(context.Context, string) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) EnsureOwned(context.Context, string, string, personas.UpdateRequest) (*personas.Persona, error) {
	panic("unused")
}
func (f *groupResPersonaFake) DeleteOwned(context.Context, string, string) error {
	panic("unused")
}

// groupResSkillFake implements SkillService; only List is meaningful.
type groupResSkillFake struct {
	items []skills.SkillSummary
}

func (f *groupResSkillFake) List(_ context.Context, _ skills.ListFilter) ([]skills.SkillSummary, error) {
	return f.items, nil
}

func (f *groupResSkillFake) Create(context.Context, skills.CreateRequest) (*skills.Skill, error) {
	panic("unused")
}
func (f *groupResSkillFake) Get(context.Context, string) (*skills.Skill, error) { panic("unused") }
func (f *groupResSkillFake) Update(context.Context, string, skills.UpdateRequest) (*skills.Skill, error) {
	panic("unused")
}
func (f *groupResSkillFake) Delete(context.Context, string) error { panic("unused") }
func (f *groupResSkillFake) ListAssignments(context.Context, string) ([]skills.Referrer, error) {
	panic("unused")
}
func (f *groupResSkillFake) Assign(context.Context, string, string) error   { panic("unused") }
func (f *groupResSkillFake) Unassign(context.Context, string, string) error { panic("unused") }

// groupResTestServer wires the endpoint over a fake authz graph and the given
// k8s objects, plus persona/skill fakes.
func groupResTestServer(t *testing.T, fake *groupResAuthzFake, objects ...runtime.Object) *Server {
	t.Helper()
	s := testServer(t, objects...)
	s.authzStore = fake
	s.authzChecker = authz.NewChecker(fake, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Hour))
	s.mux.HandleFunc("/api/v1/groups/", s.handleGroup)
	return s
}

func getGroupResources(t *testing.T, s *Server, path, sub string) (int, []GroupResourceItem) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if sub != "" {
		req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: sub, Username: sub}))
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	var out struct {
		Items []GroupResourceItem `json:"items"`
	}
	if rec.Body.Len() > 0 && rec.Code/100 == 2 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out.Items
}

func itemSet(items []GroupResourceItem) map[string]GroupResourceItem {
	set := map[string]GroupResourceItem{}
	for _, it := range items {
		set[it.Type+"/"+it.ID] = it
	}
	return set
}

func agentObj(name, displayName string) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec:       agentv1alpha1.AgentSpec{DisplayName: displayName},
	}
}

func TestGroupResources_MemberSeesAllTypes(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-1", "alice")
	fake.add("g1", "agent", "a-2", "alice")
	fake.add("g1", "agent-image", "img-1", "alice")
	fake.add("g1", "connector", "c-1", "alice")
	fake.add("g1", "persona", "p-1", "alice")
	fake.add("g1", "skill", "s-1", "alice")
	fake.setPublic("agent", "a-2")

	s := groupResTestServer(t, fake,
		agentObj("a-1", "Review Bot"), // displayName wins
		agentObj("a-2", ""),           // falls back to metadata.name
		&agentv1alpha1.AgentImage{ObjectMeta: metav1.ObjectMeta{Name: "img-1", Namespace: "test-ns"}, Spec: agentv1alpha1.AgentImageSpec{DisplayName: "Base Image"}},
		&agentv1alpha1.WebhookConnector{ObjectMeta: metav1.ObjectMeta{Name: "c-1", Namespace: "test-ns"}, Spec: agentv1alpha1.WebhookConnectorSpec{DisplayName: "Forgejo Hook"}},
	)
	s.personas = &groupResPersonaFake{shared: []personas.PersonaSummary{{ID: "p-1", Name: "Doc Writer"}}}
	s.skills = &groupResSkillFake{items: []skills.SkillSummary{{ID: "s-1", Name: "Code Review"}}}

	code, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if code != http.StatusOK {
		t.Fatalf("status: %d body: %s", code, "")
	}
	set := itemSet(items)
	if len(items) != 6 {
		t.Fatalf("want 6 items, got %d: %+v", len(items), items)
	}
	if got := set["agent/a-1"].Name; got != "Review Bot" {
		t.Errorf("agent display name = %q, want %q", got, "Review Bot")
	}
	if got := set["agent/a-2"].Name; got != "a-2" {
		t.Errorf("agent fallback name = %q, want %q", got, "a-2")
	}
	if !set["agent/a-2"].Public {
		t.Error("public agent not flagged public")
	}
	if set["agent/a-1"].Public {
		t.Error("non-public agent flagged public")
	}
	if got := set["agent-image/img-1"].Name; got != "Base Image" {
		t.Errorf("agent-image name = %q, want %q", got, "Base Image")
	}
	if got := set["connector/c-1"].Name; got != "Forgejo Hook" {
		t.Errorf("connector name = %q, want %q", got, "Forgejo Hook")
	}
	if got := set["persona/p-1"].Name; got != "Doc Writer" {
		t.Errorf("persona name = %q, want %q", got, "Doc Writer")
	}
	if got := set["skill/s-1"].Name; got != "Code Review" {
		t.Errorf("skill name = %q, want %q", got, "Code Review")
	}
}

func TestGroupResources_NonMemberSeesNothing(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-1", "alice") // bob is not a member
	s := groupResTestServer(t, fake, agentObj("a-1", "Review Bot"))

	code, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "bob")
	if code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	if len(items) != 0 {
		t.Fatalf("non-member should see nothing, got %+v", items)
	}
}

func TestGroupResources_NonMemberIncludePublicSeesPublicOnly(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-public", "alice")
	fake.add("g1", "agent", "a-private", "alice")
	fake.setPublic("agent", "a-public")
	s := groupResTestServer(t, fake, agentObj("a-public", "Pub"), agentObj("a-private", "Priv"))

	_, items := getGroupResources(t, s, "/api/v1/groups/g1/resources?includePublic=true", "bob")
	if len(items) != 1 || items[0].ID != "a-public" || !items[0].Public {
		t.Fatalf("non-member with includePublic should see only the public item, got %+v", items)
	}
}

func TestGroupResources_AdminSeesAll(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-1", "alice")
	fake.admins["root"] = true
	s := groupResTestServer(t, fake, agentObj("a-1", "Review Bot"))

	_, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "root")
	if len(items) != 1 || items[0].ID != "a-1" {
		t.Fatalf("admin should see all, got %+v", items)
	}
}

func TestGroupResources_StaleRowsDropped(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-gone", "alice") // no Agent CR exists
	fake.add("g1", "agent", "a-live", "alice")
	s := groupResTestServer(t, fake, agentObj("a-live", "Live"))

	_, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if len(items) != 1 || items[0].ID != "a-live" {
		t.Fatalf("stale row must be dropped, not rendered as a bare id, got %+v", items)
	}
}

func TestGroupResources_AgentOwnedPersonaSkipped(t *testing.T) {
	// The persona store's List excludes agent-owned personas; a group row for
	// such a persona must resolve to nothing and be dropped, so it cannot
	// double-count against its owning agent.
	fake := newGroupResAuthzFake()
	fake.add("g1", "persona", "p-owned", "alice")
	fake.add("g1", "persona", "p-shared", "alice")
	s := groupResTestServer(t, fake)
	s.personas = &groupResPersonaFake{shared: []personas.PersonaSummary{{ID: "p-shared", Name: "Shared"}}}

	_, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if len(items) != 1 || items[0].ID != "p-shared" {
		t.Fatalf("agent-owned persona must be skipped, got %+v", items)
	}
}

func TestGroupResources_EmptyGroup(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-1", "alice")
	fake.groups["g2"] = true // exists but owns nothing
	s := groupResTestServer(t, fake, agentObj("a-1", "Review Bot"))

	code, items := getGroupResources(t, s, "/api/v1/groups/g2/resources", "alice")
	if code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	if items == nil {
		t.Fatal("items must serialize as [] not null")
	}
	if len(items) != 0 {
		t.Fatalf("other group's resources must not leak, got %+v", items)
	}
}

func TestGroupResources_UnknownGroup404(t *testing.T) {
	fake := newGroupResAuthzFake()
	s := groupResTestServer(t, fake)

	code, _ := getGroupResources(t, s, "/api/v1/groups/nope/resources", "alice")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestGroupResources_CapPerType(t *testing.T) {
	fake := newGroupResAuthzFake()
	var objects []runtime.Object
	for i := 0; i < groupResourceCap+5; i++ {
		name := fmt.Sprintf("a-%03d", i)
		fake.add("g1", "agent", name, "alice")
		objects = append(objects, agentObj(name, ""))
	}
	s := groupResTestServer(t, fake, objects...)

	_, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if len(items) != groupResourceCap {
		t.Fatalf("want cap of %d items, got %d", groupResourceCap, len(items))
	}
}

func TestGroupResources_MethodNotAllowed(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "agent", "a-1", "alice")
	s := groupResTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups/g1/resources", nil)
	req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: "alice"}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
