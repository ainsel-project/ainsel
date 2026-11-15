package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/api"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveAuthMode reads HUB_AUTH_MODE and derives it from the OIDC settings when
// unset. It is resolved before any other auth wiring because the mode decides
// whether a user registry exists at all.
//
// The derived mode is never "oidc+local": coexistence is a deliberate
// operational decision and must be requested explicitly.
func resolveAuthMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("HUB_AUTH_MODE")))
	if mode != "" {
		switch mode {
		case "oidc", "local", "oidc+local", "none":
			return mode, nil
		}
		return "", fmt.Errorf("invalid HUB_AUTH_MODE %q (want oidc, local, oidc+local, or none)", mode)
	}
	oidcCfg := oidc.FromEnvironment()
	if oidcCfg.Issuer != "" && oidcCfg.Audience != "" {
		mode = "oidc"
	} else {
		mode = "none"
	}
	slog.Info("HUB_AUTH_MODE not set, derived from environment", "mode", mode)
	return mode, nil
}

// wireAuthZ builds the authorization registry and the checker that reads it.
//
// Every mode hands the API a store and a checker — api.Deps requires both. Mode
// "none" gets authz.PermissiveStore, which answers every subject as admin and
// refuses the user/group write surface because there is no table behind it. That
// keeps the guard code path the one the configured modes run, instead of a nil
// that each handler reads as "so, am I allowed?".
//
// The first return value is the concrete Postgres registry, nil in mode "none":
// the local-auth wiring (bootstrap, disabled guard, session middleware) needs a
// real registry, and only ever runs in the modes that have one.
func wireAuthZ(ctx context.Context, pool *pgxpool.Pool, mode string) (*authz.Store, api.AuthzStore, *authz.Checker, error) {
	if mode == "none" {
		store := authz.NewPermissiveStore()
		return nil, store, authz.NewChecker(store, nil), nil
	}
	store := authz.NewStore(pool)
	// A 30s TTL bounds how long a revoked group membership can still read as
	// valid inside one request burst; it is a freshness contract, not a security
	// boundary.
	groupCache := authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return store.UserGroupRoles(ctx, uid)
	}, 30*time.Second)
	return store, store, authz.NewChecker(store, groupCache), nil
}

// authPlan is the authentication half of the wiring: the middleware chain every
// /api/v1/* request runs through, plus the settings only the auth mode decides.
//
// It exists because api.New is one step. The chain terminates in the identity
// persistence middleware, so it has to be composed before the server it hands
// identity to — and Deps requires the chain, so the server cannot be built first
// and wired afterwards.
type authPlan struct {
	// middleware is never nil. Auth mode "none" supplies a middleware that
	// injects a synthetic admin caller rather than an empty chain, so handlers
	// always have a caller to read.
	middleware func(http.Handler) http.Handler
	// authenticating is false only in mode "none", where the middleware hands
	// every caller an admin identity without checking anything. It is what
	// api.ValidateAuthConfig weighs against HUB_ALLOW_INSECURE_NO_AUTH.
	authenticating bool

	// identityTracker is shared with the server (Deps.IdentityTracker) so the
	// admin surface can invalidate a subject the chain is holding back.
	identityTracker *api.IdentityPersistTracker

	localSecret    []byte
	passwordPolicy *api.PasswordPolicy
	userInfoURL    string

	// pluginCreds is nil when HUB_PLUGIN_CREDENTIAL_SECRET is unset: plugin
	// credentials are a deployment mode, not a wiring gap.
	pluginCreds *plugincreds.Issuer

	rateLimitRPS   float64
	rateLimitBurst int
}

