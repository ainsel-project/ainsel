// Package auth provides a thin adapter that constructs the shared OIDC
// middleware for the MCP server.
package auth

import (
	"net/http"

	"github.com/DominikPinsel/ainsel/shared/auth/oidc"
)

// Config configures the MCP auth middleware.
type Config struct {
	Issuer    string // OIDC issuer URL (e.g. https://oidc.example.com)
	ProjectID string // Zitadel project ID; required value in token `aud`

	// JWKSURL and UserInfoURL are the resolved OIDC endpoints. Callers must
	// resolve them via oidc.ResolveEndpoints (OIDC discovery); they are not
	// derived from Issuer. An empty JWKSURL is rejected by the shared
	// middleware.
	JWKSURL     string
	UserInfoURL string
}

// NewMiddleware constructs the OIDC middleware. Returns 401 with a plain
// Bearer challenge on failure; no OAuth discovery metadata is advertised.
func NewMiddleware(cfg Config) (func(http.Handler) http.Handler, error) {
	return oidc.NewMiddleware(oidc.Config{
		Issuer:          cfg.Issuer,
		Audience:        cfg.ProjectID,
		JWKSURL:         cfg.JWKSURL,
		UserInfoURL:     cfg.UserInfoURL,
		WWWAuthenticate: `Bearer error="invalid_token"`,
	})
}
