package plugincreds

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestNewIssuer_ShortSecret tests that NewIssuer returns ErrDisabled for short secrets.
func TestNewIssuer_ShortSecret(t *testing.T) {
	_, err := NewIssuer([]byte("short"), DefaultTTL)
	if !errors.Is(err, ErrDisabled) {
		t.Errorf("NewIssuer with short secret: got %v, want ErrDisabled", err)
	}
}

// TestIssueVerify_RoundTrip tests that claims survive issue/verify round-trip for each kind.
func TestIssueVerify_RoundTrip(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}
	issuer, err := NewIssuer(secret, DefaultTTL)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	tests := []struct {
		name   string
		claims Claims
	}{
		{
			name: "user kind",
			claims: Claims{
				PluginID:    "test-plugin",
				Kind:        KindUser,
				Sub:         "user:alice",
				Username:    "alice",
				Admin:       true,
				Permissions: []string{"b", "a", "a"}, // Should be deduplicated and sorted to ["a", "b"]
				Groups:      []GroupGrant{{ID: "g1", Role: "reader"}},
			},
		},
		{
			name: "agent kind",
			claims: Claims{
				PluginID:     "test-plugin",
				Kind:         KindAgent,
				Sub:          "agent:reviewer",
				Username:     "",
				Admin:        false,
				Permissions:  []string{"agents:read", "skills:read"},
				Groups:       []GroupGrant{{ID: "owning-group", Role: "reader"}},
				InvocationID: "inv-123",
			},
		},
		{
			name: "plugin kind",
			claims: Claims{
				PluginID:    "test-plugin",
				Kind:        KindPlugin,
				Sub:         "plugin:test-plugin",
				Username:    "test-plugin",
				Admin:       false,
				Permissions: []string{"invocations:read"},
				Groups: []GroupGrant{
					{ID: "z-group", Role: "writer"},
					{ID: "a-group", Role: "writer"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, expiry, err := issuer.Issue(tt.claims)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}

			// Token should have prefix
			if !LooksLike(token) {
				t.Errorf("Token doesn't have prefix, got: %s", token[:20])
			}

			// Verify token
			verified, err := issuer.Verify(token)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}

			// Check fields
			if verified.PluginID != tt.claims.PluginID {
				t.Errorf("PluginID: got %s, want %s", verified.PluginID, tt.claims.PluginID)
			}
			if verified.Kind != tt.claims.Kind {
				t.Errorf("Kind: got %s, want %s", verified.Kind, tt.claims.Kind)
			}
			if verified.Sub != tt.claims.Sub {
				t.Errorf("Sub: got %s, want %s", verified.Sub, tt.claims.Sub)
			}
			if verified.Username != tt.claims.Username {
				t.Errorf("Username: got %s, want %s", verified.Username, tt.claims.Username)
			}
			if verified.Admin != tt.claims.Admin && tt.claims.Kind == KindUser {
				t.Errorf("Admin: got %v, want %v", verified.Admin, tt.claims.Admin)
			}
			if verified.InvocationID != tt.claims.InvocationID {
				t.Errorf("InvocationID: got %s, want %s", verified.InvocationID, tt.claims.InvocationID)
			}

			// Check permissions are sorted and deduplicated
			if tt.name == "user kind" {
				expectedPerms := []string{"a", "b"}
				if len(verified.Permissions) != len(expectedPerms) {
					t.Errorf("Permissions length: got %d, want %d", len(verified.Permissions), len(expectedPerms))
				}
				for i, p := range verified.Permissions {
					if i < len(expectedPerms) && p != expectedPerms[i] {
						t.Errorf("Permissions[%d]: got %s, want %s", i, p, expectedPerms[i])
					}
				}
			}

			// Check groups are sorted by ID
			if len(tt.claims.Groups) > 1 {
				if verified.Groups[0].ID != "a-group" {
					t.Errorf("Groups not sorted: got %s as first, want a-group", verified.Groups[0].ID)
				}
			}

			// Check expiry
			if expiry.After(time.Now().Add(DefaultTTL + time.Second)) {
				t.Errorf("Expiry too far in future: %v", expiry)
			}
		})
	}
}

// TestVerify_Expired tests that expired tokens are rejected.
func TestVerify_Expired(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}

	// Issue a token with an expiration time in the past
	past := time.Now().Add(-1 * time.Hour)
	issuer, _ := NewIssuer(secret, DefaultTTL)
	issuer.WithClock(func() time.Time { return past })

	token, _, err := issuer.Issue(Claims{
		PluginID: "test",
		Kind:     KindUser,
		Sub:      "user:test",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Create a new issuer for verification (uses current time.Now())
	verifier, _ := NewIssuer(secret, DefaultTTL)
	_, err = verifier.Verify(token)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify expired token: got %v, want ErrInvalid", err)
	}
}