// wireAuthPlan resolves the already-resolved HUB_AUTH_MODE into the middleware
// chain and the settings that come with it: "oidc" (external IdP, today's
// default), "local" (hub username/password accounts, issue #108), "oidc+local"
// (both at once, issue #114), or "none" (no authentication; requires
// HUB_ALLOW_INSECURE_NO_AUTH=true).
func wireAuthPlan(ctx context.Context, c *container, cfg containerConfig) (authPlan, error) {
	oidcCfg := oidc.FromEnvironment()
	issuer, audience := oidcCfg.Issuer, oidcCfg.Audience

	// One tracker for the chain and the server: an admin clearing a username has
	// to be able to make the next request re-persist the identity, and the chain
	// is the thing holding it back.
	plan := authPlan{identityTracker: api.NewIdentityPersistTracker()}

	pluginCreds, err := wirePluginCreds()
	if err != nil {
		return authPlan{}, err
	}
	plan.pluginCreds = pluginCreds
	// The plugin credential chain sits outermost: a plugin credential is a caller
	// identity in its own right and must never reach the caller-authenticating
	// chain. It is built even without an issuer, so turning credentials on later
	// does not change the shape of the chain — it only changes what verifies.
	pluginChain := api.NewPluginCredentialChain(pluginCreds, c.apiClient, cfg.namespace, c.auditEventStore)
	// identityMW is the innermost link: it runs once the caller's identity is on
	// the request, and the server gets the same instance through Deps so an
	// invalidation from the admin surface reaches it.
	identityMW := api.NewIdentityPersistMiddleware(c.apiAuthzStore, plan.identityTracker)

	switch c.authMode {
	case "oidc":
		if issuer == "" || audience == "" {
			return authPlan{}, fmt.Errorf("auth mode oidc requires OIDC_ISSUER and OIDC_AUDIENCE (or OIDC_CLIENT_ID) to be set")
		}
		mw, userInfoURL, err := buildOIDCChain(c, oidcCfg, pluginChain, identityMW)
		if err != nil {
			return authPlan{}, err
		}
		plan.middleware, plan.userInfoURL, plan.authenticating = mw, userInfoURL, true
	case "local":
		mw, secret, policy, err := buildLocalChain(ctx, c, pluginChain, identityMW)
		if err != nil {
			return authPlan{}, err
		}
		plan.middleware, plan.localSecret, plan.passwordPolicy, plan.authenticating = mw, secret, &policy, true
	case "oidc+local":
		if issuer == "" || audience == "" {
			return authPlan{}, fmt.Errorf("auth mode oidc+local requires OIDC_ISSUER and OIDC_AUDIENCE (or OIDC_CLIENT_ID) to be set")
		}
		mw, secret, policy, userInfoURL, err := buildOIDCPlusLocalChain(ctx, c, oidcCfg, pluginChain, identityMW)
		if err != nil {
			return authPlan{}, err
		}
		plan.middleware, plan.localSecret, plan.passwordPolicy, plan.userInfoURL = mw, secret, &policy, userInfoURL
		plan.authenticating = true
	case "none":
		// No authentication: inject a synthetic admin caller so handlers that
		// require one (oidc.FromContext) keep working, and report
		// authenticating=false so the insecure-no-auth gate can do its job.
		//
		// The caller is an admin because mode "none" has no notion of a
		// non-privileged caller: the checker behind it is permissive, so
		// pretending otherwise would 403 the console while leaving every
		// endpoint open to the network.
		slog.Warn("auth mode none: API runs without authentication middleware")
		localUser := &oidc.User{Sub: "local", Email: "local@ainsel", Username: "local"}
		plan.middleware = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(oidc.ContextWithUser(r.Context(), localUser)))
			})
		}
	default:
		return authPlan{}, fmt.Errorf("invalid HUB_AUTH_MODE %q (want oidc, local, oidc+local, or none)", c.authMode)
	}

	// The gate is about authentication, not about the chain being non-empty:
	// mode "none" has a chain, and that chain is exactly what opts out of
	// checking. Passing nil says "no middleware authenticates anybody".
	if !plan.authenticating {
		if err := api.ValidateAuthConfig(nil, cfg.hubAllowInsecureNoAuth); err != nil {
			return authPlan{}, fmt.Errorf("API auth middleware not configured (override: HUB_ALLOW_INSECURE_NO_AUTH): %w", err)
		}
		slog.Error("========================================")
		slog.Error("API IS RUNNING WITHOUT AUTHENTICATION")
		slog.Error("All /api/v1/* endpoints are open to any caller.")
		slog.Error("Set HUB_AUTH_MODE=oidc, =local, or =oidc+local to enable auth.")
		slog.Error("This mode is intended for local development ONLY.")
		slog.Error("========================================",
			"override", "HUB_ALLOW_INSECURE_NO_AUTH=true",
		)
	}

	// Per-IP rate limiting. Defaults: 30 rps sustained, burst 60.
	// Override via HUB_RATE_LIMIT_RPS and HUB_RATE_LIMIT_BURST.
	plan.rateLimitRPS, plan.rateLimitBurst = 30.0, 60
	if v := os.Getenv("HUB_RATE_LIMIT_RPS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			plan.rateLimitRPS = f
		}
	}
	if v := os.Getenv("HUB_RATE_LIMIT_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			plan.rateLimitBurst = n
		}
	}
	slog.Info("Rate limiting enabled", "rps", plan.rateLimitRPS, "burst", plan.rateLimitBurst)

	return plan, nil
}

