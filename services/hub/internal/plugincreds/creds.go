package plugincreds

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token and claim constants for plugin credentials.
const (
	TokenPrefix  = "ainselp_"
	issuerName   = "ainsel-hub-plugin" // JWT issuer claim value
	DefaultTTL   = 5 * time.Minute
	MinSecretLen = 32

	// Caller kinds.
	KindUser   = "user"
	KindAgent  = "agent"
	KindPlugin = "plugin"
)

// Errors for plugin credentials.
var (
	ErrDisabled = errors.New("plugin credentials are not configured")
	ErrInvalid  = errors.New("invalid plugin credential")
)

// GroupGrant represents a group access grant.
type GroupGrant struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// Claims is the claim set for a plugin credential token.
type Claims struct {
	PluginID     string
	Kind         string
	Sub          string
	Username     string
	Admin        bool // user kind only; informational
	Permissions  []string
	Groups       []GroupGrant
	InvocationID string // optional, agent kind
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// Issuer signs and verifies plugin credential tokens.
type Issuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewIssuer creates a new Issuer. Returns ErrDisabled if secret is too short.
func NewIssuer(secret []byte, ttl time.Duration) (*Issuer, error) {
	if len(secret) < MinSecretLen {
		return nil, ErrDisabled
	}
	return &Issuer{
		secret: secret,
		ttl:    ttl,
		now:    time.Now,
	}, nil
}

// WithClock replaces the time.Now function for testing.
func (i *Issuer) WithClock(now func() time.Time) *Issuer {
	i.now = now
	return i
}

// Issue creates a signed plugin credential token.
func (i *Issuer) Issue(c Claims) (string, time.Time, error) {
	// Validate claims
	if c.PluginID == "" {
		return "", time.Time{}, fmt.Errorf("%w: empty plugin ID", ErrInvalid)
	}
	if !isKnownKind(c.Kind) {
		return "", time.Time{}, fmt.Errorf("%w: unknown kind %s", ErrInvalid, c.Kind)
	}
	if c.Sub == "" {
		return "", time.Time{}, fmt.Errorf("%w: empty subject", ErrInvalid)
	}
	if c.Admin && c.Kind != KindUser {
		return "", time.Time{}, fmt.Errorf("%w: admin flag only for user kind", ErrInvalid)
	}

	// Validate subject format
	switch c.Kind {
	case KindAgent:
		if !isValidAgentSub(c.Sub) {
			return "", time.Time{}, fmt.Errorf("%w: invalid agent subject %s", ErrInvalid, c.Sub)
		}
	case KindPlugin:
		if !isValidPluginSub(c.Sub, c.PluginID) {
			return "", time.Time{}, fmt.Errorf("%w: invalid plugin subject %s", ErrInvalid, c.Sub)
		}
	}

	now := i.now()
	exp := now.Add(i.ttl)

	// Deduplicate and sort permissions
	perms := make([]string, len(c.Permissions))
	copy(perms, c.Permissions)
	slices.Sort(perms)
	perms = slices.Compact(perms)

	// Sort groups by ID
	groups := make([]GroupGrant, len(c.Groups))
	copy(groups, c.Groups)
	slices.SortFunc(groups, func(a, b GroupGrant) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	// Generate random JTI (16 bytes hex = 32 chars)
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", time.Time{}, fmt.Errorf("jti generation: %w", err)
	}
	jti := hex.EncodeToString(jtiBytes)

	// Build JWT claims
	claims := jwt.MapClaims{
		"iss":   issuerName,
		"aud":   "plugin:" + c.PluginID,
		"sub":   c.Sub,
		"iat":   now.Unix(),
		"exp":   exp.Unix(),
		"jti":   jti,
		"pid":   c.PluginID,
		"kind":  c.Kind,
		"name":  c.Username,
		"adm":   c.Admin,
		"perms": perms,
		"grps":  groups,
	}
	if c.InvocationID != "" {
		claims["inv"] = c.InvocationID
	}

	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}

	return TokenPrefix + tok, exp, nil
}

// Verify parses and validates a plugin credential token.
func (i *Issuer) Verify(token string) (*Claims, error) {
	// Check prefix
	if !LooksLike(token) {
		return nil, ErrInvalid
	}
	// Strip prefix for parsing
	raw := token[len(TokenPrefix):]

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(issuerName),
		jwt.WithExpirationRequired(),
	)

	parsed, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected claims type", ErrInvalid)
	}

	// Extract plugin ID from pid claim and validate against aud
	pid, _ := mc["pid"].(string)
	if pid == "" {
		return nil, fmt.Errorf("%w: missing pid claim", ErrInvalid)
	}

	// Validate aud matches plugin:<pid>
	aud, _ := mc["aud"].(string)
	expectedAud := "plugin:" + pid
	if aud != expectedAud {
		return nil, fmt.Errorf("%w: aud mismatch", ErrInvalid)
	}

	if kind, _ := mc["kind"].(string); !isKnownKind(kind) {
		return nil, fmt.Errorf("%w: unknown kind", ErrInvalid)
	}

	// Extract other claims
	c := &Claims{
		PluginID: pid,
	}

	if v, ok := mc["kind"].(string); ok {
		c.Kind = v
	}
	if v, ok := mc["sub"].(string); ok {
		c.Sub = v
	}
	if v, ok := mc["name"].(string); ok {
		c.Username = v
	}
	if v, ok := mc["adm"].(bool); ok {
		c.Admin = v
	}
	if v, ok := mc["inv"].(string); ok {
		c.InvocationID = v
	}

	// Extract permissions (array of strings)
	if permsRaw, ok := mc["perms"].([]interface{}); ok {
		for _, p := range permsRaw {
			if str, ok := p.(string); ok {
				c.Permissions = append(c.Permissions, str)
			}
		}
	}

	// Extract groups (array of objects)
	if grpsRaw, ok := mc["grps"].([]interface{}); ok {
		for _, g := range grpsRaw {
			if gMap, ok := g.(map[string]interface{}); ok {
				gg := GroupGrant{}
				if id, ok := gMap["id"].(string); ok {
					gg.ID = id
				}
				if role, ok := gMap["role"].(string); ok {
					gg.Role = role
				}
				c.Groups = append(c.Groups, gg)
			}
		}
	}

	// Extract timestamps
	if iat, err := mc.GetIssuedAt(); err == nil && iat != nil {
		c.IssuedAt = iat.Time
	}
	if exp, err := mc.GetExpirationTime(); err == nil && exp != nil {
		c.ExpiresAt = exp.Time
	}

	return c, nil
}

// LooksLike returns true if a token looks like a plugin credential (has prefix).
func LooksLike(token string) bool {
	return len(token) > len(TokenPrefix) && token[:len(TokenPrefix)] == TokenPrefix
}

// Helper functions

func isKnownKind(kind string) bool {
	return kind == KindUser || kind == KindAgent || kind == KindPlugin
}

func isValidAgentSub(sub string) bool {
	return len(sub) > len("agent:") && sub[:6] == "agent:"
}

func isValidPluginSub(sub, pluginID string) bool {
	expected := "plugin:" + pluginID
	return sub == expected
}
