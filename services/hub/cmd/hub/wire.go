package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/api"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginsync"
	"github.com/ainsel-project/ainsel/services/hub/internal/prometheus"
	"github.com/ainsel-project/ainsel/services/hub/internal/selfsync"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	"github.com/ainsel-project/ainsel/services/hub/internal/usertokens"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ctrl "sigs.k8s.io/controller-runtime"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// wireDB runs migrations and opens the database pool.
func wireDB(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	if dbURL == "" {
		return nil, fmt.Errorf("HUB_DB_URL is required")
	}
	if err := db.Migrate(ctx, dbURL); err != nil {
		return nil, fmt.Errorf("db migrate: %w", err)
	}
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	return pool, nil
}

// wireK8sManager creates the controller-runtime manager.
func wireK8sManager(namespace string) (ctrl.Manager, error) {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache: crcache.Options{
			DefaultNamespaces: map[string]crcache.Config{namespace: {}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create manager: %w", err)
	}
	return mgr, nil
}

// wireAPIClient creates the Kubernetes API client from the manager.
func wireAPIClient(mgr ctrl.Manager) (client.Client, error) {
	ac, err := client.New(mgr.GetConfig(), client.Options{
		Scheme:     mgr.GetScheme(),
		Mapper:     mgr.GetRESTMapper(),
		HTTPClient: mgr.GetHTTPClient(),
	})
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return ac, nil
}

// wirePrometheus creates the Prometheus client if URL is set.
func wirePrometheus(promURL string) *prometheus.Client {
	if promURL == "" {
		slog.Warn("HUB_PROMETHEUS_URL not set: raw PromQL is unavailable; token tiles, the by-subject table and event metrics come from the hub's own records")
		return nil
	}
	slog.Info("prometheus client configured", "url", promURL)
	return prometheus.NewClient(promURL, nil)
}

// wireMCP creates the MCP servers service.
func wireMCP(pool *pgxpool.Pool) *mcpservers.Service {
	store := mcpservers.NewStore(pool)
	return mcpservers.NewService(store)
}

// pluginSyncInterval is how often the plugin registry syncer reconciles its
// rows. Same cadence as the trigger sync loop: a plugin installed or removed
// shows up in the MCP registry within one tick, which is quick enough for an
// operator and cheap enough to do by listing rather than watching.
const pluginSyncInterval = 30 * time.Second

// agentMCPEnabledEnv opts a cluster into the platform read plane. One flag
// gates both halves of the feature on purpose: the endpoint and the registry row
// that points agents at it. A row for a plane that answers 503 would be worse
// than no row — an agent would attach a server that refuses it and read that as
// a broken platform.
const agentMCPEnabledEnv = "HUB_AGENT_MCP_ENABLED"

// agentMCPEnabled reports whether the platform read plane is opted in.
func agentMCPEnabled() bool {
	return os.Getenv(agentMCPEnabledEnv) == "true"
}

// wireSelfSync builds the syncer that registers the hub's own agent-facing MCP
// endpoint in the registry, or nil when it cannot do its job. It is the sibling
// of wirePluginSync and shares its requirements: HUB_SELF_URL, because the row
// is the hub's own address plus a path, and the plane being enabled, because a
// registered endpoint that refuses its callers is a trap rather than a feature.
func wireSelfSync(c *container) *selfsync.Syncer {
	if !agentMCPEnabled() {
		return nil
	}
	selfURL := os.Getenv("HUB_SELF_URL")
	if selfURL == "" {
		slog.Info("platform registry sync disabled: HUB_SELF_URL not set")
		return nil
	}
	s := selfsync.New(c.mcpSvc, selfURL)
	s.SetResourceVisibility(c.authzStore)
	slog.Info("platform registry sync enabled", "selfURL", selfURL, "interval", pluginSyncInterval)
	return s
}

// runSelfSyncLoop is the background loop main.go starts. A disabled plane is one
// log line at most and no work, so the loop never has to know why.
func runSelfSyncLoop(ctx context.Context, c *container) error {
	s := wireSelfSync(c)
	if s == nil {
		return nil
	}
	s.Run(ctx, pluginSyncInterval)
	return nil
}

// wirePluginSync builds the syncer that keeps plugin MCP servers registered for
// agents and plugin skills registered in the skill registry, or nil when it
// cannot do its job. HUB_SELF_URL is required: it is the hub's own in-cluster
// address and the base of every URL the syncer writes, so without it the rows
// would point agents at an address nobody told the hub.
//
// The skill halves are wired together: the registry adapter to write rows
// through and the HTTP fetcher to read a skill document off the plugin pod.
// The fetcher's client carries the 10 s timeout — a plugin that does not
// answer a skill GET fast is not worth stalling a sync pass for.
func wirePluginSync(c *container) *pluginsync.Syncer {
	selfURL := os.Getenv("HUB_SELF_URL")
	if selfURL == "" {
		slog.Info("plugin registry sync disabled: HUB_SELF_URL not set")
		return nil
	}
	fetcher := pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 10 * time.Second})
	s := pluginsync.New(c.apiClient, c.namespace, c.mcpSvc, pluginSkillRegistry{svc: c.skillSvc}, selfURL, fetcher)
	s.SetResourceVisibility(c.apiAuthzStore)
	slog.Info("plugin registry sync enabled", "selfURL", selfURL, "interval", pluginSyncInterval)
	return s
}