// wirePluginCreds builds the plugin credential issuer from
// HUB_PLUGIN_CREDENTIAL_SECRET. A missing or too-short secret is a supported
// deployment mode, not an error: the issuer stays nil and the plugin credential
// endpoints answer "not configured".
func wirePluginCreds() (*plugincreds.Issuer, error) {
	secretStr := os.Getenv("HUB_PLUGIN_CREDENTIAL_SECRET")
	if secretStr == "" {
		slog.Info("plugin credentials disabled (HUB_PLUGIN_CREDENTIAL_SECRET not set)")
		return nil, nil
	}
	issuer, err := plugincreds.NewIssuer([]byte(secretStr), 5*time.Minute)
	if err == plugincreds.ErrDisabled {
		slog.Info("plugin credentials disabled (secret too short)")
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("create plugin credentials issuer: %w", err)
	}
	slog.Info("plugin credentials enabled")
	return issuer, nil
}

// wireAPIServer builds the API server in one call: every dependency the hub has
// is a field of the Deps literal below, and api.New refuses the build when a
// required one is missing rather than leaving a nil for a handler to trip over.
func wireAPIServer(c *container, cfg containerConfig, plan authPlan) (*api.Server, error) {
	// Which backend answers the event metric panels is the difference between
	// two sets of figures on the same panel, and an operator reading a support
	// report needs to know which one their hub picked.
	source := wireMetricsSource(cfg.metricsSource)
	slog.Info("event metric source configured", "source", source, "prometheus", c.promClient != nil)

	// The read-only skill catalogue MCP. c.skillSvc is the concrete service,
	// which satisfies SkillDiscovery; passing it as the narrower interface keeps
	// Create/Update/Delete out of the catalogue's reach. The token is
	// deliberately not HUB_INTERNAL_VALIDATE_SECRET: see requireCatalogueToken.
	// Unset means the endpoint answers 503.
	skillsMCPToken := os.Getenv("HUB_SKILLS_MCP_TOKEN")
	if skillsMCPToken != "" {
		slog.Info("skill catalogue MCP enabled", "path", "/api/internal/skills/mcp")
	} else {
		slog.Info("skill catalogue MCP disabled (HUB_SKILLS_MCP_TOKEN not set)")
	}

	// The validate credential only. The catalogue has its own token and its own
	// line above; naming it here would tie two independent settings together and
	// let one log line lie about the other.
	internalValidateSecret := os.Getenv("HUB_INTERNAL_VALIDATE_SECRET")
	if internalValidateSecret != "" {
		slog.Info("user token validate endpoint enabled")
	} else {
		slog.Warn("HUB_INTERNAL_VALIDATE_SECRET not set, user token validate endpoint disabled")
	}

	// Extra Origins allowed to open /api/v1/ws. Same-origin is always
	// permitted, so this is only needed when the UI is served from a different
	// host or port than the hub API (local development).
	var wsAllowedOrigins []string
	if v := os.Getenv("HUB_WS_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				wsAllowedOrigins = append(wsAllowedOrigins, o)
			}
		}
		slog.Info("websocket cross-origin allowlist configured", "origins", wsAllowedOrigins)
	}

	srv, err := api.New(api.Deps{
		Client:       c.apiClient,
		Namespace:    cfg.namespace,
		ConnectorCfg: cfg.connectorCfg,

		AuthzStore:     c.apiAuthzStore,
		AuthzChecker:   c.authzChecker,
		AuthMiddleware: plan.middleware,

		Invocations:    c.invStore,
		MCP:            c.mcpSvc,
		Personas:       c.personaSvc,
		Skills:         c.skillSvc,
		SkillDiscovery: c.skillSvc,

		EventQueue:      c.eventQueue,
		Chat:            c.chatStore,
		TriggerStore:    c.triggerStore,
		ChannelSvc:      c.channels,
		ChannelTransfer: c.transfer,
		UserTokens:      c.userTokenStore,
		SignIn:          c.signInStore,
		TaskLogs:        c.taskLogStore,
		AuditEvents:     c.auditEventStore,
		Telemetry:       c.telemetryStore,

		Prom:        c.promClient,
		PluginCreds: plan.pluginCreds,

		TaskStartTimeout:       time.Duration(cfg.taskStartTimeoutSecs) * time.Second,
		SkillsMCPToken:         skillsMCPToken,
		InternalValidateSecret: internalValidateSecret,
		WSAllowedOrigins:       wsAllowedOrigins,
		MetricsSource:          source,
		RateLimitRPS:           plan.rateLimitRPS,
		RateLimitBurst:         plan.rateLimitBurst,
		LocalAuthSecret:        plan.localSecret,
		PasswordPolicy:         plan.passwordPolicy,
		UserInfoURL:            plan.userInfoURL,
		IdentityTracker:        plan.identityTracker,
	})
	if err != nil {
		return nil, fmt.Errorf("api server: %w", err)
	}
	return srv, nil
}

