package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
)

// TestStoreLocalUserLifecycle covers the local-account methods added for
// issue #108: create with credentials, read back, password set/reset,
// duplicate protection, and deletion including owned resources.
func TestStoreLocalUserLifecycle(t *testing.T) {
	env := newTestAuthZ(t)
	defer env.cleanup()
	ctx := context.Background()

	u, err := env.store.CreateLocalUser(ctx, "local:alice", "alice@example.com", "alice", "$argon2id$fake", true, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if u.ID != "local:alice" || u.Username != "alice" || !u.IsAdmin {
		t.Fatalf("unexpected user: %+v", u)
	}

	// Duplicate is rejected.
	if _, err := env.store.CreateLocalUser(ctx, "local:alice", "", "alice", "x", false, false); !errors.Is(err, authz.ErrAlreadyExists) {
		t.Fatalf("duplicate: got %v, want ErrAlreadyExists", err)
	}

	// Password hash round-trip.
	hash, err := env.store.UserPasswordHash(ctx, "local:alice")
	if err != nil || hash != "$argon2id$fake" {
		t.Fatalf("UserPasswordHash = %q, %v", hash, err)
	}
	if err := env.store.SetPasswordWithFlag(ctx, "local:alice", "$argon2id$new", true); err != nil {
		t.Fatalf("SetPasswordWithFlag: %v", err)
	}
	hash, err = env.store.UserPasswordHash(ctx, "local:alice")
	if err != nil || hash != "$argon2id$new" {
		t.Fatalf("after SetPasswordWithFlag = %q, %v", hash, err)
	}

	// Missing user errors.
	if _, err := env.store.UserPasswordHash(ctx, "local:ghost"); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("UserPasswordHash(ghost): %v", err)
	}
	if err := env.store.SetPasswordWithFlag(ctx, "local:ghost", "x", false); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("SetPasswordWithFlag(ghost): %v", err)
	}
	if err := env.store.DeleteUser(ctx, "local:ghost"); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("DeleteUser(ghost): %v", err)
	}

	// Deletion works and cascades the registry row.
	if err := env.store.DeleteUser(ctx, "local:alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := env.store.GetUser(ctx, "local:alice"); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

// TestStoreUpsertKeepsLocalPassword guards a critical interaction: identity
// persistence calls UpsertUser on every authenticated request. For a local
// user that must never clear or overwrite the stored password.
func TestStoreUpsertKeepsLocalPassword(t *testing.T) {
	env := newTestAuthZ(t)
	defer env.cleanup()
	ctx := context.Background()

	if _, err := env.store.CreateLocalUser(ctx, "local:bob", "", "bob", "$argon2id$orig", false, false); err != nil {
		t.Fatal(err)
	}
	// Simulate identity-persist upserts with partial data.
	if _, err := env.store.UpsertUser(ctx, "local:bob", "bob@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.UpsertUser(ctx, "local:bob", "", "bob"); err != nil {
		t.Fatal(err)
	}
	hash, err := env.store.UserPasswordHash(ctx, "local:bob")
	if err != nil || hash != "$argon2id$orig" {
		t.Fatalf("password lost by upsert: %q, %v", hash, err)
	}
	u, err := env.store.GetUser(ctx, "local:bob")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "bob@example.com" || u.Username != "bob" {
		t.Fatalf("upsert fields wrong: %+v", u)
	}
}

// TestStoreMustChangePassword covers the forced-change flag (issue #113): it
// round-trips through create, is stated by every password write, and survives
// the identity-persist upserts that run on every authenticated request.
func TestStoreMustChangePassword(t *testing.T) {
	env := newTestAuthZ(t)
	defer env.cleanup()
	ctx := context.Background()

	// Created flagged (bootstrap admin / admin-created user).
	if _, err := env.store.CreateLocalUser(ctx, "local:carol", "", "carol", "$argon2id$boot", true, true); err != nil {
		t.Fatal(err)
	}
	u, err := env.store.GetUser(ctx, "local:carol")
	if err != nil || !u.MustChangePassword {
		t.Fatalf("created flagged: %+v, %v", u, err)
	}
	// ListUsers reports the same flag as GetUser.
	users, err := env.store.ListUsers(ctx)
	if err != nil || len(users) != 1 || !users[0].MustChangePassword {
		t.Fatalf("ListUsers flag = %+v, %v", users, err)
	}

	// Identity persistence must not clear a pending change.
	if _, err := env.store.UpsertUser(ctx, "local:carol", "carol@example.com", "carol"); err != nil {
		t.Fatal(err)
	}
	if u, err = env.store.GetUser(ctx, "local:carol"); err != nil || !u.MustChangePassword {
		t.Fatalf("upsert cleared the flag: %+v, %v", u, err)
	}

	// Admin reset re-arms it; the owner's own change clears it.
	if err := env.store.SetPasswordWithFlag(ctx, "local:carol", "$argon2id$reset", true); err != nil {
		t.Fatal(err)
	}
	if u, err = env.store.GetUser(ctx, "local:carol"); err != nil || !u.MustChangePassword {
		t.Fatalf("reset must arm the flag: %+v, %v", u, err)
	}
	if err := env.store.SetPasswordWithFlag(ctx, "local:carol", "$argon2id$chosen", false); err != nil {
		t.Fatal(err)
	}
	if u, err = env.store.GetUser(ctx, "local:carol"); err != nil || u.MustChangePassword {
		t.Fatalf("self-change must clear the flag: %+v, %v", u, err)
	}
}

// Note: DeleteUser relies on ON DELETE CASCADE for the remaining FKs to
// users (group_members, user_tokens); the legacy resource_ownership table
// was dropped by migration 0016.

// TestStoreTokenVersion covers the token_version column (issue #112):
// SetPassword bumps it atomically with the hash write, BumpTokenVersion
// increments it on its own, and both report missing users.
func TestStoreTokenVersion(t *testing.T) {
	env := newTestAuthZ(t)
	defer env.cleanup()
	ctx := context.Background()

	if _, err := env.store.CreateLocalUser(ctx, "local:carol", "", "carol", "$argon2id$first", false, false); err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	v, err := env.store.UserTokenVersion(ctx, "local:carol")
	if err != nil || v != 0 {
		t.Fatalf("initial UserTokenVersion = %d, %v; want 0", v, err)
	}

	// SetPasswordWithFlag bumps exactly once per call, atomically with the hash.
	if err := env.store.SetPasswordWithFlag(ctx, "local:carol", "$argon2id$second", false); err != nil {
		t.Fatalf("SetPasswordWithFlag: %v", err)
	}
	v, err = env.store.UserTokenVersion(ctx, "local:carol")
	if err != nil || v != 1 {
		t.Fatalf("after SetPasswordWithFlag UserTokenVersion = %d, %v; want 1", v, err)
	}
	if h, _ := env.store.UserPasswordHash(ctx, "local:carol"); h != "$argon2id$second" {
		t.Fatalf("hash not written: %q", h)
	}

	// Standalone bump (sign-out-everywhere).
	if err := env.store.BumpTokenVersion(ctx, "local:carol"); err != nil {
		t.Fatalf("BumpTokenVersion: %v", err)
	}
	if v, err = env.store.UserTokenVersion(ctx, "local:carol"); err != nil || v != 2 {
		t.Fatalf("after BumpTokenVersion UserTokenVersion = %d, %v; want 2", v, err)
	}

	// Missing user: both readers/writers report ErrNotFound.
	if _, err := env.store.UserTokenVersion(ctx, "local:ghost"); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("UserTokenVersion(ghost): %v", err)
	}
	if err := env.store.BumpTokenVersion(ctx, "local:ghost"); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("BumpTokenVersion(ghost): %v", err)
	}
}