// pluginSkillRegistry adapts the skills service to the syncer's SkillRegistry
// seam: the syncer stays free of the skill registry's own types, and the skill
// package stays free of the syncer.
type pluginSkillRegistry struct {
	svc *skills.Service
}

func (r pluginSkillRegistry) ListManaged(ctx context.Context) ([]pluginsync.ManagedSkill, error) {
	rows, err := r.svc.ListManaged(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pluginsync.ManagedSkill, 0, len(rows))
	for _, sk := range rows {
		out = append(out, pluginsync.ManagedSkill{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Body:        sk.Body,
			Tags:        sk.Tags,
		})
	}
	return out, nil
}

func (r pluginSkillRegistry) Upsert(ctx context.Context, s pluginsync.ManagedSkill) error {
	return r.svc.UpsertManaged(ctx, &skills.Skill{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Body:        s.Body,
		Tags:        s.Tags,
	})
}

func (r pluginSkillRegistry) Delete(ctx context.Context, id string) error {
	return r.svc.DeleteManaged(ctx, id)
}

// runPluginSyncLoop is the background loop main.go starts. A disabled syncer is
// one log line and no work, so the loop itself never has to know why.
func runPluginSyncLoop(ctx context.Context, c *container) error {
	s := wirePluginSync(c)
	if s == nil {
		return nil
	}
	s.Run(ctx, pluginSyncInterval)
	return nil
}

// wirePersonas creates the personas service.
func wirePersonas(pool *pgxpool.Pool, apiClient client.Client, namespace string) *personas.Service {
	store := personas.NewStore(pool)
	rec := personas.NewReconciler(apiClient, namespace)
	lister := personas.NewAgentLister(apiClient, namespace)
	return personas.NewService(store, rec, lister)
}

// wireSkills creates the skills service.
func wireSkills(pool *pgxpool.Pool, apiClient client.Client, namespace string) *skills.Service {
	store := skills.NewStore(pool)
	rec := skills.NewReconciler(apiClient, namespace)
	lister := skills.NewAgentLister(apiClient, namespace)
	return skills.NewService(store, rec, lister)
}

// buildOIDCMiddleware resolves the OIDC endpoints via discovery and builds
// the verification middleware. Shared by the "oidc" and "oidc+local" modes
// so the subtle discovery + escape-hatch logic stays identical in both.
func buildOIDCMiddleware(oidcCfg oidc.Config) (func(http.Handler) http.Handler, string, error) {
	// Resolve JWKS/userinfo endpoints from the issuer's OIDC discovery
	// document. Hardcoding provider-specific paths broke username/email
	// enrichment on Zitadel versions whose userinfo endpoint lives at
	// /oidc/v1/userinfo instead of /oauth/v2/userinfo. There is no
	// fallback: a broken issuer must fail startup visibly.
	discoveryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	jwksURL, userInfoURL, derr := oidc.ResolveEndpoints(discoveryCtx, nil, oidcCfg.Issuer)
	cancel()
	if derr != nil {
		return nil, "", fmt.Errorf("oidc endpoint resolution: %w", derr)
	}
	slog.Info("OIDC endpoints resolved via discovery", "jwks", jwksURL, "userinfo", userInfoURL)
	// Escape hatches: allow operators to pin endpoints explicitly.
	if v := os.Getenv("OIDC_JWKS_URL"); v != "" {
		jwksURL = v
	}
	if v := os.Getenv("OIDC_USERINFO_URL"); v != "" {
		userInfoURL = v
	}
	oidcCfg.JWKSURL = jwksURL
	oidcCfg.UserInfoURL = userInfoURL
	oidcMW, err := oidc.NewMiddleware(oidcCfg)
	if err != nil {
		return nil, "", fmt.Errorf("auth middleware setup: %w", err)
	}
	return oidcMW, userInfoURL, nil
}

// buildUserTokenMiddleware builds the user-token (API token) middleware used
// by every authenticated mode.
func buildUserTokenMiddleware(c *container) func(http.Handler) http.Handler {
	return usertokens.NewMiddleware(c.userTokenStore, func(ctx context.Context, userID string) (string, error) {
		u, err := c.authzStore.GetUser(ctx, userID)
		if err != nil {
			return "", err
		}
		return u.Username, nil
	})
}

