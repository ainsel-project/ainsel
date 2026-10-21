package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// SetLocalAuthSecret enables local username/password authentication with the
// given JWT signing secret. Must be called before the server starts serving.
func (s *Server) SetLocalAuthSecret(secret []byte) {
	s.localAuthSecret = secret
	s.loginThrottle = newLoginThrottle()
}

// LocalAuthEnabled reports whether local login is wired.
func (s *Server) LocalAuthEnabled() bool {
	return len(s.localAuthSecret) > 0
}

// SetPasswordPolicy installs the password policy enforced on all
// password-setting paths. Must be called before the server starts serving,
// following the SetLocalAuthSecret pattern.
func (s *Server) SetPasswordPolicy(p PasswordPolicy) {
	s.passwordPolicy = &p
}

// passwordPolicyOrDefault returns the effective policy. The zero-value Server
// (and test servers built without wiring) falls back to the default, which
// reproduces the pre-config behavior exactly.
func (s *Server) passwordPolicyOrDefault() PasswordPolicy {
	if s.passwordPolicy == nil {
		return DefaultPasswordPolicy()
	}
	return *s.passwordPolicy
}

// validatePassword checks a candidate password against the server's policy.
// All three password-setting paths (create, admin reset, own change) must use
// this so the configured policy cannot be bypassed.
func (s *Server) validatePassword(p string) error {
	return s.passwordPolicyOrDefault().Validate(p)
}

// handlePasswordPolicy exposes the active password policy so the frontend can
// render matching hints on all password forms. Any authenticated caller may
// read it: the change-password form is available to non-admins, and the rules
// are enforced visibly at set-time anyway, so the policy is not a secret.
// Mounted inside the auth middleware (see serveHTTPInner).
func (s *Server) handlePasswordPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := s.passwordPolicyOrDefault()
	classes := p.RequireClasses
	if classes == nil {
		classes = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"minLength":               p.MinLength,
		"maxLength":               MaxPasswordLen,
		"requireCharacterClasses": classes,
	})
}

// usernameRe restricts local usernames to a safe, lowercase, URL-friendly
// character set so they can never collide with OIDC subs or break paths.
var usernameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

func validateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return errors.New("username must be 1-64 chars of lowercase a-z, 0-9, '.', '_', '-' (no leading/trailing punctuation)")
	}
	return nil
}

// handleLogin authenticates a local user and issues a session JWT. Mounted
// OUTSIDE the auth middleware (see serveHTTPInner) but behind rate limiting.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.LocalAuthEnabled() {
		writeError(w, http.StatusForbidden, "local login is not enabled on this hub")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	body.Username = strings.ToLower(strings.TrimSpace(body.Username))

	// Per-username backoff on top of the global per-IP rate limiter.
	if locked := s.loginThrottle.denied(body.Username); locked > 0 {
		w.Header().Set("Retry-After", formatRetryAfter(locked))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}

	// Generic 401 for every failure mode: unknown user, no local password,
	// wrong password. Never reveal which one it was.
	fail := func() {
		s.loginThrottle.recordFailure(body.Username)
		slog.Warn("auth_login_failed", "log_type", "auth_event", "username", body.Username)
		// The attempted username is the brute-force signal, so it is recorded
		// even when the user does not exist: actor/target carry the derived
		// local id, matching what a successful login for that name would write.
		id := authz.LocalUserIDPrefix + body.Username
		s.recordAuthEvent(r, auditevents.TypeLoginFailed, id, id, auditevents.OutcomeFailure)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	}

	if body.Username == "" || body.Password == "" || validateUsername(body.Username) != nil {
		fail()
		return
	}

	id := authz.LocalUserIDPrefix + body.Username
	hash, err := s.authzStore.UserPasswordHash(r.Context(), id)
	if err != nil {
		fail()
		return
	}
	if err := localauth.VerifyPassword(body.Password, hash); err != nil {
		fail()
		return
	}

	u, err := s.authzStore.GetUser(r.Context(), id)
	if err != nil {
		fail()
		return
	}
	if u.Disabled {
		// Same generic 401 as a wrong password — never reveal that the
		// account exists but is disabled. Logged separately for audit.
		slog.Info("auth_login_disabled_user", "log_type", "auth_event", "username", body.Username)
		fail()
		return
	}

	// Embed the current token version so a later password change/reset or
	// sign-out-everywhere invalidates this token (issue #112). One extra PK
	// read on the rare, already rate-limited login path.
	tokenVersion, err := s.authzStore.UserTokenVersion(r.Context(), u.ID)
	if err != nil {
		slog.Error("auth_login: token version lookup failed", "username", body.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	token, exp, err := localauth.IssueToken(s.localAuthSecret, u.ID, u.Username, u.IsAdmin, tokenVersion)
	if err != nil {
		slog.Error("auth_login: token issue failed", "username", body.Username, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.loginThrottle.recordSuccess(body.Username)
	slog.Info("auth_login", "log_type", "auth_event", "username", body.Username, "isAdmin", u.IsAdmin)
	s.recordAuthEvent(r, auditevents.TypeLogin, u.ID, u.ID, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": exp.UTC().Format(time.RFC3339),
		"user": map[string]any{
			"sub":      u.ID,
			"username": u.Username,
			"isAdmin":  u.IsAdmin,
			// The login response carries the flag so the frontend can route to
			// the forced change screen without a second round-trip to /auth/me.
			"mustChangePassword": u.MustChangePassword,
		},
	})
}

// CodePasswordChangeRequired is the machine-readable code every 403 from the
// forced-change gate carries. The frontend branches on this code; the message
// text is not a contract.
const CodePasswordChangeRequired = "password_change_required"

// writePasswordChangeRequired answers with 403 — not 401: a 401 would hand the
// request to the shared client's session-recovery path, which logs the user out
// instead of taking them to the change screen.
func writePasswordChangeRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "forbidden",
		"code":    CodePasswordChangeRequired,
		"message": "password change required",
	})
}

