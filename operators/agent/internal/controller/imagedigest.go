package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// imageDigestAnnotation is stamped on the Deployment pod template.
	// When the digest behind a mutable tag changes (e.g. :dev is rebuilt),
	// the annotation value changes and Kubernetes performs a rolling restart.
	imageDigestAnnotation = "ainsel.dev/image-digest"

	// dockerHubRegistry is the default registry when no host is specified.
	dockerHubRegistry = "registry-1.docker.io"

	// digestResolveTimeout is the HTTP timeout for a single registry or
	// auth request. Digest resolution chains up to three of them
	// (manifest → challenge, token, retried manifest), each bounded
	// independently so a slow auth endpoint cannot stall a reconcile
	// beyond a bounded multiple of this value.
	digestResolveTimeout = 10 * time.Second

	// tokenSafetyMargin is subtracted from a cached token's lifetime so we
	// never present a token that is about to expire.
	tokenSafetyMargin = 10 * time.Second

	// defaultTokenTTL is used when the auth endpoint omits expires_in.
	defaultTokenTTL = 60 * time.Second
)

// manifestAcceptTypes are the manifest media types we ask the registry for.
// Covering both Docker and OCI manifest/index types keeps resolution working
// across registries and multi-arch images.
var manifestAcceptTypes = strings.Join([]string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
}, ", ")

// cachedToken is a registry bearer token with its validity window.
type cachedToken struct {
	token  string
	expiry time.Time
}

// DigestResolver resolves image references to registry manifest digests so
// the restart-on-rebuild annotation can be stamped on agent pod templates.
//
// It speaks the standard OCI distribution auth flow: the manifest request is
// sent anonymously first (or with a cached bearer token); on a 401/403 the
// WWW-Authenticate challenge is followed — Bearer realms are exchanged for a
// token, Basic challenges are answered with pull credentials when available —
// and the manifest request is retried once with the credential. This covers
// Docker Hub, GHCR, ECR, GAR, Harbor, and any spec-compliant registry with a
// single code path.
//
// Registries are contacted over HTTPS unless the registry host is listed in
// Insecure, in which case plain HTTP is used directly (never upgraded to
// HTTPS, and HTTPS registries never fall back to HTTP).
//
// Credentials are resolved lazily — only when a challenge actually requires
// them — from the pod template's imagePullSecrets, falling back to the
// ServiceAccount the pod would use. Anonymous-capable registries therefore
// never trigger ServiceAccount/Secret reads.
//
// Resolution failures are returned as errors; callers must treat them as
// non-fatal and keep reconciling (the annotation is simply skipped).
type DigestResolver struct {
	// Client reads ServiceAccounts and pull-secret Secrets. May be nil,
	// in which case resolution is anonymous-only.
	Client client.Client

	// HTTPClient is used for all registry and auth requests. When nil a
	// client with digestResolveTimeout is created.
	HTTPClient *http.Client

	// Insecure holds registry hosts (exact host:port match) that must be
	// contacted over plain HTTP. All other hosts are HTTPS-only.
	Insecure map[string]struct{}

	tokenMu sync.Mutex
	tokens  map[string]cachedToken
}

// NewDigestResolver builds a resolver. insecureRegistries is a list of
// registry hosts (e.g. "zot.platform.svc:5000") that are reachable over
// plain HTTP only; everything else is contacted over HTTPS.
func NewDigestResolver(c client.Client, insecureRegistries []string) *DigestResolver {
	insecure := make(map[string]struct{}, len(insecureRegistries))
	for _, r := range insecureRegistries {
		if host := strings.TrimSpace(r); host != "" {
			insecure[host] = struct{}{}
		}
	}
	return &DigestResolver{
		Client:   c,
		Insecure: insecure,
	}
}

// defaultDigestResolver is the fallback resolver used when a reconciler was
// constructed without one (tests, embedded managers): anonymous-only and
// HTTPS-only, sharing one token cache.
var defaultDigestResolver = sync.OnceValue(func() *DigestResolver {
	return NewDigestResolver(nil, nil)
})

func (r *DigestResolver) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: digestResolveTimeout}
}

