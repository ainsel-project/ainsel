package authz

import (
	"context"
	"errors"
	"net/http"

	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// DisabledUserChecker is the store subset the disabled guard needs.
// *Store implements it; tests can substitute a fake.
type DisabledUserChecker interface {
	IsUserDisabled(ctx context.Context, id string) (bool, error)
}

// NewDisabledGuard returns middleware that rejects requests whose
// authenticated user is disabled in the registry with 401, even when the
// bearer token itself is still cryptographically valid (local session JWTs
// and OIDC access tokens are verified statelessly).
//
// It must sit inside all token middlewares (user-token, local JWT, OIDC) so
// every authentication attempt has populated the request context before the
// check runs, and outside the handlers so every authenticated route —
// including /auth/me and self-service endpoints — is covered by one guard.
// User tokens (ainsel_…) are covered separately: usertokens.Store.Validate
// rejects tokens of disabled users at the DB level.
//
// Requests with no user in the context pass through: rejecting anonymous
// requests is the job of the surrounding closers (RequireAuth, oidcMW).
// Users with no registry row yet pass through: a first-seen OIDC identity is
// legitimately not persisted until IdentityPersistMiddleware runs, which sits
// inside this guard in both wired chains.
func NewDisabledGuard(store DisabledUserChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := oidc.FromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			disabled, err := store.IsUserDisabled(r.Context(), u.Sub)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if disabled {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
