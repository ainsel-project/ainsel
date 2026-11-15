package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newUserTokenStub mimics the hub's personal-access-token routes. The plaintext
// value exists only in the POST response — the hub stores a hash — which is the
// contract these tests pin.
func newUserTokenStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user-tokens":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "ut-1", "name": "ci", "createdAt": "2026-11-01T00:00:00Z"},
				{"id": "ut-2", "name": "laptop", "expiresAt": "2026-12-01T00:00:00Z"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/user-tokens":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] == "" {
				writeErrorJSON(w, http.StatusBadRequest, "name is required")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ainsel_secret_value_123"}) // #nosec G101 -- test fixture, not a credential
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/user-tokens/ut-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/user-tokens/ut-404":
			writeErrorJSON(w, http.StatusNotFound, "token not found")
		default:
			writeErrorJSON(w, http.StatusNotFound, "unexpected path "+r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListUserTokensNeverCarriesAValue(t *testing.T) {
	srv := newUserTokenStub(t)
	tt := &UserTokenTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	text := userToolText(t, tt.ListUserTokens, nil)
	if !strings.Contains(text, "ut-1") || !strings.Contains(text, "ci") {
		t.Errorf("expected the token rows; got: %s", text)
	}
	if strings.Contains(text, "ainsel_") || strings.Contains(text, "token\":") {
		t.Errorf("a token value must never appear in a list; got: %s", text)
	}
}

func TestCreateUserTokenReturnsTheValueExactlyOnce(t *testing.T) {
	srv := newUserTokenStub(t)
	tt := &UserTokenTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	text := userToolText(t, tt.CreateUserToken, map[string]any{"name": "ci", "expires_in_days": float64(30)})

	const value = "ainsel_secret_value_123"
	if strings.Count(text, value) != 1 {
		t.Errorf("expected the token value exactly once; got: %s", text)
	}
	if !strings.Contains(text, "shown once") {
		t.Errorf("expected a note that the value is shown once; got: %s", text)
	}

	// And the read path cannot show it again.
	list := userToolText(t, tt.ListUserTokens, nil)
	if strings.Contains(list, value) {
		t.Errorf("list_user_tokens echoed a created token value")
	}
}

func TestCreateUserTokenRequiresName(t *testing.T) {
	srv := newUserTokenStub(t)
	tt := &UserTokenTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	if msg := userToolError(t, tt.CreateUserToken, map[string]any{}); !strings.Contains(msg, "name is required") {
		t.Errorf("expected a missing-name error; got: %s", msg)
	}
}

func TestRevokeUserToken(t *testing.T) {
	srv := newUserTokenStub(t)
	tt := &UserTokenTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	text := userToolText(t, tt.RevokeUserToken, map[string]any{"id": "ut-1"})
	if !strings.Contains(text, "ut-1 revoked") {
		t.Errorf("expected a revocation confirmation; got: %s", text)
	}
	msg := userToolError(t, tt.RevokeUserToken, map[string]any{"id": "ut-404"})
	if !strings.Contains(msg, "404") || !strings.Contains(msg, "token not found") {
		t.Errorf("expected the hub's 404 to pass through; got: %s", msg)
	}
	if msg := userToolError(t, tt.RevokeUserToken, map[string]any{}); !strings.Contains(msg, "id is required") {
		t.Errorf("expected a missing-id error; got: %s", msg)
	}
}