// buildOIDCChain builds the "oidc" chain (previous default behavior).
// Authorization is enforced in handlers via requireRead/requireWrite/
// requireManage helpers. The middleware chain only handles authentication.
func buildOIDCChain(c *container, oidcCfg oidc.Config, pluginChain *api.PluginCredentialChain, identityMW func(http.Handler) http.Handler) (func(http.Handler) http.Handler, string, error) {
	slog.Info("OIDC auth enabled", "issuer", oidcCfg.Issuer, "audience", oidcCfg.Audience)
	oidcMW, userInfoURL, err := buildOIDCMiddleware(oidcCfg)
	if err != nil {
		return nil, "", err
	}
	userTokenMW := buildUserTokenMiddleware(c)
	// The disabled guard sits inside both token middlewares so a disabled
	// user is rejected even with a still-valid OIDC access token; it is
	// required in every future chain too (e.g. a combined oidc+local mode).
	// The identity persistence middleware is the innermost handler so it
	// runs after userTokenMW and oidcMW have populated the request context
	// with the authenticated user's identity.
	disabledGuard := authz.NewDisabledGuard(c.authzStore)
	fallbackChain := func(next http.Handler) http.Handler {
		return userTokenMW(oidcMW(disabledGuard(identityMW(next))))
	}
	return pluginChain.Middleware(disabledGuard, fallbackChain), userInfoURL, nil
}

