package personas_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
)

// stubAgentLister returns the configured referrers for ListReferrers calls.
type stubAgentLister struct {
	refs []personas.Referrer
	err  error
}

func (s *stubAgentLister) ListReferrers(ctx context.Context, personaID string) ([]personas.Referrer, error) {
	return s.refs, s.err
}

func newTestService(t *testing.T, lister personas.AgentLister) (*personas.Service, func()) {
	t.Helper()
	store, cleanup := newTestStore(t)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	kc := fake.NewClientBuilder().WithScheme(scheme).Build()
	rec := personas.NewReconciler(kc, "ainsel-test")

	if lister == nil {
		lister = &stubAgentLister{}
	}
	svc := personas.NewService(store, rec, lister)
	return svc, cleanup
}

func TestServiceCreateRendersConfigMap(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p, err := svc.Create(ctx, personas.CreateRequest{Name: "code-reviewer", Text: "Hello."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" {
		t.Error("expected service to generate an ID")
	}
	if p.CurrentVersion != 1 {
		t.Errorf("expected version 1, got %d", p.CurrentVersion)
	}

	var cm corev1.ConfigMap
	if err := svc.Reconciler().Client().Get(ctx,
		types.NamespacedName{Name: personas.ConfigMapName(p.ID), Namespace: "ainsel-test"}, &cm); err != nil {
		t.Fatalf("expected ConfigMap rendered: %v", err)
	}
	if cm.Data["persona.md"] != "Hello." {
		t.Errorf("expected text in ConfigMap, got %q", cm.Data["persona.md"])
	}
}

func TestServiceCreateRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	if _, err := svc.Create(ctx, personas.CreateRequest{Name: "dup", Text: "a"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(ctx, personas.CreateRequest{Name: "dup", Text: "b"})
	if !errors.Is(err, personas.ErrNameTaken) {
		t.Errorf("expected ErrNameTaken, got %v", err)
	}
}

func TestServiceUpdateBumpsConfigMap(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p, err := svc.Create(ctx, personas.CreateRequest{Name: "t", Text: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := svc.Update(ctx, p.ID, personas.UpdateRequest{Text: ptr("v2")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CurrentVersion != 2 {
		t.Errorf("expected version 2, got %d", updated.CurrentVersion)
	}

	var cm corev1.ConfigMap
	if err := svc.Reconciler().Client().Get(ctx,
		types.NamespacedName{Name: personas.ConfigMapName(p.ID), Namespace: "ainsel-test"}, &cm); err != nil {
		t.Fatalf("get cm: %v", err)
	}
	if cm.Data["persona.md"] != "v2" {
		t.Errorf("expected v2 in ConfigMap, got %q", cm.Data["persona.md"])
	}
}

func TestServiceRollbackRendersConfigMap(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p, err := svc.Create(ctx, personas.CreateRequest{Name: "r", Text: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Update(ctx, p.ID, personas.UpdateRequest{Text: ptr("v2")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rolled, err := svc.Rollback(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolled.CurrentVersion != 3 || rolled.Text != "v1" {
		t.Errorf("unexpected rollback result: %+v", rolled)
	}
	var cm corev1.ConfigMap
	if err := svc.Reconciler().Client().Get(ctx,
		types.NamespacedName{Name: personas.ConfigMapName(p.ID), Namespace: "ainsel-test"}, &cm); err != nil {
		t.Fatalf("get cm: %v", err)
	}
	if cm.Data["persona.md"] != "v1" {
		t.Errorf("expected v1 in ConfigMap (rollback target), got %q", cm.Data["persona.md"])
	}
}

func TestServiceDeleteRefusedWhenReferenced(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, &stubAgentLister{
		refs: []personas.Referrer{{AgentName: "code-reviewer-agent"}},
	})
	defer cleanup()
	p, err := svc.Create(ctx, personas.CreateRequest{Name: "p", Text: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	err = svc.Delete(ctx, p.ID)
	var conflictErr *personas.ErrInUse
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ErrInUse, got %v", err)
	}
	if len(conflictErr.Referrers) != 1 || conflictErr.Referrers[0].AgentName != "code-reviewer-agent" {
		t.Errorf("expected referrer list, got %+v", conflictErr.Referrers)
	}
}

func TestServiceDeleteRemovesConfigMap(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p, err := svc.Create(ctx, personas.CreateRequest{Name: "p", Text: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var cm corev1.ConfigMap
	err = svc.Reconciler().Client().Get(ctx,
		types.NamespacedName{Name: personas.ConfigMapName(p.ID), Namespace: "ainsel-test"}, &cm)
	if err == nil {
		t.Errorf("expected ConfigMap to be deleted")
	}
}

func TestServiceValidation(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	cases := []struct {
		name    string
		req     personas.CreateRequest
		wantErr string
	}{
		{"empty name", personas.CreateRequest{Name: "", Text: "x"}, "name"},
		{"empty text", personas.CreateRequest{Name: "n", Text: ""}, "text"},
		{"name too long", personas.CreateRequest{Name: strings.Repeat("a", 201), Text: "x"}, "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(ctx, c.req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			var verr *personas.ValidationError
			if !errors.As(err, &verr) || !strings.Contains(verr.Error(), c.wantErr) {
				t.Errorf("expected validation error mentioning %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestServiceGetAndList(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p1, err := svc.Create(ctx, personas.CreateRequest{Name: "a", Text: "x"})
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if _, err := svc.Create(ctx, personas.CreateRequest{Name: "b", Text: "y"}); err != nil {
		t.Fatalf("Create b: %v", err)
	}
	got, err := svc.Get(ctx, p1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "a" {
		t.Errorf("expected name=a, got %q", got.Name)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 personas, got %d", len(list))
	}
}

// --- issue #146: externally managed personas ---------------------------------

// TestServiceMutationsRefuseManagedPersona: a persona whose row carries an
// external managedBy (seeded directly, since no API path can set it) refuses
// Update, Rollback, and Delete with a ManagedError naming the manager, and the
// row survives untouched.
func TestServiceMutationsRefuseManagedPersona(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	kc := fake.NewClientBuilder().WithScheme(scheme).Build()
	svc := personas.NewService(personas.NewStore(pool), personas.NewReconciler(kc, "ainsel-test"), &stubAgentLister{})
	ctx := context.Background()

	p, err := svc.Create(ctx, personas.CreateRequest{Name: "ext", Text: "external text"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ManagedBy != "" && p.ManagedBy != "user" {
		t.Fatalf("fresh persona should be console-managed, got %q", p.ManagedBy)
	}
	if _, err := pool.Exec(ctx, `UPDATE personas SET managed_by = 'gitops' WHERE id = $1`, p.ID); err != nil {
		t.Fatalf("seed managed_by: %v", err)
	}

	got, err := svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ManagedBy != "gitops" {
		t.Errorf("Get must expose managedBy, got %q", got.ManagedBy)
	}

	var mErr *personas.ManagedError
	if _, err := svc.Update(ctx, p.ID, personas.UpdateRequest{Text: ptr("mine")}); !errors.As(err, &mErr) {
		t.Errorf("Update: expected ManagedError, got %T: %v", err, err)
	} else if mErr.Manager != "gitops" || mErr.Error() != "persona is managed by gitops" {
		t.Errorf("Update refusal: %v", mErr)
	}
	if _, err := svc.Rollback(ctx, p.ID, 1); !errors.As(err, &mErr) {
		t.Errorf("Rollback: expected ManagedError, got %T: %v", err, err)
	}
	if err := svc.Delete(ctx, p.ID); !errors.As(err, &mErr) {
		t.Errorf("Delete: expected ManagedError, got %T: %v", err, err)
	}

	var managedBy string
	if err := pool.QueryRow(ctx, `SELECT managed_by FROM personas WHERE id = $1`, p.ID).Scan(&managedBy); err != nil {
		t.Fatalf("row must survive the refused mutations: %v", err)
	}
	if managedBy != "gitops" {
		t.Errorf("row changed: managed_by=%q", managedBy)
	}
	if got, err := svc.Get(ctx, p.ID); err != nil || got.Text != "external text" {
		t.Errorf("persona text changed despite the refused mutations: %+v (%v)", got, err)
	}
}

// TestOwnedMutationsRefuseManagedPersona: the agent-owned mutation paths
// (EnsureOwned's update branch and DeleteOwned) obey the same managed rule as
// the library paths — an externally managed owned persona is refused with a
// ManagedError naming the manager and survives untouched. This is the
// personas counterpart of the "every mutation → 409" contract in
// docs/api-reference.md.
func TestOwnedMutationsRefuseManagedPersona(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	kc := fake.NewClientBuilder().WithScheme(scheme).Build()
	svc := personas.NewService(personas.NewStore(pool), personas.NewReconciler(kc, "ainsel-test"), &stubAgentLister{})
	ctx := context.Background()

	text := "external text"
	owned, err := svc.EnsureOwned(ctx, "a-1", "Agent One (own)", personas.UpdateRequest{Text: &text})
	if err != nil {
		t.Fatalf("EnsureOwned (create): %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE personas SET managed_by = 'gitops' WHERE id = $1`, owned.ID); err != nil {
		t.Fatalf("seed managed_by: %v", err)
	}

	var mErr *personas.ManagedError
	if _, err := svc.EnsureOwned(ctx, "a-1", "Agent One (own)", personas.UpdateRequest{Text: ptr("mine")}); !errors.As(err, &mErr) {
		t.Errorf("EnsureOwned (update): expected ManagedError, got %T: %v", err, err)
	} else if mErr.Manager != "gitops" {
		t.Errorf("EnsureOwned refusal names %q, want gitops", mErr.Manager)
	}
	if err := svc.DeleteOwned(ctx, owned.ID, "a-1"); !errors.As(err, &mErr) {
		t.Errorf("DeleteOwned: expected ManagedError, got %T: %v", err, err)
	}

	if got, err := svc.Get(ctx, owned.ID); err != nil || got.Text != "external text" {
		t.Errorf("owned persona changed despite the refused mutations: %+v (%v)", got, err)
	}
}

// TestIsManagedValueAgnostic: any non-empty, non-"user" value is external.
func TestIsManagedValueAgnostic(t *testing.T) {
	for _, tc := range []struct {
		managedBy string
		want      bool
	}{
		{"", false},
		{"user", false},
		{"gitops", true},
		{"helm", true},
	} {
		if got := personas.IsManaged(tc.managedBy); got != tc.want {
			t.Errorf("IsManaged(%q) = %v, want %v", tc.managedBy, got, tc.want)
		}
	}
}