// passwordPolicyFromEnv reads the HUB_LOCAL_PASSWORD_* overrides for the
// password policy. Invalid values fail startup naming the offending
// variable — same fail-fast posture as the rest of wireAuthMiddleware, so a
// typo'd class name becomes a deploy-time error instead of a silently
// weakened policy. Unset variables keep the historical defaults (min 8,
// max 128 hard-coded, no character classes).
func passwordPolicyFromEnv() (api.PasswordPolicy, error) {
	p := api.DefaultPasswordPolicy()
	if v := os.Getenv("HUB_LOCAL_PASSWORD_MIN_LENGTH"); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return p, fmt.Errorf("invalid HUB_LOCAL_PASSWORD_MIN_LENGTH %q: want an integer", v)
		}
		if n < 1 || n > api.MaxPasswordLen {
			return p, fmt.Errorf("invalid HUB_LOCAL_PASSWORD_MIN_LENGTH %d: want 1..%d", n, api.MaxPasswordLen)
		}
		p.MinLength = n
	}
	if v := os.Getenv("HUB_LOCAL_PASSWORD_CHARACTER_CLASSES"); v != "" {
		classes, err := api.ParsePasswordCharacterClasses(v)
		if err != nil {
			return p, fmt.Errorf("invalid HUB_LOCAL_PASSWORD_CHARACTER_CLASSES %q: %w", v, err)
		}
		p.RequireClasses = classes
	}
	return p, nil
}

// bootstrapLocalAdmin creates the initial admin account on first start in
// local mode. HUB_LOCAL_ADMIN_USER defaults to "admin"; the password comes
// from HUB_LOCAL_ADMIN_PASSWORD and is only used when the account does not
// exist yet — restarts never silently change an existing password. The
// bootstrap password is checked against the configured policy so a strict
// policy cannot be bypassed by the very first account.
//
// The bootstrapped password is issued rather than chosen by the account owner
// — chart-generated in a Kubernetes Secret, or supplied by the operator via
// auth.local.adminPasswordSecret — so the account is created with the
// forced-change flag set (issue #113): the first login has to replace it
// before the console is usable.
func bootstrapLocalAdmin(ctx context.Context, store *authz.Store, policy api.PasswordPolicy) error {
	adminUser := os.Getenv("HUB_LOCAL_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPass := os.Getenv("HUB_LOCAL_ADMIN_PASSWORD")

	bctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	id := authz.LocalUserIDPrefix + adminUser
	_, err := store.GetUser(bctx, id)
	if err == nil {
		slog.Info("bootstrap admin already exists", "id", id)
		return nil
	}
	if !errors.Is(err, authz.ErrNotFound) {
		return err
	}
	if adminPass == "" {
		return fmt.Errorf("admin user %q does not exist and HUB_LOCAL_ADMIN_PASSWORD is empty", id)
	}
	if err := policy.Validate(adminPass); err != nil {
		return fmt.Errorf("HUB_LOCAL_ADMIN_PASSWORD: %w", err)
	}
	hash, err := localauth.HashPassword(adminPass)
	if err != nil {
		return err
	}
	if _, err := store.CreateLocalUser(bctx, id, "", adminUser, hash, true, true); err != nil {
		if errors.Is(err, authz.ErrAlreadyExists) {
			return nil
		}
		return err
	}
	slog.Info("bootstrap admin created", "id", id)
	return nil
}

// wireMetricsServer creates the Prometheus metrics HTTP server.
func wireMetricsServer(metricsPort string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return &http.Server{
		Addr:              ":" + metricsPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// wireHTTPServer creates an HTTP server for the given port and handler.
func wireHTTPServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// runUntilCanceled wraps a blocking function so that context.Canceled
// is converted to nil. Use for components expected to end via cancellation.
func runUntilCanceled(ctx context.Context, fn func(context.Context) error) func() error {
	return func() error {
		err := fn(ctx)
		if err == context.Canceled {
			return nil
		}
		return err
	}
}

// wireMetricsSource normalises HUB_METRICS_SOURCE into the token api.Server
// expects. An unrecognised value falls back to the default with a warning
// instead of failing startup: this setting chooses between two backends that
// both work, so a typo should cost an operator a log line, not a pod.
func wireMetricsSource(raw string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "", api.MetricsSourceRecords:
		return api.MetricsSourceRecords
	case api.MetricsSourcePrometheus:
		return api.MetricsSourcePrometheus
	default:
		slog.Warn("unknown HUB_METRICS_SOURCE, ignoring it",
			"value", raw, "using", api.MetricsSourceRecords)
		return api.MetricsSourceRecords
	}
}

// envOrDefault returns the env var value or a default.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envIntOrDefault returns the env var value as a positive int or a default.
func envIntOrDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
	return n
}