// buildLocalChain builds the local username/password chain and bootstraps the
// admin account from the environment (issue #108). It returns the signing secret
// and the password policy so the caller can hand them to the server as Deps.
func buildLocalChain(ctx context.Context, c *container, pluginChain *api.PluginCredentialChain, identityMW func(http.Handler) http.Handler) (func(http.Handler) http.Handler, []byte, api.PasswordPolicy, error) {
	var policy api.PasswordPolicy
	secretStr := os.Getenv("HUB_LOCAL_JWT_SECRET")
	if secretStr == "" {
		return nil, nil, policy, fmt.Errorf("auth mode local requires HUB_LOCAL_JWT_SECRET to be set")
	}
	policy, err := passwordPolicyFromEnv()
	if err != nil {
		return nil, nil, policy, err
	}
	if err := bootstrapLocalAdmin(ctx, c.authzStore, policy); err != nil {
		return nil, nil, policy, fmt.Errorf("bootstrap local admin: %w", err)
	}
	secret := []byte(secretStr)

	localMW := localauth.NewMiddleware(secret, c.authzStore)
	userTokenMW := buildUserTokenMiddleware(c)
	// The disabled guard sits inside both token middlewares (required in any
	// future combined chain too): local session JWTs are verified statelessly,
	// so this is what makes disabling take effect immediately.
	disabledGuard := authz.NewDisabledGuard(c.authzStore)
	fallbackChain := func(next http.Handler) http.Handler {
		// RequireAuth sits inside both token middlewares: anonymous requests
		// fall through the token checks and are rejected there, mirroring how
		// the OIDC middleware rejects missing bearer tokens.
		return userTokenMW(localMW(localauth.RequireAuth(disabledGuard(identityMW(next)))))
	}
	slog.Info("local auth enabled", "loginEndpoint", "/api/v1/auth/login",
		"passwordMinLength", policy.MinLength, "passwordCharacterClasses", policy.RequireClasses)
	return pluginChain.Middleware(disabledGuard, fallbackChain), secret, policy, nil
}

// buildOIDCPlusLocalChain builds the coexistence chain for auth mode
// "oidc+local" (issue #114): local session JWTs are verified by the local
// layer and never reach the OIDC middleware; everything else (OIDC access
// tokens and anonymous requests) is handled by the OIDC middleware, which
// supplies the anonymous rejection — so no localauth.RequireAuth closer is
// needed in this mode. Both halves are required: a half-configured
// coexistence install must fail startup, not silently drop the break-glass
// path or the SSO path.
func buildOIDCPlusLocalChain(ctx context.Context, c *container, oidcCfg oidc.Config, pluginChain *api.PluginCredentialChain, identityMW func(http.Handler) http.Handler) (mw func(http.Handler) http.Handler, secret []byte, policy api.PasswordPolicy, userInfoURL string, err error) {
	secretStr := os.Getenv("HUB_LOCAL_JWT_SECRET")
	if secretStr == "" {
		return nil, nil, policy, "", fmt.Errorf("auth mode oidc+local requires HUB_LOCAL_JWT_SECRET to be set")
	}
	oidcMW, userInfoURL, err := buildOIDCMiddleware(oidcCfg)
	if err != nil {
		return nil, nil, policy, "", err
	}
	policy, err = passwordPolicyFromEnv()
	if err != nil {
		return nil, nil, policy, "", err
	}
	if err := bootstrapLocalAdmin(ctx, c.authzStore, policy); err != nil {
		return nil, nil, policy, "", fmt.Errorf("bootstrap local admin: %w", err)
	}
	secret = []byte(secretStr)

	chainMW := localauth.NewChainMiddleware(secret, c.authzStore, oidcMW)
	userTokenMW := buildUserTokenMiddleware(c)
	// Same innermost-guard rule as the single-mode chains: both halves of
	// chainMW verify tokens statelessly, so the disabled check must run
	// inside chainMW and outside the identity middleware.
	disabledGuard := authz.NewDisabledGuard(c.authzStore)
	fallbackChain := func(next http.Handler) http.Handler {
		return userTokenMW(chainMW(disabledGuard(identityMW(next))))
	}
	slog.Info("oidc+local auth enabled", "issuer", oidcCfg.Issuer, "audience", oidcCfg.Audience, "loginEndpoint", "/api/v1/auth/login",
		"passwordMinLength", policy.MinLength, "passwordCharacterClasses", policy.RequireClasses)
	return pluginChain.Middleware(disabledGuard, fallbackChain), secret, policy, userInfoURL, nil
}
