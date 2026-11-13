package api

import (
	"context"
	"sort"
	"strings"

	"github.com/ainsel-project/ainsel/services/hub/internal/pluginsync"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// isHubPluginURL reports whether url is one of the hub's own internal plugin
// endpoints (<hubSelfURL>/api/internal/plugins/...). Only such servers may be
// given the platform credential: handing it to any other URL would let a
// user-registered server collect it. Mirrors the operator-side check in
// operators/agent/internal/controller/mcpservers (IsHubPluginURL) — the
// operator package is internal to the operator module, so the rule lives in
// both places by necessity; change them together.
func isHubPluginURL(url, hubSelfURL string) bool {
	hubSelfURL = strings.TrimSuffix(hubSelfURL, "/")
	if hubSelfURL == "" {
		return false
	}
	return strings.HasPrefix(url, hubSelfURL+"/api/internal/plugins/")
}

// lookupEnvVar returns the value of the named AgentImage env var, or "" when
// the image does not define it or defines it with an empty value. Secret
// entries resolve like any other: the secret flag only masks the value on API
// reads, the CR itself stores it.
func lookupEnvVar(env []agentv1alpha1.AgentImageEnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

// resolveMCPServerToken resolves the bearer token to present to an MCP server
// whose registry row names tokenFromEnv, applying the same rule the operator
// uses to inject the credential into the agent pod
// (operators/agent/internal/controller/mcpservers, TokenEnvValueWithPlatform):
//
//   - An empty tokenFromEnv means anonymous; the env is never searched.
//   - Platform exception: a server pointing at the hub's own internal plugin
//     endpoints (isHubPluginURL) may use the hub's internal-validate secret,
//     even though no image defines it as an env var. A user-registered row
//     naming the same var never receives the platform secret.
//   - Otherwise the value is looked up by exact name in imgEnv (the image's
//     spec.env), non-empty matches only.
//   - When fallbackToReferencingImages is set and imgEnv yields nothing, the
//     hub searches the AgentImages that reference the server by name, in
//     ascending image-name order, and takes the first image whose spec.env
//     defines the var with a non-empty value. Tool names and descriptions are
//     server-global, so any correctly-tokenized view is the right catalog;
//     if two referencing images define different values, first-by-name wins.
//
// Anything unresolved yields "" (anonymous) rather than an error — the same
// skip-if-missing direction the operator takes, so a wrong token is never
// sent. A var supplied only by connector/operator injection (not spec.env)
// resolves to nothing and the listing stays anonymous. The returned value is
// a credential: never log it and never echo it into a response (see
// redactToken).
func (s *Server) resolveMCPServerToken(ctx context.Context, serverName, tokenFromEnv, url string, imgEnv []agentv1alpha1.AgentImageEnvVar, fallbackToReferencingImages bool) (string, error) {
	if tokenFromEnv == "" {
		return "", nil
	}
	if tokenFromEnv == pluginsync.MCPServerTokenFromEnv && isHubPluginURL(url, s.hubSelfURL) && s.internalValidateSecret != "" {
		return s.internalValidateSecret, nil
	}
	if v := lookupEnvVar(imgEnv, tokenFromEnv); v != "" {
		return v, nil
	}
	if !fallbackToReferencingImages {
		return "", nil
	}
	var list agentv1alpha1.AgentImageList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		return "", err
	}
	var referencing []agentv1alpha1.AgentImage
	for _, img := range list.Items {
		for _, m := range img.Spec.MCPServers {
			if m.Name == serverName {
				referencing = append(referencing, img)
				break
			}
		}
	}
	sort.Slice(referencing, func(i, j int) bool {
		return referencing[i].Name < referencing[j].Name
	})
	for _, img := range referencing {
		if v := lookupEnvVar(img.Spec.Env, tokenFromEnv); v != "" {
			return v, nil
		}
	}
	return "", nil
}

// redactToken scrubs the credential value out of an error string. Errors from
// the MCP transport can echo request context (some servers reflect the
// Authorization header back in their 401 body), and the refresh warnings and
// the tools-proxy 502 body are user-visible.
func redactToken(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[redacted]")
}
