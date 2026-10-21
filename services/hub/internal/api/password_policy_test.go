package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
)

// --- policy validation (unit) ---

func TestPasswordPolicyValidate(t *testing.T) {
	def := DefaultPasswordPolicy()

	cases := []struct {
		name    string
		policy  PasswordPolicy
		pass    string
		wantErr string // "" = must pass; else must be a substring of the error
	}{
		// Default policy: byte-identical to the historical min 8 / max 128.
		{"default: 7 chars rejected", def, "1234567", "at least 8 characters"},
		{"default: 8 chars accepted", def, "12345678", ""},
		{"default: 128 chars accepted", def, strings.Repeat("a", 128), ""},
		{"default: 129 chars rejected", def, strings.Repeat("a", 129), "at most 128 characters"},
		{"default: no class requirements", def, "alllowercase", ""},

		// Character classes (ASCII-only definitions).
		{"upper required, missing", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassUpper}}, "abcdef1!", "1 uppercase letter"},
		{"upper required, present", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassUpper}}, "aBcdef1!", ""},
		{"digit required, missing", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassDigit}}, "abcdefg!", "1 digit"},
		{"lower required, missing", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassLower}}, "ABCDEF1!", "1 lowercase letter"},
		{"symbol required, space counts", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassSymbol}}, "abcd ef", ""},
		{"symbol required, missing", PasswordPolicy{MinLength: 4, RequireClasses: []string{PasswordClassSymbol}}, "abcdefg1", "1 symbol"},
		{"symbol: non-ASCII is not a symbol", PasswordPolicy{MinLength: 2, RequireClasses: []string{PasswordClassSymbol}}, "abéé", "1 symbol"},

		// All violations in one message.
		{
			"all violations listed",
			PasswordPolicy{MinLength: 12, RequireClasses: []string{PasswordClassUpper, PasswordClassDigit}},
			"lowercase",
			"password does not meet the password policy: at least 12 characters; 1 uppercase letter; 1 digit",
		},

		// Zero-value policy normalizes to the default minimum, never to "no minimum".
		{"zero-value policy keeps min 8", PasswordPolicy{}, "abc", "at least 8 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate(tc.pass)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", tc.pass, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want error containing %q", tc.pass, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate(%q) = %q, want containing %q", tc.pass, err, tc.wantErr)
			}
		})
	}
}

func TestParsePasswordCharacterClasses(t *testing.T) {
	cases := []struct {
		raw     string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"  ,  ", nil, false},
		{"upper", []string{"upper"}, false},
		{"UPPER,digit", []string{"upper", "digit"}, false},
		{" lower , symbol ", []string{"lower", "symbol"}, false},
		{"digit,digit", []string{"digit"}, false},
		{"bogus", nil, true},
		{"upper,spaceship", nil, true},
	}
	for _, tc := range cases {
		got, err := ParsePasswordCharacterClasses(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParsePasswordCharacterClasses(%q): want error, got %v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParsePasswordCharacterClasses(%q): unexpected error %v", tc.raw, err)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("ParsePasswordCharacterClasses(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// --- policy endpoint ---

func policyEndpointServer(t *testing.T) *Server {
	t.Helper()
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)
	s.mux.HandleFunc("/api/v1/auth/password-policy", s.handlePasswordPolicy)
	return s
}

func TestPasswordPolicyEndpointDefaults(t *testing.T) {
	s := policyEndpointServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/password-policy", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var resp struct {
		MinLength               int      `json:"minLength"`
		MaxLength               int      `json:"maxLength"`
		RequireCharacterClasses []string `json:"requireCharacterClasses"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.MinLength != 8 || resp.MaxLength != 128 || len(resp.RequireCharacterClasses) != 0 {
		t.Fatalf("unexpected default policy: %+v", resp)
	}
	// The classes list must serialize as [] not null so the frontend can
	// iterate without a null check.
	if !strings.Contains(body, `"requireCharacterClasses":[]`) {
		t.Fatalf("body = %s, want requireCharacterClasses as []", body)
	}
}

func TestPasswordPolicyEndpointConfigured(t *testing.T) {
	s := policyEndpointServer(t)
	s.SetPasswordPolicy(PasswordPolicy{MinLength: 12, RequireClasses: []string{PasswordClassUpper, PasswordClassDigit}})

	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/password-policy", nil, asUser("local:bob", "bob"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	// Non-admin local users may read the policy: the change-password form
	// needs it.
	var resp struct {
		MinLength               int      `json:"minLength"`
		MaxLength               int      `json:"maxLength"`
		RequireCharacterClasses []string `json:"requireCharacterClasses"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MinLength != 12 || resp.MaxLength != 128 || strings.Join(resp.RequireCharacterClasses, ",") != "upper,digit" {
		t.Fatalf("unexpected policy: %+v", resp)
	}
}

func TestPasswordPolicyEndpointMethodNotAllowed(t *testing.T) {
	s := policyEndpointServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/password-policy", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestPasswordPolicyEndpointBehindAuthMiddleware(t *testing.T) {
	s := policyEndpointServer(t)
	// The endpoint must not be auth-exempt: with the middleware installed it
	// sits behind it like every other /api/v1/* path.
	s.authMW = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		})
	}
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/password-policy", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- enforcement on the three password-setting paths ---

const strictPolicyMsg = "password does not meet the password policy: at least 12 characters; 1 uppercase letter; 1 digit"

func strictPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: 12, RequireClasses: []string{PasswordClassUpper, PasswordClassDigit}}
}

func TestChangePasswordEnforcesPolicy(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)
	s.SetPasswordPolicy(strictPolicy())

	rec := doJSON(t, s, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current": "admin-password", "new": "lowercase",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), strictPolicyMsg) {
		t.Fatalf("body = %s, want all violations in one message", rec.Body.String())
	}
	// Old password still works: the rejected change must not have been applied.
	if err := localauth.VerifyPassword("admin-password", store.passwords["local:admin"]); err != nil {
		t.Fatalf("password must be unchanged after rejection: %v", err)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current": "admin-password", "new": "StrategicPassword9",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("compliant password: status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestCreateUserEnforcesPolicy(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)
	s.SetPasswordPolicy(strictPolicy())
	admin := asUser("local:admin", "admin")

	rec := doJSON(t, s, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "bob", "password": "short",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), strictPolicyMsg) {
		t.Fatalf("body = %s, want all violations in one message", rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "bob", "password": "StrategicPassword9",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("compliant password: status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestAdminResetPasswordEnforcesPolicy(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)
	s.SetPasswordPolicy(strictPolicy())
	admin := asUser("local:admin", "admin")

	hash, _ := localauth.HashPassword("user-password")
	if _, err := store.CreateLocalUser(t.Context(), "local:bob", "", "bob", hash, false, false); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodPatch, "/api/v1/users/local:bob", map[string]any{
		"password": "short",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), strictPolicyMsg) {
		t.Fatalf("body = %s, want all violations in one message", rec.Body.String())
	}
	// Existing password untouched.
	if err := localauth.VerifyPassword("user-password", store.passwords["local:bob"]); err != nil {
		t.Fatalf("password must be unchanged after rejection: %v", err)
	}

	rec = doJSON(t, s, http.MethodPatch, "/api/v1/users/local:bob", map[string]any{
		"password": "StrategicPassword9",
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("compliant password: status = %d, body %s", rec.Code, rec.Body.String())
	}
}
