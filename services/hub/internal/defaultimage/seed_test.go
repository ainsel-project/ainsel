package defaultimage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/defaultimage"
	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNS = "ainsel"

type groupCall struct {
	resourceType, name, group string
	public                    bool
}

// fakeGroups records every group assignment the seeder makes.
type fakeGroups struct {
	calls []groupCall
	err   error
}

func (f *fakeGroups) SetResourceGroup(_ context.Context, resourceType, name, group string, public bool) error {
	f.calls = append(f.calls, groupCall{resourceType, name, group, public})
	return f.err
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := ainselv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func getImage(t *testing.T, c client.Client) *ainselv1alpha1.AgentImage {
	t.Helper()
	img := &ainselv1alpha1.AgentImage{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: defaultimage.Name}, img); err != nil {
		t.Fatalf("get default image: %v", err)
	}
	return img
}

func TestEnsure_CreatesManagedPublicImage(t *testing.T) {
	c := newClient(t)
	groups := &fakeGroups{}

	if err := defaultimage.Ensure(context.Background(), c, groups, testNS, "dpinsel/ainsel-pi:0.2.0"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	img := getImage(t, c)
	if img.Spec.ImageURL != "dpinsel/ainsel-pi:0.2.0" {
		t.Errorf("imageURL = %q", img.Spec.ImageURL)
	}
	if img.Spec.DisplayName != defaultimage.DisplayName {
		t.Errorf("displayName = %q", img.Spec.DisplayName)
	}
	if img.Spec.ManagedBy != defaultimage.ManagedBy {
		t.Errorf("managedBy = %q, want %q", img.Spec.ManagedBy, defaultimage.ManagedBy)
	}
	if len(img.Spec.Tools) != 0 || len(img.Spec.MCPServers) != 0 || len(img.Spec.Env) != 0 {
		t.Errorf("the default image must not grant tools, MCP servers or env: %+v", img.Spec)
	}
	want := []groupCall{{"agent-image", defaultimage.Name, "legacy", true}}
	if len(groups.calls) != 1 || groups.calls[0] != want[0] {
		t.Errorf("group calls = %+v, want %+v", groups.calls, want)
	}
}

func TestEnsure_UpgradesOwnImage(t *testing.T) {
	c := newClient(t, &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: defaultimage.Name},
		Spec: ainselv1alpha1.AgentImageSpec{
			DisplayName: defaultimage.DisplayName,
			ImageURL:    "dpinsel/ainsel-pi:0.1.0",
			ManagedBy:   defaultimage.ManagedBy,
		},
	})
	groups := &fakeGroups{}

	if err := defaultimage.Ensure(context.Background(), c, groups, testNS, "dpinsel/ainsel-pi:0.2.0"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if got := getImage(t, c).Spec.ImageURL; got != "dpinsel/ainsel-pi:0.2.0" {
		t.Errorf("imageURL = %q, want the release's image", got)
	}
	if len(groups.calls) != 0 {
		t.Errorf("an upgrade must not reset the group an admin chose, got %+v", groups.calls)
	}
}

func TestEnsure_LeavesUserImageWithSameNameAlone(t *testing.T) {
	c := newClient(t, &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: defaultimage.Name},
		Spec: ainselv1alpha1.AgentImageSpec{
			DisplayName: "Mine",
			ImageURL:    "registry.example.com/mine:1",
		},
	})
	groups := &fakeGroups{}

	if err := defaultimage.Ensure(context.Background(), c, groups, testNS, "dpinsel/ainsel-pi:0.2.0"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	img := getImage(t, c)
	if img.Spec.ImageURL != "registry.example.com/mine:1" || img.Spec.DisplayName != "Mine" {
		t.Errorf("a user-owned image must not be overwritten: %+v", img.Spec)
	}
	if len(groups.calls) != 0 {
		t.Errorf("group calls = %+v, want none", groups.calls)
	}
}

// raceClient answers Get with NotFound and Create with AlreadyExists: the
// window in which another hub replica created the image first.
type raceClient struct{ client.Client }

func (raceClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "ainsel.dev", Resource: "agentimages"}, defaultimage.Name)
}

func (raceClient) Create(context.Context, client.Object, ...client.CreateOption) error {
	return apierrors.NewAlreadyExists(schema.GroupResource{Group: "ainsel.dev", Resource: "agentimages"}, defaultimage.Name)
}

func TestEnsure_LosingCreateRaceIsNotAnError(t *testing.T) {
	groups := &fakeGroups{}
	if err := defaultimage.Ensure(context.Background(), raceClient{newClient(t)}, groups, testNS, "dpinsel/ainsel-pi:0.2.0"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(groups.calls) != 0 {
		t.Errorf("the replica that lost the race must leave the group to the winner, got %+v", groups.calls)
	}
}

func TestEnsure_EmptyURLDisablesSeeding(t *testing.T) {
	c := newClient(t)
	if err := defaultimage.Ensure(context.Background(), c, &fakeGroups{}, testNS, ""); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	var list ainselv1alpha1.AgentImageList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected no image, got %d", len(list.Items))
	}
}

func TestEnsure_NilGroupsStillCreates(t *testing.T) {
	c := newClient(t)
	if err := defaultimage.Ensure(context.Background(), c, nil, testNS, "dpinsel/ainsel-pi:0.2.0"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	getImage(t, c)
}

func TestEnsure_GroupErrorIsReported(t *testing.T) {
	c := newClient(t)
	err := defaultimage.Ensure(context.Background(), c, &fakeGroups{err: errors.New("db down")}, testNS, "dpinsel/ainsel-pi:0.2.0")
	if err == nil {
		t.Fatal("expected the group error to surface")
	}
	getImage(t, c) // the image itself is still created
}