// isPasswordChangeAllowed lists what a flagged local user may still reach:
// their own identity, the password change itself, and logout. Exact path and
// method, not prefixes — a prefix would open sibling routes under /auth/.
func isPasswordChangeAllowed(path, method string) bool {
	switch {
	case path == "/api/v1/auth/me" && method == http.MethodGet:
		return true
	case path == "/api/v1/auth/password" && method == http.MethodPut:
		return true
	case path == "/api/v1/auth/logout" && method == http.MethodPost:
		return true
	}
	return false
}

// passwordChangeGate enforces the forced password change (issue #113) for local
// accounts whose password was issued by someone else: the bootstrapped admin,
// an admin-created user, or a user whose password an admin reset.
//
// The decision is read from the database on every request rather than taken
// from a JWT claim. The flag is cleared by the change itself, so a claim would
// keep 403-ing a user who has already changed their password until they logged
// in again; a DB read is correct the moment the change lands. It costs one
// indexed primary-key read per local-mode request, which the authz checker
// already pays for its own decisions.
//
// Anything that is not a local account — OIDC subjects, and requests in
// `none` mode, where this middleware is never reached — short-circuits, so
// those modes are unaffected.
func (s *Server) passwordChangeGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authzStore == nil || isPasswordChangeAllowed(r.URL.Path, r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		u, ok := oidc.FromContext(r.Context())
		if !ok || !strings.HasPrefix(u.Sub, authz.LocalUserIDPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		stored, err := s.authzStore.GetUser(r.Context(), u.Sub)
		if errors.Is(err, authz.ErrNotFound) {
			// Deleted from the registry: there is no flag to enforce, and the
			// handlers answered such a request on their own before this gate
			// existed. Keep that behaviour instead of claiming a change is due.
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !stored.MustChangePassword {
			next.ServeHTTP(w, r)
			return
		}
		slog.Info("auth_password_change_required", "log_type", "auth_event", "sub", u.Sub, "path", r.URL.Path)
		writePasswordChangeRequired(w)
	})
}

// authMeResponse is the GET /api/v1/auth/me wire shape. It keeps the exact
// fields the shared oidc.MeHandler emitted (sub/email/username/roles) and adds
// the forced-change flag, so the swap to the hub-side handler is additive for
// every existing consumer.
type authMeResponse struct {
	Sub                string   `json:"sub"`
	Email              string   `json:"email"`
	Username           string   `json:"username"`
	Roles              []string `json:"roles"`
	MustChangePassword bool     `json:"mustChangePassword"`
}

