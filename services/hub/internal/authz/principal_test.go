package authz

import (
	"context"
	"testing"
	"time"
)

// fakeStore is a test implementation of CheckerStore.
type fakeStore struct {
	resourceGroups map[string]map[string]*ResourceGroup // resourceType -> resourceName -> ResourceGroup
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		resourceGroups: make(map[string]map[string]*ResourceGroup),
	}
}

func (f *fakeStore) GetUser(ctx context.Context, id string) (*User, error) {
	// Always return admin:true for testing
	return &User{ID: id, IsAdmin: true}, nil
}

func (f *fakeStore) GetResourceGroup(ctx context.Context, resourceType, resourceName string) (*ResourceGroup, error) {
	if rt, ok := f.resourceGroups[resourceType]; ok {
		if rg, ok := rt[resourceName]; ok {
			return rg, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeStore) setupResource(resourceType, resourceName, groupID string, public bool) {
	if _, ok := f.resourceGroups[resourceType]; !ok {
		f.resourceGroups[resourceType] = make(map[string]*ResourceGroup)
	}
	f.resourceGroups[resourceType][resourceName] = &ResourceGroup{
		GroupID: groupID,
		Public:  public,
	}
}

// newFakeCacheWithRoles creates a GroupCache with pre-configured roles.
func newFakeCacheWithRoles(userRoles map[string]map[string]GroupRole) *GroupCache {
	loader := func(userID string) (map[string]GroupRole, error) {
		if roles, ok := userRoles[userID]; ok {
			return roles, nil
		}
		return make(map[string]GroupRole), nil // No roles for user
	}
	return NewGroupCache(loader, time.Minute)
}

// TestChecker_PrincipalNeverAdmin tests that a principal is never considered admin.
func TestChecker_PrincipalNeverAdmin(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCacheWithRoles(nil)
	checker := NewChecker(store, cache)

	// Create a principal with empty groups
	principal := &Principal{
		Sub:    "plugin:mem0",
		Kind:   "plugin",
		Groups: make(map[string]GroupRole),
	}
	ctx := WithPrincipal(context.Background(), principal)

	// IsAdmin should return false because of the principal
	admin, err := checker.IsAdmin(ctx, "plugin:mem0")
	if err != nil {
		t.Fatalf("IsAdmin error: %v", err)
	}
	if admin {
		t.Error("principal should never be admin, got true")
	}

	// CanRead on a resource the principal doesn't have access to should be false
	store.setupResource("agent", "a1", "g1", false)
	canRead, err := checker.CanRead(ctx, "plugin:mem0", "agent", "a1")
	if err != nil {
		t.Fatalf("CanRead error: %v", err)
	}
	if canRead {
		t.Error("principal without group access should not be able to read, got true")
	}
}

// TestChecker_PrincipalGroupRoles tests that group roles are correctly applied.
func TestChecker_PrincipalGroupRoles(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCacheWithRoles(nil)
	checker := NewChecker(store, cache)

	// Setup resources
	store.setupResource("agent", "a1", "g1", false)
	store.setupResource("agent", "a2", "g2", false)
	store.setupResource("agent", "pub", "public", true) // public resource

	tests := []struct {
		name      string
		groups    map[string]GroupRole
		canRead   map[string]bool // resource -> expected canRead result
		canWrite  map[string]bool // resource -> expected canWrite result
	}{
		{
			name:   "reader on g1",
			groups: map[string]GroupRole{"g1": RoleReader},
			canRead: map[string]bool{
				"a1":  true,  // g1 reader can read
				"a2":  false, // g2 not accessible
				"pub": true,  // public always readable
			},
			canWrite: map[string]bool{
				"a1": false, // reader cannot write
			},
		},
		{
			name:   "writer on g1",
			groups: map[string]GroupRole{"g1": RoleWriter},
			canRead: map[string]bool{
				"a1": true,
			},
			canWrite: map[string]bool{
				"a1": true, // writer can write
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal := &Principal{
				Sub:    "plugin:mem0",
				Kind:   "plugin",
				Groups: tt.groups,
			}
			ctx := WithPrincipal(context.Background(), principal)

			for resource, expected := range tt.canRead {
				result, err := checker.CanRead(ctx, "plugin:mem0", "agent", resource)
				if err != nil {
					t.Errorf("CanRead(%s) error: %v", resource, err)
					continue
				}
				if result != expected {
					t.Errorf("CanRead(%s): got %v, want %v", resource, result, expected)
				}
			}

			for resource, expected := range tt.canWrite {
				result, err := checker.CanWrite(ctx, "plugin:mem0", "agent", resource)
				if err != nil {
					t.Errorf("CanWrite(%s) error: %v", resource, err)
					continue
				}
				if result != expected {
					t.Errorf("CanWrite(%s): got %v, want %v", resource, result, expected)
				}
			}
		})
	}
}

// TestChecker_PrincipalOnlyForMatchingSub tests that principal is only used for matching Sub.
func TestChecker_PrincipalOnlyForMatchingSub(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCacheWithRoles(nil)
	checker := NewChecker(store, cache)

	// Setup a principal for plugin:mem0
	principal := &Principal{
		Sub:    "plugin:mem0",
		Kind:   "plugin",
		Groups: make(map[string]GroupRole), // empty groups
	}
	ctx := WithPrincipal(context.Background(), principal)

	// When we call with a different userID, it should fall back to store (which returns admin:true)
	admin, err := checker.IsAdmin(ctx, "u1")
	if err != nil {
		t.Fatalf("IsAdmin error: %v", err)
	}
	if !admin {
		t.Error("should fall back to store when userID differs from principal.Sub, got false")
	}
}

// TestPrincipalGroupIDs tests that PrincipalGroupIDs returns sorted IDs correctly.
func TestPrincipalGroupIDs(t *testing.T) {
	tests := []struct {
		name         string
		setupCtx     func(context.Context) context.Context
		userID       string
		expectedOK   bool
		expectedIDs  []string
	}{
		{
			name: "with matching principal",
			setupCtx: func(ctx context.Context) context.Context {
				return WithPrincipal(ctx, &Principal{
					Sub:  "plugin:mem0",
					Kind: "plugin",
					Groups: map[string]GroupRole{
						"z-group": RoleReader,
						"a-group": RoleWriter,
						"m-group": RoleReader,
					},
				})
			},
			userID:      "plugin:mem0",
			expectedOK:  true,
			expectedIDs: []string{"a-group", "m-group", "z-group"}, // sorted
		},
		{
			name: "with non-matching userID",
			setupCtx: func(ctx context.Context) context.Context {
				return WithPrincipal(ctx, &Principal{
					Sub:    "plugin:mem0",
					Kind:   "plugin",
					Groups: map[string]GroupRole{"g1": RoleReader},
				})
			},
			userID:     "other:user",
			expectedOK: false,
		},
		{
			name: "without principal",
			setupCtx: func(ctx context.Context) context.Context {
				return ctx
			},
			userID:     "any:user",
			expectedOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupCtx(context.Background())
			ids, ok := PrincipalGroupIDs(ctx, tt.userID)
			if ok != tt.expectedOK {
				t.Errorf("PrincipalGroupIDs ok: got %v, want %v", ok, tt.expectedOK)
			}
			if ok {
				if len(ids) != len(tt.expectedIDs) {
					t.Errorf("PrincipalGroupIDs length: got %d, want %d", len(ids), len(tt.expectedIDs))
				}
				for i, id := range ids {
					if i >= len(tt.expectedIDs) || id != tt.expectedIDs[i] {
						t.Errorf("PrincipalGroupIDs[%d]: got %s, want %s", i, id, tt.expectedIDs[i])
					}
				}
			}
		})
	}
}
