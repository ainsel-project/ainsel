package authz

import (
	"context"
	"errors"
)

// ErrNoRegistry reports an operation that needs a user or group registry the
// hub does not have. It surfaces from PermissiveStore on the write surface:
// auth mode "none" has no user table to write to, so the honest answer is an
// error rather than a silently discarded write.
var ErrNoRegistry = errors.New("authz: no user registry (auth mode none)")

// PermissiveStore is the authorization store for auth mode "none".
//
// Mode "none" used to leave the API's store and checker nil, and every guard
// then read that nil as "allow". That made the unauthenticated path the only
// path the guard code never exercised, and it put a "is authorization wired"
// branch in front of every check.
//
// Instead, mode "none" wires this store and a real Checker over it. Every
// subject is admin, so the Checker answers allow through the same code path the
// configured modes use — there is no permissive special case inside Checker, and
// no nil for a handler to branch on.
//
// The read surface answers as an empty registry would. The write surface
// refuses with ErrNoRegistry: there is no user table behind mode "none", and a
// user-management endpoint that answered 200 while dropping the write would be
// worse than one that says it cannot.
//
// It satisfies CheckerStore (GetUser, GetResourceGroup) and the wider store
// surface the API depends on.
type PermissiveStore struct{}

// NewPermissiveStore builds the store for auth mode "none".
func NewPermissiveStore() *PermissiveStore { return &PermissiveStore{} }

// GetUser synthesises the admin identity for any subject. This is the single
// place permissiveness lives: Checker.isAdmin reads it, and every other
// Checker method short-circuits on admin before consulting anything else.
func (s *PermissiveStore) GetUser(_ context.Context, id string) (*User, error) {
	return &User{ID: id, IsAdmin: true}, nil
}

// UpsertUser is a no-op that reports success. Identity persistence calls it,
// and a failure there is only ever logged — answering with an error would turn
// a harmless no-op into log noise on every request.
func (s *PermissiveStore) UpsertUser(_ context.Context, sub, _, _ string) (*User, error) {
	return &User{ID: sub, IsAdmin: true}, nil
}

// ListUsers reports no accounts: mode "none" has no user table.
func (s *PermissiveStore) ListUsers(context.Context) ([]User, error) { return []User{}, nil }

// SetAdmin refuses: there is no account to grant or revoke admin on.
func (s *PermissiveStore) SetAdmin(context.Context, string, bool) error { return ErrNoRegistry }

// SetDisabled refuses: there is no account to disable.
func (s *PermissiveStore) SetDisabled(context.Context, string, bool) error { return ErrNoRegistry }

// ClearUsername refuses: there is no account to rename.
func (s *PermissiveStore) ClearUsername(context.Context, string) error { return ErrNoRegistry }

// CreateLocalUser refuses: mode "none" has no password store.
func (s *PermissiveStore) CreateLocalUser(_ context.Context, _, _, _, _ string, _, _ bool) (*User, error) {
	return nil, ErrNoRegistry
}

// UserPasswordHash reports no hash: no account can authenticate by password.
func (s *PermissiveStore) UserPasswordHash(context.Context, string) (string, error) {
	return "", ErrNoRegistry
}

// UserTokenVersion reports the baseline version. No token exists to revoke.
func (s *PermissiveStore) UserTokenVersion(context.Context, string) (int64, error) {
	return 0, nil
}

// BumpTokenVersion refuses: there is no row to bump.
func (s *PermissiveStore) BumpTokenVersion(context.Context, string) error { return ErrNoRegistry }

// SetPasswordWithFlag refuses: there is no row to set a password on.
func (s *PermissiveStore) SetPasswordWithFlag(context.Context, string, string, bool) error {
	return ErrNoRegistry
}

// DeleteUser refuses: there is no row to delete.
func (s *PermissiveStore) DeleteUser(context.Context, string) error { return ErrNoRegistry }

// IsUserDisabled reports enabled: nothing can be disabled without a registry.
func (s *PermissiveStore) IsUserDisabled(context.Context, string) (bool, error) {
	return false, nil
}

// UserGroupIDs reports no groups. The Checker never asks — it short-circuits on
// admin — but the API's group listing does, and "no groups" is the truth.
func (s *PermissiveStore) UserGroupIDs(context.Context, string) ([]string, error) {
	return nil, nil
}

// UserGroupRoles reports no roles.
func (s *PermissiveStore) UserGroupRoles(context.Context, string) (map[string]GroupRole, error) {
	return map[string]GroupRole{}, nil
}

// CreateGroup refuses: mode "none" has no group table.
func (s *PermissiveStore) CreateGroup(_ context.Context, _, _ string) (*Group, error) {
	return nil, ErrNoRegistry
}

// GetGroup reports not found.
func (s *PermissiveStore) GetGroup(context.Context, string) (*Group, error) { return nil, nil }

// ListGroups reports no groups.
func (s *PermissiveStore) ListGroups(context.Context) ([]Group, error) { return []Group{}, nil }

// UpdateGroup refuses: there is no row to update.
func (s *PermissiveStore) UpdateGroup(_ context.Context, _, _, _ string) (*Group, error) {
	return nil, ErrNoRegistry
}

// DeleteGroup refuses: there is no row to delete.
func (s *PermissiveStore) DeleteGroup(context.Context, string) error { return ErrNoRegistry }

// AddGroupMember refuses: there is no group to add to.
func (s *PermissiveStore) AddGroupMember(context.Context, string, string, GroupRole) error {
	return ErrNoRegistry
}

// RemoveGroupMember refuses: there is no group to remove from.
func (s *PermissiveStore) RemoveGroupMember(context.Context, string, string) error {
	return ErrNoRegistry
}

// ListGroupMembers reports no members.
func (s *PermissiveStore) ListGroupMembers(context.Context, string) ([]MemberWithUser, error) {
	return []MemberWithUser{}, nil
}

// SetResourceGroup is a no-op. The plugin registry syncer calls it while
// installing plugin-owned resources; in mode "none" those resources are
// reachable by every caller anyway, so recording nothing records the truth.
func (s *PermissiveStore) SetResourceGroup(context.Context, string, string, string, bool) error {
	return nil
}

// GetResourceGroup reports no ownership row, which the Checker reads as
// "not accessible" — a branch no permissive caller reaches, because
// isAdmin short-circuits first.
func (s *PermissiveStore) GetResourceGroup(context.Context, string, string) (*ResourceGroup, error) {
	return nil, nil
}

// DeleteResourceGroup is a no-op: there is no ownership row to remove.
func (s *PermissiveStore) DeleteResourceGroup(context.Context, string, string) error { return nil }

// SetResourcePublic is a no-op; see SetResourceGroup.
func (s *PermissiveStore) SetResourcePublic(context.Context, string, string, bool) error {
	return nil
}

// ListResourcesByGroups reports nothing. Only a non-admin caller's listing
// consults it, and in permissive mode there is none.
func (s *PermissiveStore) ListResourcesByGroups(context.Context, string, []string, bool) ([]string, error) {
	return nil, nil
}

// ListResourcesByGroupsPage reports nothing, for the same reason.
func (s *PermissiveStore) ListResourcesByGroupsPage(context.Context, string, []string, bool, int, int) ([]string, int, error) {
	return nil, 0, nil
}

// ListResourceGroupsByType reports no ownership rows.
func (s *PermissiveStore) ListResourceGroupsByType(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

// Compile-time proof that the permissive store covers both surfaces the hub
// needs: CheckerStore (what a Checker reads) and the store interface the API
// server is wired with. The API-side assertion lives in package api, which owns
// that interface.
var _ CheckerStore = (*PermissiveStore)(nil)
