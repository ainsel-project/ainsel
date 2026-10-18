package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantReg  string
		wantRepo string
		wantTag  string
	}{
		{
			name:     "docker hub namespaced with tag",
			ref:      "dpinsel/ainsel-pi:dev",
			wantReg:  "registry-1.docker.io",
			wantRepo: "dpinsel/ainsel-pi",
			wantTag:  "dev",
		},
		{
			name:     "internal registry with port and tag",
			ref:      "zot.platform.svc:5000/ainsel/pi:main",
			wantReg:  "zot.platform.svc:5000",
			wantRepo: "ainsel/pi",
			wantTag:  "main",
		},
		{
			name:     "internal registry with port, no tag defaults to latest",
			ref:      "zot.platform.svc:5000/ainsel/pi",
			wantReg:  "zot.platform.svc:5000",
			wantRepo: "ainsel/pi",
			wantTag:  "latest",
		},
		{
			name:     "localhost registry with port and tag",
			ref:      "localhost:5000/foo:bar",
			wantReg:  "localhost:5000",
			wantRepo: "foo",
			wantTag:  "bar",
		},
		{
			name:     "single-segment docker hub defaults to library and latest",
			ref:      "nginx",
			wantReg:  "registry-1.docker.io",
			wantRepo: "library/nginx",
			wantTag:  "latest",
		},
		{
			name:     "single-segment docker hub with tag",
			ref:      "nginx:1.27",
			wantReg:  "registry-1.docker.io",
			wantRepo: "library/nginx",
			wantTag:  "1.27",
		},
		{
			name:     "namespaced docker hub without tag defaults to latest",
			ref:      "dpinsel/ainsel-pi",
			wantReg:  "registry-1.docker.io",
			wantRepo: "dpinsel/ainsel-pi",
			wantTag:  "latest",
		},
		{
			name:     "registry with hostname and nested repo",
			ref:      "ghcr.io/ainsel/pi:dev",
			wantReg:  "ghcr.io",
			wantRepo: "ainsel/pi",
			wantTag:  "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, repo, tag := parseImageRef(tt.ref)
			if reg != tt.wantReg {
				t.Errorf("registry = %q, want %q", reg, tt.wantReg)
			}
			if repo != tt.wantRepo {
				t.Errorf("repository = %q, want %q", repo, tt.wantRepo)
			}
			if tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", tag, tt.wantTag)
			}
		})
	}
}

func TestSplitDigest(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantBase   string
		wantDigest string
		wantOK     bool
	}{
		{
			name:       "digest-pinned ref",
			ref:        "dpinsel/ainsel-pi@sha256:cec864ebec545abcdef",
			wantBase:   "dpinsel/ainsel-pi",
			wantDigest: "sha256:cec864ebec545abcdef",
			wantOK:     true,
		},
		{
			name:       "tag ref has no digest",
			ref:        "dpinsel/ainsel-pi:dev",
			wantBase:   "dpinsel/ainsel-pi:dev",
			wantDigest: "",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, digest, ok := splitDigest(tt.ref)
			if base != tt.wantBase || digest != tt.wantDigest || ok != tt.wantOK {
				t.Errorf("splitDigest(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.ref, base, digest, ok, tt.wantBase, tt.wantDigest, tt.wantOK)
			}
		})
	}
}

// newTestResolver builds a resolver pointed at a plain-HTTP test server: the
// server host is registered as an insecure registry, which is the only way to
// reach an httptest server now that automatic HTTP fallback is gone.
func newTestResolver(insecureHosts ...string) *DigestResolver {
	r := NewDigestResolver(nil, insecureHosts)
	r.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	return r
}

// hostFromURL converts an httptest server URL into the registry host form
// used in image references.
func hostFromURL(t *testing.T, srvURL string) string {
	t.Helper()
	host, ok := strings.CutPrefix(srvURL, "http://")
	if !ok {
		t.Fatalf("unexpected test server URL %q", srvURL)
	}
	return host
}