// Resolve returns the manifest digest of the given image reference
// (e.g. "ghcr.io/ainsel/pi:dev" → "sha256:abc123…"). namespace and podSpec
// describe where to look for pull credentials (they may be nil for
// anonymous-only resolution). Digest-pinned references (repo@sha256:…) are
// returned as-is without contacting the registry.
func (r *DigestResolver) Resolve(ctx context.Context, imageRef, namespace string, podSpec *corev1.PodSpec) (string, error) {
	// A digest-pinned ref is immutable; its digest is already known.
	if _, digest, ok := splitDigest(imageRef); ok {
		return digest, nil
	}

	registry, repository, tag := parseImageRef(imageRef)

	scheme := "https"
	if _, insecure := r.Insecure[registry]; insecure {
		scheme = "http"
	}
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, tag)

	// Anonymous (or cached-token) attempt first — the happy path for public
	// images on registries that do not challenge, and for registries whose
	// token we already hold.
	digest, challenge, err := r.manifestDigest(ctx, manifestURL, r.cachedToken(registry, repository))
	if err == nil {
		return digest, nil
	}
	if challenge == nil {
		// Definitive failure (transport error, 404, missing digest header):
		// there is no auth challenge to follow.
		return "", err
	}

	// The registry demanded authentication. Credentials are read lazily,
	// only now that a challenge actually requires them.
	creds, _ := r.credentialsFor(ctx, namespace, podSpec, registry)

	switch challenge.scheme {
	case "bearer":
		token, terr := r.fetchToken(ctx, registry, repository, challenge, creds)
		if terr != nil {
			return "", fmt.Errorf("registry auth for %s: %w", imageRef, terr)
		}
		digest, _, err := r.manifestDigest(ctx, manifestURL, "Bearer "+token)
		if err != nil {
			return "", err
		}
		return digest, nil
	case "basic":
		if creds == nil {
			return "", fmt.Errorf("registry %s requires basic authentication but no matching pull credentials were found", registry)
		}
		digest, _, err := r.manifestDigest(ctx, manifestURL, creds.header())
		if err != nil {
			return "", err
		}
		return digest, nil
	}
	return "", err
}

// manifestDigest performs one HEAD manifest request. On 401/403 it returns
// the parsed WWW-Authenticate challenge (nil when the header is absent or
// unparseable) alongside the error, so callers can decide whether to retry
// with credentials.
func (r *DigestResolver) manifestDigest(ctx context.Context, manifestURL, authHeader string) (digest string, challenge *authChallenge, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("creating manifest request: %w", err)
	}
	req.Header.Set("Accept", manifestAcceptTypes)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := r.httpClient().Do(req)
	if err != nil {
		// Transport failure (DNS, TLS handshake, refused connection):
		// terminal for this resolution — no silent scheme downgrade.
		return "", nil, fmt.Errorf("fetching manifest from %s: %w", manifestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		d := resp.Header.Get("Docker-Content-Digest")
		if d == "" {
			return "", nil, fmt.Errorf("no Docker-Content-Digest header for %s", manifestURL)
		}
		return d, nil, nil
	}

	challenge = parseAuthChallenge(resp.Header.Get("WWW-Authenticate"))
	return "", challenge, fmt.Errorf("registry returned %d for %s", resp.StatusCode, manifestURL)
}

// cachedToken returns a still-valid cached bearer token for the repository,
// or the Authorization header value to use on the first manifest attempt.
func (r *DigestResolver) cachedToken(registry, repository string) string {
	key := registry + "|" + repository
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()
	if ct, ok := r.tokens[key]; ok && time.Now().Before(ct.expiry) {
		return "Bearer " + ct.token
	}
	return ""
}

// fetchToken exchanges the bearer challenge for a token, using cached tokens
// while they are valid. Credentials (when non-nil) are sent to the auth
// realm as HTTP Basic. The token is scoped to pull only.
func (r *DigestResolver) fetchToken(ctx context.Context, registry, repository string, challenge *authChallenge, creds *registryAuth) (string, error) {
	key := registry + "|" + repository

	r.tokenMu.Lock()
	if ct, ok := r.tokens[key]; ok && time.Now().Before(ct.expiry) {
		token := ct.token
		r.tokenMu.Unlock()
		return token, nil
	}
	r.tokenMu.Unlock()

	// The realm URL comes from the registry's response — restrict it to
	// http(s) so a hostile or misconfigured registry cannot redirect the
	// operator to arbitrary schemes/hosts beyond http(s) fetches.
	realm, err := url.Parse(challenge.realm)
	if err != nil {
		return "", fmt.Errorf("invalid auth realm %q: %w", challenge.realm, err)
	}
	if realm.Scheme != "https" && realm.Scheme != "http" {
		return "", fmt.Errorf("unsupported auth realm scheme %q", realm.Scheme)
	}
	query := realm.Query()
	if challenge.service != "" {
		query.Set("service", challenge.service)
	}
	if challenge.scope != "" {
		query.Set("scope", challenge.scope)
	}
	realm.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating auth request: %w", err)
	}
	if creds != nil {
		req.Header.Set("Authorization", creds.header())
	}

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting auth realm: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth realm returned %d", resp.StatusCode)
	}

	var tokenResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding auth response: %w", err)
	}
	token := tokenResp.Token
	if token == "" {
		token = tokenResp.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("empty token in auth response")
	}

	// Cache for the token's validity window (minus a safety margin). When
	// the registry omits expires_in, assume a conservative lifetime.
	ttl := time.Duration(tokenResp.ExpiresIn) * time.Second
	if ttl <= tokenSafetyMargin {
		ttl = defaultTokenTTL
	}
	r.tokenMu.Lock()
	if r.tokens == nil {
		r.tokens = map[string]cachedToken{}
	}
	r.tokens[key] = cachedToken{token: token, expiry: time.Now().Add(ttl - tokenSafetyMargin)}
	r.tokenMu.Unlock()

	return token, nil
}

