// Package authz implements the hub's authorization layer. Checker answers
// whether a principal (see Principal) may perform an action in a group,
// backed by a Postgres store and a TTL cache of group roles (GroupCache);
// guard.go adds HTTP middleware that enforces those checks and rejects
// disabled users.
package authz

import "time"

// GroupRole is the permission level a user has within a group.
type GroupRole string

const (
	// RoleReader may see the group's resources but not change them.
	RoleReader GroupRole = "reader"
	// RoleWriter may change the group's resources; it covers RoleReader.
	RoleWriter GroupRole = "writer"
	// RoleOwner is the top role: it covers RoleWriter and RoleReader.
	RoleOwner GroupRole = "owner"
)

// DefaultGroupID is the single group of an install that runs with the group
// model switched off (auth mode "none", see PermissiveStore): the synthetic
// 'legacy' group migration 0016 creates. cmd/hub hands it to the API server as
// api.Deps.DefaultGroupID so a create that names no groupId still lands in a
// group instead of failing a "groupId is required" check there is no way to
// satisfy. In every other auth mode this is not used and a create without
// groupId is a 400.
const DefaultGroupID = "legacy"

// roleCovers returns true if `have` covers `need`.
// owner covers writer covers reader.
func roleCovers(have, need GroupRole) bool {
	rank := map[GroupRole]int{RoleReader: 1, RoleWriter: 2, RoleOwner: 3}
	return rank[have] >= rank[need]
}

// User is a hub-managed user record, synced lazily from OIDC JWT claims.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	// Disabled cuts the user off from login and all API access without
	// deleting the row: group memberships and user tokens survive.
	Disabled bool `json:"disabled"`
	// MustChangePassword is the forced-change flag (issue #113): the account's
	// current password was issued by someone else — bootstrap, admin create or
	// admin reset — and has to be replaced before the console is usable. Only
	// ever true for local-credential accounts; OIDC users have no hub-side
	// password to change.
	MustChangePassword bool      `json:"mustChangePassword"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Group is a named collection of users. Groups are flat — no nesting.
type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// MemberWithUser is a resolved group member for API responses.
type MemberWithUser struct {
	User User      `json:"user"`
	Role GroupRole `json:"role"`
}

// ResourceGroup maps a resource to its group and visibility.
type ResourceGroup struct {
	ResourceType string    `json:"resourceType"`
	ResourceName string    `json:"resourceName"`
	GroupID      string    `json:"groupId"`
	Public       bool      `json:"public"`
	CreatedAt    time.Time `json:"createdAt"`
}
