package authz

import (
	"context"
	"slices"
)

// Principal represents a non-user caller (agent or plugin) with their identity and
// group permissions.
type Principal struct {
	Sub    string               // e.g. "agent:reviewer", "plugin:mem0"
	Kind   string               // "agent" | "plugin"
	Groups map[string]GroupRole // groupID -> role
}

// principalKey is the context key type for storing a Principal.
type principalKey struct{}

// WithPrincipal returns a new context with the Principal attached.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the Principal from the context, if present.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

// PrincipalGroupIDs returns the principal's group IDs (sorted) when ctx holds a
// principal whose Sub == userID; ok=false otherwise.
func PrincipalGroupIDs(ctx context.Context, userID string) ([]string, bool) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Sub != userID {
		return nil, false
	}
	ids := make([]string, 0, len(p.Groups))
	for id := range p.Groups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, true
}
