package authz

import "context"

// CheckerStore is the subset of Store functionality required by Checker.
// Accepted as an interface so tests can substitute in-memory fakes.
type CheckerStore interface {
	GetUser(ctx context.Context, id string) (*User, error)
	GetResourceGroup(ctx context.Context, resourceType, resourceName string) (*ResourceGroup, error)
}

// Checker provides authorization checks against the store.
type Checker struct {
	store CheckerStore
	cache *GroupCache
}

// NewChecker creates a Checker with the given store and group cache.
func NewChecker(store CheckerStore, cache *GroupCache) *Checker {
	return &Checker{store: store, cache: cache}
}

// IsAdmin checks if the user has the admin flag.
// If a principal is present with matching Sub, returns false without store lookup.
// Otherwise, looks up the user in the store.
func (c *Checker) IsAdmin(ctx context.Context, userID string) (bool, error) {
	return c.isAdmin(ctx, userID)
}

// isAdmin is the internal helper that checks admin status.
func (c *Checker) isAdmin(ctx context.Context, userID string) (bool, error) {
	// Check if there's a principal in the context with matching Sub
	if principal, ok := PrincipalFrom(ctx); ok && principal.Sub == userID {
		// Principals are never admins
		return false, nil
	}
	// Fall back to store lookup
	u, err := c.store.GetUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return u.IsAdmin, nil
}

// CanRead returns true if the user can read the resource.
// Allowed: admin, public resource, member of the resource's group (any role).
func (c *Checker) CanRead(ctx context.Context, userID, resourceType, resourceName string) (bool, error) {
	admin, err := c.isAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}

	rg, err := c.store.GetResourceGroup(ctx, resourceType, resourceName)
	if err != nil {
		if err == ErrNotFound {
			return false, nil // No group mapping = default-closed
		}
		return false, err
	}
	if rg.Public {
		return true, nil
	}

	role, err := c.role(ctx, userID, rg.GroupID)
	if err != nil {
		return false, err
	}
	return roleCovers(role, RoleReader), nil
}

// role is the internal helper that gets the user's role in a group.
// If a principal is present with matching Sub, returns the role from the principal.
// Otherwise, looks up the role in the cache.
func (c *Checker) role(ctx context.Context, userID, groupID string) (GroupRole, error) {
	// Check if there's a principal in the context with matching Sub
	if principal, ok := PrincipalFrom(ctx); ok && principal.Sub == userID {
		// Return the role from the principal, or empty string if not present
		return principal.Groups[groupID], nil
	}
	// Fall back to cache lookup
	return c.cache.UserRole(ctx, userID, groupID)
}

// CanWrite returns true if the user can modify the resource.
// Allowed: admin, writer or owner in the resource's group.
func (c *Checker) CanWrite(ctx context.Context, userID, resourceType, resourceName string) (bool, error) {
	admin, err := c.isAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}

	rg, err := c.store.GetResourceGroup(ctx, resourceType, resourceName)
	if err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}

	role, err := c.role(ctx, userID, rg.GroupID)
	if err != nil {
		return false, err
	}
	return roleCovers(role, RoleWriter), nil
}

// CanManage returns true if the user can manage the resource (toggle public, etc).
// Allowed: admin, owner in the resource's group.
func (c *Checker) CanManage(ctx context.Context, userID, resourceType, resourceName string) (bool, error) {
	admin, err := c.isAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}

	rg, err := c.store.GetResourceGroup(ctx, resourceType, resourceName)
	if err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}

	role, err := c.role(ctx, userID, rg.GroupID)
	if err != nil {
		return false, err
	}
	return roleCovers(role, RoleOwner), nil
}

// CanManageGroup returns true if the user is an owner of the given group or an admin.
func (c *Checker) CanManageGroup(ctx context.Context, userID, groupID string) (bool, error) {
	admin, err := c.isAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}

	role, err := c.role(ctx, userID, groupID)
	if err != nil {
		return false, err
	}
	return role == RoleOwner, nil
}

// CanWriteGroup returns true if the user is a writer or owner of the given
// group, or an admin. Used to gate resource creation into a group.
func (c *Checker) CanWriteGroup(ctx context.Context, userID, groupID string) (bool, error) {
	admin, err := c.isAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}

	role, err := c.role(ctx, userID, groupID)
	if err != nil {
		return false, err
	}
	return roleCovers(role, RoleWriter), nil
}
