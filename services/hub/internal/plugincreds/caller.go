package plugincreds

import "context"

// Caller describes an authenticated plugin credential principal: the plugin
// it belongs to, the credential kind, and the effective permissions/groups
// resolved by the auth middleware.
//
// It lives here rather than in the API package because the managed-resource
// guards in the DB-backed registries (personas, MCP servers, skills) must be
// able to read it from a request context without importing the API package.
type Caller struct {
	PluginID     string
	Kind         string
	Sub          string
	Username     string
	Permissions  []string
	Groups       []GroupGrant
	InvocationID string
}

// IsPlugin reports whether the caller authenticated with a plugin-kind
// credential. Agent- and user-kind credentials also carry a PluginID (the
// plugin they were minted for), so identity-bearing is not the same as
// being a plugin — the plugin-only exceptions must use this.
func (c *Caller) IsPlugin() bool {
	return c != nil && c.Kind == KindPlugin
}

// ManagerPrefix is the prefix of the managedBy value that names a plugin as a
// resource's author: "plugin-<plugin id>". It is a distinct marker from the
// bare "plugin" value the plugin registry syncer writes for its derived rows
// (whose owner is encoded in the row *name*): only a stamped value carries the
// named-manager exception.
const ManagerPrefix = "plugin-"

// ManagerStamp is the managedBy value naming pluginID as a resource's author.
// It is always composed from an authenticated caller's identity, never from a
// request body — that is what makes the stamp un-spoofable.
func ManagerStamp(pluginID string) string {
	return ManagerPrefix + pluginID
}

// IsManagerOf reports whether this caller is the named manager of a resource
// whose managedBy value is managedBy: a plugin-kind credential whose own stamp
// the value is. That is the single rule behind every managed-row exception —
// everyone else keeps hitting the managed-resource 409. The bare legacy
// "plugin" marker never matches.
func (c *Caller) IsManagerOf(managedBy string) bool {
	return c.IsPlugin() && managedBy == ManagerStamp(c.PluginID)
}

// callerKey is the context key for a Caller.
type callerKey struct{}

// CallerContextKey is the context key a Caller is stored under. Tests that
// need to place a Caller on a context without going through the auth
// middleware use it with context.WithValue.
func CallerContextKey() any { return callerKey{} }

// WithCaller returns a context carrying the caller.
func WithCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom retrieves the Caller from the context, if any.
func CallerFrom(ctx context.Context) (*Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(*Caller)
	return c, ok
}
