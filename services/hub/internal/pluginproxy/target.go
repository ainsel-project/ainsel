// Package pluginproxy provides functions for proxying requests to plugin backends.
package pluginproxy

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"path"
	"strings"

	"github.com/ainsel-project/ainsel/shared/api/plugin"
)

// Surface identifies a plugin surface.
type Surface string

const (
	SurfaceUI  Surface = "ui"
	SurfaceAPI Surface = "api"
	SurfaceMCP Surface = "mcp"
)

var (
	ErrInvalidPath = errors.New("invalid plugin path")
	ErrNoSurface   = errors.New("plugin does not serve this surface")
)

// Resolve maps the escaped remainder after /api/v1/plugins/<id>/<surface>
// (no leading slash, may be empty) to the upstream path.
func Resolve(m *plugin.Manifest, s Surface, escapedRest string) (string, error) {
	// Validate escapedRest BEFORE any decoding: reject % outright to prevent
	// encoded traversal from being decoded later.
	if err := validateEscapedPath(escapedRest); err != nil {
		return "", ErrInvalidPath
	}

	switch s {
	case SurfaceUI:
		if m.UI == nil {
			return "", ErrNoSurface
		}
		// UI with empty rest is invalid
		if escapedRest == "" {
			return "", ErrInvalidPath
		}
		// Join path.Dir(manifest.ui.entry) + "/" + rest
		return joinPath(path.Dir(m.UI.Entry), escapedRest), nil

	case SurfaceAPI:
		if m.API == nil {
			return "", ErrNoSurface
		}
		// Empty rest -> basePath itself
		return joinPath(m.API.BasePath, escapedRest), nil

	case SurfaceMCP:
		if m.MCP == nil {
			return "", ErrNoSurface
		}
		// Empty rest -> mcp.path itself
		return joinPath(m.MCP.Path, escapedRest), nil

	default:
		return "", ErrInvalidPath
	}
}

// joinPath joins a validated base and rest with exactly one "/"; an empty rest
// yields the base without a trailing slash (but never an empty path).
func joinPath(base, rest string) string {
	base = strings.TrimSuffix(base, "/")
	if rest == "" {
		if base == "" {
			return "/"
		}
		return base
	}
	return base + "/" + rest
}

// validateEscapedPath checks that the escaped rest path does not contain
// forbidden characters or segments. It validates BEFORE any decoding.
func validateEscapedPath(escapedRest string) error {
	// Empty rest is allowed for some surfaces (checked in Resolve)
	if escapedRest == "" {
		return nil
	}

	// Reject % outright to prevent encoded traversal
	if strings.Contains(escapedRest, "%") {
		return ErrInvalidPath
	}

	// Reject backslash
	if strings.Contains(escapedRest, "\\") {
		return ErrInvalidPath
	}

	// Reject a leading slash and double slashes (empty segments)
	if strings.HasPrefix(escapedRest, "/") || strings.Contains(escapedRest, "//") {
		return ErrInvalidPath
	}

	// Check characters - allowed: A-Z a-z 0-9 . _ ~ - /
	for _, r := range escapedRest {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '~' || r == '-' || r == '/':
		default:
			return ErrInvalidPath
		}
	}

	// Check segments for . and ..
	segments := strings.Split(escapedRest, "/")
	for _, seg := range segments {
		// Skip empty segments
		if seg == "" {
			continue
		}
		// . segment is invalid
		if seg == "." {
			return ErrInvalidPath
		}
		// .. segment is invalid
		if seg == ".." {
			return ErrInvalidPath
		}
	}

	return nil
}

// PickEndpoint returns one "ip:port" from endpoints using rnd (nil → math/rand/v2).
func PickEndpoint(endpoints []string, rnd func(n int) int) (string, bool) {
	if len(endpoints) == 0 {
		return "", false
	}

	if rnd == nil {
		rnd = func(n int) int { return rand.IntN(n) }
	}

	idx := rnd(len(endpoints))
	return endpoints[idx], true
}

// SanitizeRequestHeaders applies the remove rules from Global Constraints in place.
// Removes: Authorization, Cookie, Proxy-Authorization, every header starting with
// X-Ainsel- (case-insensitive), Forwarded, X-Forwarded-* (case-insensitive),
// X-Real-Ip.
func SanitizeRequestHeaders(h http.Header) {
	// List of headers to remove exactly
	headersToRemove := []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
		"Forwarded",
		"X-Real-Ip",
	}

	for _, hdr := range headersToRemove {
		h.Del(hdr)
	}

	// Remove headers starting with X-Ainsel- and X-Forwarded- (case-insensitive)
	// We need to iterate over the header map and delete keys that match
	keysToDelete := []string{}
	for key := range h {
		lowerKey := strings.ToLower(key)
		if strings.HasPrefix(lowerKey, "x-ainsel-") || strings.HasPrefix(lowerKey, "x-forwarded-") {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		h.Del(key)
	}
}