func TestResolve_DigestPinnedSkipsNetwork(t *testing.T) {
	// No server: a digest-pinned ref must resolve without any network call.
	const digest = "sha256:cec864ebec545abcdef"
	got, err := newTestResolver().Resolve(context.Background(), "dpinsel/ainsel-pi@"+digest, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != digest {
		t.Errorf("digest = %q, want %q", got, digest)
	}
}

func TestResolve_InsecureRegistryAnonymousOK(t *testing.T) {
	const wantDigest = "sha256:deadbeefcafe"
	var gotAccept, gotPath, gotMethod, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Docker-Content-Digest", wantDigest)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	r := newTestResolver(registry)

	got, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}
	if gotMethod != http.MethodHead {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodHead)
	}
	if gotPath != "/v2/ainsel/pi/manifests/dev" {
		t.Errorf("path = %q, want %q", gotPath, "/v2/ainsel/pi/manifests/dev")
	}
	if gotAuth != "" {
		t.Errorf("anonymous request must not carry Authorization, got %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "application/vnd.oci.image.manifest.v1+json") {
		t.Errorf("Accept header missing OCI manifest type: %q", gotAccept)
	}
	if !strings.Contains(gotAccept, "application/vnd.docker.distribution.manifest.v2+json") {
		t.Errorf("Accept header missing Docker manifest type: %q", gotAccept)
	}
}

// A registry that is not in the insecure list must never be contacted over
// plain HTTP: the HTTPS-only attempt fails at the transport layer and the
// resolution errors out instead of silently downgrading.
func TestResolve_HTTPSOnlyWhenNotListedInsecure(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Docker-Content-Digest", "sha256:shouldnotreach")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	r := newTestResolver() // registry deliberately NOT in the insecure list

	_, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
	if err == nil {
		t.Fatal("expected error when registry is HTTPS-only but serves plain HTTP, got nil")
	}
	if hits != 0 {
		t.Errorf("plain-HTTP server must not be reached, got %d requests", hits)
	}
}

// The core GHCR-style flow: anonymous HEAD gets 401 + Bearer challenge, the
// realm is exchanged for a token, and the manifest request is retried once
// with the bearer token.
func TestResolve_BearerChallenge(t *testing.T) {
	const wantDigest = "sha256:cafebabecafe"
	const wantToken = "test-bearer-token"

	var mu sync.Mutex
	var tokenHits int
	var manifestAuths []string
	var tokenService, tokenScope string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			auth := r.Header.Get("Authorization")
			mu.Lock()
			manifestAuths = append(manifestAuths, auth)
			mu.Unlock()
			if auth != "Bearer "+wantToken {
				w.Header().Set("WWW-Authenticate",
					fmt.Sprintf(`Bearer realm=%q,service="registry.test",scope="repository:ainsel/pi:pull"`, srv.URL+"/token"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", wantDigest)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/token":
			mu.Lock()
			tokenHits++
			tokenService = r.URL.Query().Get("service")
			tokenScope = r.URL.Query().Get("scope")
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      wantToken,
				"expires_in": 300,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	r := newTestResolver(registry)

	got, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}
	if tokenService != "registry.test" {
		t.Errorf("token service = %q, want %q", tokenService, "registry.test")
	}
	if tokenScope != "repository:ainsel/pi:pull" {
		t.Errorf("token scope = %q, want %q", tokenScope, "repository:ainsel/pi:pull")
	}
	mu.Lock()
	seqOK := len(manifestAuths) == 2 && manifestAuths[0] == "" && manifestAuths[1] == "Bearer "+wantToken
	tokenHitsAfterFirst := tokenHits
	mu.Unlock()
	if !seqOK {
		t.Errorf("manifest auth sequence = %q, want [\"\", %q)", manifestAuths, "Bearer "+wantToken)
	}

	// A second resolution must reuse the cached token: no extra realm hit
	// and a single manifest request, already authorized.
	if _, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenHits != tokenHitsAfterFirst {
		t.Errorf("token endpoint hit %d more times, want 0 (cache)", tokenHits-tokenHitsAfterFirst)
	}
	if manifestAuths[len(manifestAuths)-1] != "Bearer "+wantToken {
		t.Errorf("cached-token attempt missing bearer: %q", manifestAuths[len(manifestAuths)-1])
	}
}

// The token realm receives Basic credentials from the pod's effective pull
// secrets, and credentials are only read when a challenge requires them.
func TestResolve_BearerChallengeWithPullSecrets(t *testing.T) {
	const wantDigest = "sha256:1122334455"
	const wantToken = "credentialed-token"

	var mu sync.Mutex
	var tokenAuth string
	var manifestAuths []string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			auth := r.Header.Get("Authorization")
			mu.Lock()
			manifestAuths = append(manifestAuths, auth)
			mu.Unlock()
			if auth != "Bearer "+wantToken {
				w.Header().Set("WWW-Authenticate",
					fmt.Sprintf(`Bearer realm=%q,service="registry.test",scope="repository:ainsel/pi:pull"`, srv.URL+"/token"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", wantDigest)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/token":
			mu.Lock()
			tokenAuth = r.Header.Get("Authorization")
			mu.Unlock()
			if tokenAuth != basicHeader("puller", "s3cret") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": wantToken})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)

	cfg := fmt.Sprintf(`{"auths":{%q:{"username":"puller","password":"s3cret"}}}`, registry)
	k8s := fakeClientWith(t,
		&corev1.ServiceAccount{
			ObjectMeta:       metav1.ObjectMeta{Namespace: "agents", Name: "agent-sa"},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "reg-creds"}},
		},
		dockerSecret("agents", "reg-creds", cfg),
	)

	r := newTestResolver(registry)
	r.Client = k8s

	podSpec := &corev1.PodSpec{ServiceAccountName: "agent-sa"}
	got, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "agents", podSpec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}
	if tokenAuth != basicHeader("puller", "s3cret") {
		t.Errorf("token endpoint Authorization = %q, want basic credentials", tokenAuth)
	}
}

// Credentials come from the ServiceAccount's imagePullSecrets when the pod
// template sets none, and the legacy .dockercfg format is understood.
func TestResolve_PullSecretsViaServiceAccountLegacyDockercfg(t *testing.T) {
	const wantDigest = "sha256:legacyok"
	const wantToken = "legacy-token"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.Header.Get("Authorization") != basicHeader("legacy", "pw") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": wantToken})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q`, srv.URL+"/token"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", wantDigest)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)

	// Legacy dockercfg: top-level map, auth field base64("legacy:pw").
	cfg := fmt.Sprintf(`{%q:{"auth":%q}}`, "https://"+registry+"/",
		base64.StdEncoding.EncodeToString([]byte("legacy:pw")))
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "legacy-creds"},
		Type:       corev1.SecretTypeDockercfg,
		Data:       map[string][]byte{corev1.DockerConfigKey: []byte(cfg)},
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "default"},
		ImagePullSecrets: []corev1.LocalObjectReference{
			{Name: "legacy-creds"},
		},
	}

	r := newTestResolver(registry)
	r.Client = fakeClientWith(t, sa, sec)

	// Empty pod spec: resolver must fall back to the "default" SA.
	got, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "agents", &corev1.PodSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}
}

