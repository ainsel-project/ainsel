// Package authz implements the hub's authorization layer. Checker answers
// whether a principal (see Principal) may perform an action in a group,
// backed by a Postgres store and a TTL cache of group roles (GroupCache);
// guard.go adds HTTP middleware that enforces those checks and rejects
// disabled users.
package authz

import "time"

// DefaultGroupID is the group ungrouped resources are filed under: the
// fallback when a create request names no group, and the target of the
// startup group backfill. It is a real row created by the access-control
// migration (0016_group_access_control), whose id is this value — renaming
// or retiring it is a data migration, not just a code change.
const DefaultGroupID = "legacy"

// ResourceType is the authz type of a resource that can own a
// resource_groups row. The values are wire-visible: they are stored in
// resource_groups.resource_type and surfaced as the `type` field of the
// group-resources API, so they are part of the hub's contract.
type ResourceType string

const (
	ResourceAgent       ResourceType = "agent"
	ResourceAgentImage  ResourceType = "agent-image"
	ResourceConnector   ResourceType = "connector"
	ResourcePersona     ResourceType = "persona"
	ResourceSkill       ResourceType = "skill"
	ResourceChannel     ResourceType = "channel"
	ResourceMCPServer   ResourceType = "mcp-server"
	ResourceTrigger     ResourceType = "trigger"
	ResourceCronTrigger ResourceType = "cron-trigger"
)

// AllResourceTypes lists every groupable resource type. Order defines the
// order items are returned in by the group-resources listing; the frontend
// groups by type anyway.
//
// Note on channels: only standalone (custom) channels have resource-group
// rows. A connector's or an agent's channel is authorised through the
// referenced entity (see the api package's channelAuthzTarget), so it
// belongs to that connector's/agent's group and is never double-counted.
var AllResourceTypes = []ResourceType{
	ResourceAgent,
	ResourceAgentImage,
	ResourceConnector,
	ResourcePersona,
	ResourceSkill,
	ResourceChannel,
	ResourceMCPServer,
	ResourceTrigger,
	ResourceCronTrigger,
}

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