// registryAuth is a username/password pair for a registry.
type registryAuth struct {
	username string
	password string
}

func (a *registryAuth) header() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.username+":"+a.password))
}

// credentialsFor resolves pull credentials for the given registry, reading
// the pod template's imagePullSecrets or — when the template sets none — the
// imagePullSecrets of the ServiceAccount the pod would use. Returns
// (nil, nil) when no credentials exist; failures to read individual objects
// are tolerated (anonymous retry is always preferable to failing outright).
func (r *DigestResolver) credentialsFor(ctx context.Context, namespace string, podSpec *corev1.PodSpec, registry string) (*registryAuth, error) {
	if r.Client == nil || podSpec == nil || namespace == "" {
		return nil, nil
	}

	refs := podSpec.ImagePullSecrets
	if len(refs) == 0 {
		saName := podSpec.ServiceAccountName
		if saName == "" {
			saName = "default"
		}
		sa := &corev1.ServiceAccount{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: saName}, sa); err != nil {
			return nil, fmt.Errorf("reading service account %s/%s: %w", namespace, saName, err)
		}
		refs = sa.ImagePullSecrets
	}

	want := normalizeRegistryHost(registry)
	for _, ref := range refs {
		sec := &corev1.Secret{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, sec); err != nil {
			// Missing or unreadable pull secret: skip it, other entries or
			// anonymous access may still work.
			continue
		}
		auths := dockerConfigAuths(sec)
		if entry, ok := auths[want]; ok {
			if auth := entry.toRegistryAuth(); auth != nil {
				return auth, nil
			}
		}
	}
	return nil, nil
}

// dockerConfigEntry is one auths map entry in a Docker config file.
type dockerConfigEntry struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

func (e dockerConfigEntry) toRegistryAuth() *registryAuth {
	username, password := e.Username, e.Password
	if (username == "" || password == "") && e.Auth != "" {
		if decoded, err := base64.StdEncoding.DecodeString(e.Auth); err == nil {
			user, pass, ok := strings.Cut(string(decoded), ":")
			if ok {
				username, password = user, pass
			}
		}
	}
	if username == "" && password == "" {
		return nil
	}
	return &registryAuth{username: username, password: password}
}