// Anonymous success must never read ServiceAccounts or Secrets.
func TestResolve_CredentialsAreLazy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:anon")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)

	var gets int
	objs := []client.Object{&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "default"},
	}}
	r := newTestResolver(registry)
	r.Client = fake.NewClientBuilder().
		WithScheme(controllerScheme()).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				gets++
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	if _, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "agents", &corev1.PodSpec{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gets != 0 {
		t.Errorf("anonymous resolution performed %d API reads, want 0", gets)
	}
}

// A 401 without a usable challenge header is a clean terminal error.
func TestResolve_ChallengeMissingOrMalformed(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantSub string
	}{
		{"no header", "", "401"},
		{"malformed header", "Bearer not-a-realm", "401"},
		{"unknown scheme", "Negotiate abc", "401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.header != "" {
					w.Header().Set("WWW-Authenticate", tt.header)
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer srv.Close()

			registry := hostFromURL(t, srv.URL)
			r := newTestResolver(registry)
			_, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q should mention %q", err, tt.wantSub)
			}
		})
	}
}

// A Basic challenge is answered with pull credentials; without credentials it
// fails cleanly instead of looping.
func TestResolve_BasicChallenge(t *testing.T) {
	const wantDigest = "sha256:basicflow"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != basicHeader("bob", "pw") {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", wantDigest)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	cfg := fmt.Sprintf(`{"auths":{%q:{"username":"bob","password":"pw"}}}`, registry)
	k8s := fakeClientWith(t,
		&corev1.ServiceAccount{
			ObjectMeta:       metav1.ObjectMeta{Namespace: "agents", Name: "default"},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "creds"}},
		},
		dockerSecret("agents", "creds", cfg),
	)

	r := newTestResolver(registry)
	r.Client = k8s
	got, err := r.Resolve(context.Background(), registry+"/ainsel/pi:dev", "agents", &corev1.PodSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}

	// Without credentials the same registry fails cleanly.
	rNoCreds := newTestResolver(registry)
	if _, err := rNoCreds.Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil); err == nil {
		t.Error("expected error for basic challenge without credentials, got nil")
	}
}

