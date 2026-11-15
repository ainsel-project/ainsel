package personas

import "testing"

// Docker-free half of the #190 regression guard: personas.managed_by is
// NOT NULL DEFAULT 'user', and Postgres does not apply that default to an
// explicit NULL, so an empty author must resolve to "user" before the INSERT.
// Internal callers that never stamp a persona (Service.EnsureOwned's
// copy-on-write create) depend on this; the DB-backed suite covers the same
// path end to end when Docker is available.
func TestResolveManagedBy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty resolves to console", "", ManagedByUser},
		{"console stays console", ManagedByUser, ManagedByUser},
		{"plugin stamp is kept", "plugin-forgejo", "plugin-forgejo"},
		{"any other value passes through", "plugin", "plugin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveManagedBy(c.in); got != c.want {
				t.Errorf("resolveManagedBy(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
