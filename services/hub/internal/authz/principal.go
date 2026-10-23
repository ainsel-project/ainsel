package authz

import "context"

// Principal represents a non-user caller (agent or plugin) with their identity and
// group permissions.
type Principal struct {
	Sub    string               // e.g. "agent:reviewer", "plugin:mem0"
	Kind   string               // "agent" | "plugin"
	Groups map[string]GroupRole // groupID -> role
}

// principalContextKey is the context key for storing a Principal.
var principalContextKey struct{}

// WithPrincipal returns a new context with the Principal attached.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, p)
}

// PrincipalFrom returns the Principal from the context, if present.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalContextKey).(*Principal)
	return p, ok
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
	// Sort for deterministic output
	sortedIDs := make([]string, len(ids))
	copy(sortedIDs, ids)
	sliceSort(sortedIDs)
	return sortedIDs, true
}

// sliceSort sorts a slice of strings in-place (minimal implementation for this package).
func sliceSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