func TestResolve_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	_, err := newTestResolver(registry).Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
	if err == nil {
		t.Fatal("expected error for non-OK status, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestResolve_MissingDigestHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 OK but no Docker-Content-Digest header.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registry := hostFromURL(t, srv.URL)
	_, err := newTestResolver(registry).Resolve(context.Background(), registry+"/ainsel/pi:dev", "", nil)
	if err == nil {
		t.Fatal("expected error for missing digest header, got nil")
	}
	if !strings.Contains(err.Error(), "Docker-Content-Digest") {
		t.Errorf("error should mention missing header: %v", err)
	}
}

// A hostile registry must not be able to point the token exchange at a
// non-http(s) scheme.
func TestFetchToken_RejectsNonHTTPRealmScheme(t *testing.T) {
	ch := &authChallenge{scheme: "bearer", realm: "file:///etc/passwd"}
	_, err := newTestResolver().fetchToken(context.Background(), "reg.test", "a/b", ch, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported auth realm scheme") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
}

func TestParseAuthChallenge(t *testing.T) {
	ch := parseAuthChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:ainsel/pi:pull"`)
	if ch == nil {
		t.Fatal("expected challenge")
	}
	if ch.scheme != "bearer" || ch.realm != "https://ghcr.io/token" || ch.service != "ghcr.io" || ch.scope != "repository:ainsel/pi:pull" {
		t.Errorf("unexpected challenge: %+v", ch)
	}

	if ch := parseAuthChallenge("Basic realm=\"x\""); ch == nil || ch.scheme != "basic" {
		t.Errorf("basic challenge not parsed: %+v", ch)
	}
	if ch := parseAuthChallenge(""); ch != nil {
		t.Errorf("empty header should yield nil, got %+v", ch)
	}
	if ch := parseAuthChallenge("Bearer service=\"no-realm\""); ch != nil {
		t.Errorf("bearer without realm should yield nil, got %+v", ch)
	}
	// Quoted value containing a comma.
	ch = parseAuthChallenge(`Bearer realm="https://r.test/token",scope="a,b"`)
	if ch == nil || ch.scope != "a,b" {
		t.Errorf("comma in quoted value not handled: %+v", ch)
	}
}

func TestNormalizeRegistryHost(t *testing.T) {
	for in, want := range map[string]string{
		"docker.io":                   "registry-1.docker.io",
		"index.docker.io":             "registry-1.docker.io",
		"registry-1.docker.io":        "registry-1.docker.io",
		"https://index.docker.io/v1/": "registry-1.docker.io",
		"ghcr.io":                     "ghcr.io",
		"https://ghcr.io/":            "ghcr.io",
		"zot.platform.svc:5000":       "zot.platform.svc:5000",
	} {
		if got := normalizeRegistryHost(in); got != want {
			t.Errorf("normalizeRegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDockerConfigAuths(t *testing.T) {
	// dockerconfigjson with docker.io alias and auth-field credentials.
	cfg := `{"auths":{"https://index.docker.io/v1/":{"auth":"` +
		base64.StdEncoding.EncodeToString([]byte("user:pass")) + `"}}}`
	sec := &corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)},
	}
	auths := dockerConfigAuths(sec)
	entry, ok := auths["registry-1.docker.io"]
	if !ok {
		t.Fatalf("docker.io alias not normalized: %v", auths)
	}
	auth := entry.toRegistryAuth()
	if auth == nil || auth.username != "user" || auth.password != "pass" {
		t.Errorf("auth-field decoding failed: %+v", auth)
	}

	// Garbage payload must not panic or produce entries.
	bad := &corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("not json")},
	}
	if got := dockerConfigAuths(bad); got != nil {
		t.Errorf("expected nil for malformed config, got %v", got)
	}
}

// --- helpers ---

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func dockerSecret(namespace, name, configJSON string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(configJSON)},
	}
}

func controllerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

func fakeClientWith(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(controllerScheme()).WithObjects(objs...).Build()
}