// dockerConfigAuths extracts the auths map from a pull-secret Secret,
// supporting both kubernetes.io/dockerconfigjson and legacy
// kubernetes.io/dockercfg types. Registry keys are normalized (docker.io
// aliases collapse to registry-1.docker.io).
func dockerConfigAuths(sec *corev1.Secret) map[string]dockerConfigEntry {
	var raw []byte
	var topLevel bool
	switch sec.Type {
	case corev1.SecretTypeDockerConfigJson:
		raw = sec.Data[corev1.DockerConfigJsonKey]
	case corev1.SecretTypeDockercfg:
		raw = sec.Data[corev1.DockerConfigKey]
		topLevel = true
	default:
		// Untyped secret: try the modern key, then the legacy one.
		if data := sec.Data[corev1.DockerConfigJsonKey]; len(data) > 0 {
			raw = data
		} else {
			raw = sec.Data[corev1.DockerConfigKey]
			topLevel = len(raw) > 0
		}
	}
	if len(raw) == 0 {
		return nil
	}

	var auths map[string]dockerConfigEntry
	if topLevel {
		if err := json.Unmarshal(raw, &auths); err != nil {
			return nil
		}
	} else {
		var cfg struct {
			Auths map[string]dockerConfigEntry `json:"auths"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil
		}
		auths = cfg.Auths
	}

	normalized := make(map[string]dockerConfigEntry, len(auths))
	for host, entry := range auths {
		normalized[normalizeRegistryHost(host)] = entry
	}
	return normalized
}

// normalizeRegistryHost collapses registry host spellings to a canonical
// form: URL prefixes/suffixes (e.g. "https://index.docker.io/v1/") are
// stripped and the Docker Hub aliases all map to registry-1.docker.io.
func normalizeRegistryHost(host string) string {
	if strings.Contains(host, "://") {
		if u, err := url.Parse(host); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	host = strings.Trim(host, "/")
	switch host {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return dockerHubRegistry
	}
	return host
}

// authChallenge is a parsed WWW-Authenticate header from a registry.
type authChallenge struct {
	// scheme is lowercase: "bearer" or "basic".
	scheme  string
	realm   string
	service string
	scope   string
}

// parseAuthChallenge parses the registry's WWW-Authenticate header. Only
// Bearer (realm/service/scope parameters) and Basic challenges are
// understood; anything else returns nil.
func parseAuthChallenge(header string) *authChallenge {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	scheme, rest, _ := strings.Cut(header, " ")
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "bearer":
		params := parseAuthParams(rest)
		realm := params["realm"]
		if realm == "" {
			return nil
		}
		return &authChallenge{
			scheme:  "bearer",
			realm:   realm,
			service: params["service"],
			scope:   params["scope"],
		}
	case "basic":
		return &authChallenge{scheme: "basic"}
	}
	return nil
}

// parseAuthParams parses `key="value",key2="value2"` challenge parameters,
// tolerating quoted values with backslash escapes and unquoted values.
func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for s != "" {
		eq := strings.Index(s, "=")
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = strings.TrimSpace(s[eq+1:])

		var value string
		if strings.HasPrefix(s, `"`) {
			var sb strings.Builder
			i := 1
			for ; i < len(s); i++ {
				if s[i] == '\\' && i+1 < len(s) {
					sb.WriteByte(s[i+1])
					i++
					continue
				}
				if s[i] == '"' {
					break
				}
				sb.WriteByte(s[i])
			}
			value = sb.String()
			if i < len(s) {
				i++ // consume the closing quote
			}
			rest := s[i:]
			if comma := strings.Index(rest, ","); comma >= 0 {
				s = rest[comma+1:]
			} else {
				s = ""
			}
		} else {
			if comma := strings.Index(s, ","); comma >= 0 {
				value = s[:comma]
				s = s[comma+1:]
			} else {
				value = s
				s = ""
			}
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

// splitDigest separates a digest-pinned reference (repo@sha256:…) into the
// base reference and the digest. ok is false when ref is not digest-pinned.
func splitDigest(ref string) (base, digest string, ok bool) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:], true
	}
	return ref, "", false
}

// parseImageRef splits an image reference into registry, repository, and tag.
// Digest-pinned references are not expected here; callers should strip the
// digest first via splitDigest. Examples:
//
//	"dpinsel/ainsel-pi:dev"                -> ("registry-1.docker.io", "dpinsel/ainsel-pi", "dev")
//	"zot.platform.svc:5000/ainsel/pi:main" -> ("zot.platform.svc:5000", "ainsel/pi", "main")
//	"zot.platform.svc:5000/ainsel/pi"      -> ("zot.platform.svc:5000", "ainsel/pi", "latest")
//	"localhost:5000/foo:bar"               -> ("localhost:5000", "foo", "bar")
//	"nginx"                                -> ("registry-1.docker.io", "library/nginx", "latest")
func parseImageRef(ref string) (registry, repository, tag string) {
	registry = dockerHubRegistry
	tag = "latest"

	// Split off the tag: the last colon whose remainder has no slash. A
	// remainder containing a slash means the colon is a registry port
	// (e.g. "zot:5000/foo"), not a tag separator.
	if idx := strings.LastIndex(ref, ":"); idx > 0 {
		candidate := ref[idx+1:]
		if !strings.Contains(candidate, "/") {
			tag = candidate
			ref = ref[:idx]
		}
	}

	// The first path component is a registry hostname when it contains a
	// dot or a colon (port), or is the "localhost" alias.
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = parts[0]
		repository = parts[1]
	} else {
		repository = ref
	}

	// Docker Hub single-segment names (e.g. "nginx") live in the "library"
	// namespace; the registry API needs the fully-qualified repository path.
	if registry == dockerHubRegistry && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}

	return registry, repository, tag
}
