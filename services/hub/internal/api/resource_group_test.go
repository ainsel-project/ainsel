package api

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
)

// failingGroupStore records nothing and fails every SetResourceGroup call,
// standing in for a store whose write is rejected.
type failingGroupStore struct {
	*fakeAuthzStore
	calls int
	err   error
}

func (f *failingGroupStore) SetResourceGroup(_ context.Context, _, _, _ string, _ bool) error {
	f.calls++
	return f.err
}

// TestAssignGroup pins the three behaviors of the shared create-time helper:
// a nil store writes nothing, an empty request falls back to the default
// group, and a failed write is swallowed rather than failing the create.
func TestAssignGroup(t *testing.T) {
	t.Run("nil store writes nothing", func(t *testing.T) {
		// No store means authz is not wired; the call must be a no-op
		// rather than a nil-pointer dereference.
		assignGroup(context.Background(), nil, authz.ResourceAgent, "a1", "g1")
	})

	t.Run("empty request falls back to the default group", func(t *testing.T) {
		store := newFakeAuthzStore()
		assignGroup(context.Background(), store, authz.ResourceAgent, "a1", "")
		if got := store.resources["agent:a1"]; got != authz.DefaultGroupID {
			t.Errorf("group = %q, want %q", got, authz.DefaultGroupID)
		}
	})

	t.Run("requested group is used as given", func(t *testing.T) {
		store := newFakeAuthzStore()
		assignGroup(context.Background(), store, authz.ResourceChannel, "c1", "g7")
		if got := store.resources["channel:c1"]; got != "g7" {
			t.Errorf("group = %q, want %q", got, "g7")
		}
	})

	t.Run("store error is swallowed", func(t *testing.T) {
		store := &failingGroupStore{fakeAuthzStore: newFakeAuthzStore(), err: errors.New("boom")}
		// The resource is already created, so a failed group write must not
		// surface to the caller.
		assignGroup(context.Background(), store, authz.ResourceAgent, "a1", "g1")
		if store.calls != 1 {
			t.Fatalf("SetResourceGroup calls = %d, want 1", store.calls)
		}
	})

	t.Run("server method uses the server store", func(t *testing.T) {
		store := newFakeAuthzStore()
		s := &Server{authzStore: store}
		s.assignGroup(context.Background(), authz.ResourceAgent, "a1", "")
		if got := store.resources["agent:a1"]; got != authz.DefaultGroupID {
			t.Errorf("group = %q, want %q", got, authz.DefaultGroupID)
		}
	})
}