// TestVerify_WrongSecret tests that tokens signed with wrong secret are rejected.
func TestVerify_WrongSecret(t *testing.T) {
	secret1 := make([]byte, MinSecretLen)
	for i := range secret1 {
		secret1[i] = byte(i % 256)
	}
	secret2 := make([]byte, MinSecretLen)
	for i := range secret2 {
		secret2[i] = byte((i + 1) % 256)
	}

	issuer1, _ := NewIssuer(secret1, DefaultTTL)
	token, _, _ := issuer1.Issue(Claims{
		PluginID: "test",
		Kind:     KindUser,
		Sub:      "user:test",
	})

	issuer2, _ := NewIssuer(secret2, DefaultTTL)
	_, err := issuer2.Verify(token)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify with wrong secret: got %v, want ErrInvalid", err)
	}
}

// TestVerify_Tampered tests that tampered tokens are rejected.
func TestVerify_Tampered(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}
	issuer, _ := NewIssuer(secret, DefaultTTL)

	token, _, _ := issuer.Issue(Claims{
		PluginID: "test",
		Kind:     KindUser,
		Sub:      "user:test",
	})

	// Tamper with a character in the middle of the token (in the payload part)
	// The token structure is: prefix + header.payload.signature
	// We'll flip a character in the payload section
	if len(token) > 40 {
		tokenBytes := []byte(token)
		// Flip a character somewhere in the middle
		midpoint := len(tokenBytes) / 2
		if tokenBytes[midpoint] == 'A' {
			tokenBytes[midpoint] = 'B'
		} else {
			tokenBytes[midpoint] = 'A'
		}
		tampered := string(tokenBytes)

		_, err := issuer.Verify(tampered)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Verify tampered token: got %v, want ErrInvalid", err)
		}
	} else {
		t.Skip("token too short for tamper test")
	}
}

// TestVerify_MissingPrefix tests that tokens without prefix are rejected.
func TestVerify_MissingPrefix(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}
	issuer, _ := NewIssuer(secret, DefaultTTL)

	token, _, _ := issuer.Issue(Claims{
		PluginID: "test",
		Kind:     KindUser,
		Sub:      "user:test",
	})

	// Remove prefix
	raw := token[len(TokenPrefix):]
	_, err := issuer.Verify(raw)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify without prefix: got %v, want ErrInvalid", err)
	}
}

// TestVerify_AlgNone tests that alg:none tokens are rejected.
func TestVerify_AlgNone(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}
	issuer, _ := NewIssuer(secret, DefaultTTL)

	// Create a minimal alg:none token with the prefix
	algNoneToken := TokenPrefix + "eyJhbGciOiJub25lIn0.eyJpc3MiOiJhaW5zZWwtaHViLXBsdWdpbiIsInN1YiI6InVzZXI6dGVzdCJ9."

	_, err := issuer.Verify(algNoneToken)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify alg:none token: got %v, want ErrInvalid", err)
	}
}

// TestVerify_LocalSessionJWTRejected tests that local session JWTs are rejected.
func TestVerify_LocalSessionJWTRejected(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}

	// Create a token with the same secret but different issuer (local session JWT)
	claims := map[string]interface{}{
		"iss": "ainsel-hub", // Different issuer
		"sub": "user:test",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	localToken, _ := (&jwt.Token{
		Method: jwt.SigningMethodHS256,
		Claims: jwt.MapClaims(claims),
	}).SignedString(secret)

	// Add the plugin credential prefix
	fakeToken := TokenPrefix + localToken

	issuer, _ := NewIssuer(secret, DefaultTTL)
	_, err := issuer.Verify(fakeToken)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify local session JWT: got %v, want ErrInvalid", err)
	}
}

// TestIssue_RejectsBadClaims tests that invalid claims are rejected.
func TestIssue_RejectsBadClaims(t *testing.T) {
	secret := make([]byte, MinSecretLen)
	for i := range secret {
		secret[i] = byte(i % 256)
	}
	issuer, _ := NewIssuer(secret, DefaultTTL)

	tests := []struct {
		name   string
		claims Claims
	}{
		{
			name: "empty plugin ID",
			claims: Claims{
				PluginID: "",
				Kind:     KindUser,
				Sub:      "user:test",
			},
		},
		{
			name: "unknown kind",
			claims: Claims{
				PluginID: "test",
				Kind:     "unknown",
				Sub:      "user:test",
			},
		},
		{
			name: "empty subject",
			claims: Claims{
				PluginID: "test",
				Kind:     KindUser,
				Sub:      "",
			},
		},
		{
			name: "admin on non-user kind",
			claims: Claims{
				PluginID: "test",
				Kind:     KindAgent,
				Sub:      "agent:test",
				Admin:    true,
			},
		},
		{
			name: "invalid agent subject",
			claims: Claims{
				PluginID: "test",
				Kind:     KindAgent,
				Sub:      "not-agent:test",
			},
		},
		{
			name: "invalid plugin subject",
			claims: Claims{
				PluginID: "test",
				Kind:     KindPlugin,
				Sub:      "plugin:other",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := issuer.Issue(tt.claims)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Issue: got %v, want ErrInvalid", err)
			}
		})
	}
}
