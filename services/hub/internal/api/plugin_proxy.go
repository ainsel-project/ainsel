package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginproxy"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// servePluginProxy handles requests to /api/v1/plugins/<id>/<surface>/<rest>
// and proxies them to the upstream plugin endpoint.
func (s *Server) servePluginProxy(w http.ResponseWriter, r *http.Request, pluginID string, surface pluginproxy.Surface, escapedRest string) {
	// UI surfaces only support GET and HEAD
	if surface == pluginproxy.SurfaceUI && r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()

	// Get the plugin CR
	plugin, err := s.availablePlugin(ctx, pluginID)
	if err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}

	// Unmarshal the manifest
	var manifest pluginpkg.Manifest
	if err := json.Unmarshal(plugin.Status.Manifest.Raw, &manifest); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}

	// Resolve the upstream path
	upstreamPath, err := pluginproxy.Resolve(&manifest, surface, escapedRest)
	if err != nil {
		if errors.Is(err, pluginproxy.ErrInvalidPath) {
			writeError(w, http.StatusBadRequest, "invalid plugin path")
		} else if errors.Is(err, pluginproxy.ErrNoSurface) {
			writeError(w, http.StatusNotFound, "plugin not found")
		} else {
			writeError(w, http.StatusNotFound, "plugin not found")
		}
		return
	}

	// Pick an endpoint
	endpoint, ok := pluginproxy.PickEndpoint(plugin.Status.VerifiedEndpoints, nil)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "plugin has no verified endpoints")
		return
	}

	// For API and MCP with an authenticated user, mint the credential and
	// context up front so any failure stops the request instead of forwarding
	// it without them. Without a user (dev mode, no auth) nothing is attached.
	var credential, contextHeader string
	if surface != pluginproxy.SurfaceUI {
		if u, ok := oidc.FromContext(ctx); ok && u != nil && u.Sub != "" {
			cred, _, err := s.MintUserPluginCredential(ctx, pluginID, u)
			if errors.Is(err, plugincreds.ErrDisabled) {
				writeError(w, http.StatusServiceUnavailable, "plugin credentials are not configured")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			pc, err := s.userPluginContext(r, u)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			pc.PluginID = pluginID
			pc.Permissions = plugin.Spec.ApprovedPermissions
			if pc.Permissions == nil {
				pc.Permissions = []string{}
			}
			raw, err := json.Marshal(pc)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			credential = cred
			contextHeader = base64.RawURLEncoding.EncodeToString(raw)
		}
	}

	// Set up the reverse proxy
	pr := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Set the upstream URL - create a new URL if needed
			if pr.Out.URL == nil {
				pr.Out.URL = &url.URL{}
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = endpoint
			pr.Out.URL.Path = upstreamPath
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = r.URL.RawQuery

			// Set Host header to the endpoint
			pr.Out.Host = endpoint

			// Sanitize request headers
			pluginproxy.SanitizeRequestHeaders(pr.Out.Header)

			// Set X-Ainsel-Plugin-Id
			pr.Out.Header.Set("X-Ainsel-Plugin-Id", pluginID)

			// Set X-Ainsel-Forwarded-Prefix
			prefix := fmt.Sprintf("/api/v1/plugins/%s/%s", pluginID, surface)
			pr.Out.Header.Set("X-Ainsel-Forwarded-Prefix", prefix)

			if credential != "" {
				pr.Out.Header.Set("X-Ainsel-Credential", credential)
				pr.Out.Header.Set("X-Ainsel-Context", contextHeader)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			// Remove Set-Cookie headers from upstream
			res.Header.Del("Set-Cookie")

			// Set X-Content-Type-Options: nosniff
			res.Header.Set("X-Content-Type-Options", "nosniff")

			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadGateway, "plugin unreachable")
		},
		FlushInterval: -1,
		Transport:     s.getPluginProxyTransport(),
	}

	pr.ServeHTTP(w, r)
}

// userPluginContext builds a pluginContextResponse for a user.
// This factors out the logic from handlePluginContext for reuse in the proxy.
func (s *Server) userPluginContext(r *http.Request, u *oidc.User) (pluginContextResponse, error) {
	resp := pluginContextResponse{
		Kind:     plugincreds.KindUser,
		Sub:      u.Sub,
		Username: u.Username,
		IsAdmin:  s.callerIsAdmin(r),
		Groups:   []pluginContextGroup{},
	}

	if s.authzStore != nil {
		roles, err := s.authzStore.UserGroupRoles(r.Context(), u.Sub)
		if err != nil {
			return pluginContextResponse{}, err
		}
		for id, role := range roles {
			resp.Groups = append(resp.Groups, pluginContextGroup{
				ID:   id,
				Name: s.groupName(r, id),
				Role: string(role),
			})
		}
	}

	sort.Slice(resp.Groups, func(a, b int) bool { return resp.Groups[a].ID < resp.Groups[b].ID })
	return resp, nil
}

// getPluginProxyTransport returns the lazily-initialized plugin proxy transport.
func (s *Server) getPluginProxyTransport() http.RoundTripper {
	// Once: concurrent first proxy requests must not race on the field.
	s.pluginProxyOnce.Do(func() {
		s.pluginProxyTransport = &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConnsPerHost:   16,
		}
	})
	return s.pluginProxyTransport
}