// handleAuthMe serves GET /api/v1/auth/me. It replaces the shared
// oidc.MeHandler mount: same echo of the context user, plus the forced-change
// flag read from the registry. shared/auth/oidc stays DB-free — this is the
// hub-side seam allowed to look at the store.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Normalize a nil Roles slice to [] in JSON, as the shared handler did:
	// clients must not have to handle two shapes.
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}
	resp := authMeResponse{Sub: u.Sub, Email: u.Email, Username: u.Username, Roles: roles}
	// Only local accounts have a flag to report. An unknown subject reads as
	// false rather than 404, so /auth/me stays a self-report and not a way to
	// probe the registry.
	if s.authzStore != nil && strings.HasPrefix(u.Sub, authz.LocalUserIDPrefix) {
		if stored, err := s.authzStore.GetUser(r.Context(), u.Sub); err == nil {
			resp.MustChangePassword = stored.MustChangePassword
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleLogout is a stateless no-op kept for API symmetry: clients discard
// their token; there is no server-side session to tear down.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword lets an authenticated LOCAL user change their own
// password after proving knowledge of the current one. A successful change
// clears the forced-change flag (issue #113): the caller just proved the old
// password, so the account now holds one its owner chose.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !strings.HasPrefix(u.Sub, authz.LocalUserIDPrefix) {
		writeError(w, http.StatusBadRequest, "password login is not available for this account")
		return
	}

	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	hash, err := s.authzStore.UserPasswordHash(r.Context(), u.Sub)
	if err != nil || localauth.VerifyPassword(body.Current, hash) != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := s.validatePassword(body.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newHash, err := localauth.HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.authzStore.SetPasswordWithFlag(r.Context(), u.Sub, newHash, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("auth_password_changed", "log_type", "auth_event", "sub", u.Sub)
	s.recordAuthEvent(r, auditevents.TypePasswordChanged, u.Sub, u.Sub, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSignOutAll bumps the caller's token version, invalidating every
// local session JWT issued so far — including the one authenticating this
// request. Complements the stateless /auth/logout no-op: it is the user-facing
// revocation switch for a suspected leaked token (issue #112).
func (s *Server) handleSignOutAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !strings.HasPrefix(u.Sub, authz.LocalUserIDPrefix) {
		writeError(w, http.StatusBadRequest, "not available for this account")
		return
	}
	if err := s.authzStore.BumpTokenVersion(r.Context(), u.Sub); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("auth_sign_out_all", "log_type", "auth_event", "sub", u.Sub)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func formatRetryAfter(d time.Duration) string {
	return strconv.Itoa(int(d.Seconds()) + 1)
}

// loginThrottle tracks consecutive failed logins per username and imposes an
// exponentially growing cooldown after 3 failures. In-memory only: a restart
// resets it, which is acceptable because the per-IP rate limiter still
// applies and the counter recovers on the next failures.
type loginThrottle struct {
	mu      sync.Mutex
	entries map[string]*throttleEntry
}

type throttleEntry struct {
	failures int
	last     time.Time
}

const throttleThreshold = 3

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{entries: make(map[string]*throttleEntry)}
}

// denied returns the remaining cooldown (>0) when the username is locked out.
func (t *loginThrottle) denied(username string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[username]
	if !ok || e.failures < throttleThreshold {
		return 0
	}
	lock := throttleLockout(e.failures)
	remaining := lock - time.Since(e.last)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (t *loginThrottle) recordFailure(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[username]
	if !ok {
		e = &throttleEntry{}
		t.entries[username] = e
	}
	e.failures++
	e.last = time.Now()
}

func (t *loginThrottle) recordSuccess(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, username)
}

// throttleLockout grows 5s, 10s, 20s, ... capped at 60s.
func throttleLockout(failures int) time.Duration {
	n := failures - throttleThreshold + 1
	lock := 5 * time.Second
	for i := 1; i < n && lock < 60*time.Second; i++ {
		lock *= 2
	}
	if lock > 60*time.Second {
		lock = 60 * time.Second
	}
	return lock
}
