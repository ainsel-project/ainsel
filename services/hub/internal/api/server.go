// Package api implements the hub's HTTP API. Server wires authentication,
// authorization and routes for all platform resources — agents, channels,
// triggers, plugins, events, observability and more — backed by the Kubernetes
// API and the hub's Postgres stores.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/services/hub/internal/prometheus"
	"github.com/ainsel-project/ainsel/services/hub/internal/signin"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	"github.com/ainsel-project/ainsel/services/hub/internal/telemetry"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
	"github.com/ainsel-project/ainsel/services/hub/internal/usertokens"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AuthzStore is the authorization store surface the API depends on: the user
// registry, the group registry and the resource-group registry. Satisfied by
// *authz.Store (Postgres), by authz.PermissiveStore (auth mode "none") and by
// test doubles.
//
// A Server always has one: Deps validates it, so no handler branches on
// "is authorization wired".
type AuthzStore interface {
	UpsertUser(ctx context.Context, sub, email, username string) (*authz.User, error)
	GetUser(ctx context.Context, id string) (*authz.User, error)
	ListUsers(ctx context.Context) ([]authz.User, error)
	SetAdmin(ctx context.Context, userID string, isAdmin bool) error
	SetDisabled(ctx context.Context, userID string, disabled bool) error
	ClearUsername(ctx context.Context, id string) error
	CreateLocalUser(ctx context.Context, id, email, username, passwordHash string, isAdmin, mustChange bool) (*authz.User, error)
	UserPasswordHash(ctx context.Context, id string) (string, error)
	UserTokenVersion(ctx context.Context, id string) (int64, error)
	BumpTokenVersion(ctx context.Context, id string) error
	SetPasswordWithFlag(ctx context.Context, id, hash string, mustChange bool) error
	DeleteUser(ctx context.Context, id string) error
	IsUserDisabled(ctx context.Context, id string) (bool, error)
	UserGroupIDs(ctx context.Context, userID string) ([]string, error)
	UserGroupRoles(ctx context.Context, userID string) (map[string]authz.GroupRole, error)
	CreateGroup(ctx context.Context, name, description string) (*authz.Group, error)
	GetGroup(ctx context.Context, id string) (*authz.Group, error)
	ListGroups(ctx context.Context) ([]authz.Group, error)
	UpdateGroup(ctx context.Context, id, name, description string) (*authz.Group, error)
	DeleteGroup(ctx context.Context, id string) error
	AddGroupMember(ctx context.Context, groupID, userID string, role authz.GroupRole) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	ListGroupMembers(ctx context.Context, groupID string) ([]authz.MemberWithUser, error)
	SetResourceGroup(ctx context.Context, resourceType, resourceName, groupID string, public bool) error
	GetResourceGroup(ctx context.Context, resourceType, resourceName string) (*authz.ResourceGroup, error)
	DeleteResourceGroup(ctx context.Context, resourceType, resourceName string) error
	SetResourcePublic(ctx context.Context, resourceType, resourceName string, public bool) error
	ListResourcesByGroups(ctx context.Context, resourceType string, groupIDs []string, includePublic bool) ([]string, error)
	ListResourcesByGroupsPage(ctx context.Context, resourceType string, groupIDs []string, includePublic bool, limit, offset int) ([]string, int, error)
	ListResourceGroupsByType(ctx context.Context, resourceType string) (map[string]string, error)
}

// authEventsStore is the interface satisfied by *auditevents.Store and test
// doubles. Insert is called on the request path and must never block long;
// List backs the admin audit endpoint.
type authEventsStore interface {
	Insert(ctx context.Context, e *auditevents.Event) error
	List(ctx context.Context, opts auditevents.ListOptions) ([]auditevents.Event, int, error)
}

// Server provides a REST API for managing Ainsel CRDs.
type Server struct {
	client client.Client
	mux    *http.ServeMux
	// routeLog records every pattern registered on mux, so the route inventory
	// the tests check can be built without a stdlib API for reading a ServeMux
	// back. See recordingMux; register routes through s.routes(), never on the
	// raw mux.
	routeLog *recordingMux
	// router is mux behind the JSON router-error wrapper; every request
	// dispatches through it (see jsonRouterErrors, routerHandler).
	router             http.Handler
	ns                 string
	connectorCfg       ConnectorConfig
	prom               *prometheus.Client
	wsHub              *wsHub
	observabilityCache *promCache
	invocations        invocations.Store
	mcp                *mcpservers.Service
	personas           PersonaService
	skills             SkillService
	triggerStore       *triggers.Store
	// channelSvc is the channel registry: the named streams events live in and
	// the subscriptions between them. channelTransfer performs the bridge
	// deliveries the registry describes.
	channelSvc             *channels.Service
	channelTransfer        *channels.Transfer
	chat                   *chat.Store
	taskLogs               *tasklogs.Store
	authEvents             authEventsStore
	authzStore             AuthzStore
	authzChecker           *authz.Checker
	userTokens             *usertokens.Store
	signIn                 signin.Store
	internalValidateSecret string
	pluginCreds            *plugincreds.Issuer
	pluginProxyTransport   http.RoundTripper
	pluginProxyOnce        sync.Once
	pluginMCPOnce          sync.Once
	// routeLogOnce and routerOnce guard the lazy build of routeLog / router for
	// a Server that was not built by New (the tests assemble one by hand). They
	// make the first-use initialisation a happens-before edge for the reads on
	// the request path, so a hand-built Server served from several goroutines
	// cannot race on those fields.
	routeLogOnce sync.Once
	routerOnce   sync.Once
	// pluginMCP is the MCP client the plugin tool endpoints use to list and call
	// tools on a plugin's own MCP server. Wired by SetPluginMCPClient; see
	// getPluginMCPClient for the default.
	pluginMCP *pluginmcp.Client

	// skillMCPHandler is the read-only skill catalogue MCP, gated by
	// skillsMCPToken below and built once in SetSkillDiscovery. nil until a
	// catalogue is wired.
	skillMCPHandler http.Handler
	// skillsMCPToken is HUB_SKILLS_MCP_TOKEN, the bearer token the catalogue
	// accepts. Empty means the endpoint stays disabled rather than open. It is
	// separate from internalValidateSecret on purpose: that one is the
	// cluster-wide internal credential. See requireCatalogueToken.
	skillsMCPToken string
	// skillDiscovery is the catalogue reader Deps.SkillDiscovery resolved to. The
	// platform agent plane reuses it so the two surfaces that answer the same
	// question share one implementation; s.skills is deliberately not used
	// there, because SkillService carries Create/Update/Delete.
	skillDiscovery SkillDiscovery
	// agentMCPEnabled gates the platform read plane at AgentMCPPath. Off by
	// default: see Deps.AgentMCPEnabled.
	agentMCPEnabled bool

	// wsAllowedOrigins lists Origins permitted to open /api/v1/ws in addition
	// to same-origin requests. See SetWSAllowedOrigins.
	wsAllowedOrigins []string

	// localAuthSecret is the HS256 signing key for local session JWTs.
	// Non-empty enables /api/v1/auth/login (auth.mode=local).
	localAuthSecret []byte
	// passwordPolicy is the configured password rule set enforced on create,
	// admin reset, and own change, and served by
	// GET /api/v1/auth/password-policy. nil means DefaultPasswordPolicy()
	// (min 8, max 128, no classes) — the historical behavior. Wired from
	// HUB_LOCAL_PASSWORD_* envs via SetPasswordPolicy in local mode.
	passwordPolicy *PasswordPolicy
	// loginThrottle guards /api/v1/auth/login against per-username brute
	// force; initialized together with localAuthSecret.
	loginThrottle *loginThrottle

	// authMW, when non-nil, is applied to every /api/v1/* request before the
	// mux dispatches to a handler. Wired from main once OIDC env is present;
	// nil leaves the API open (local-dev escape hatch).
	authMW func(http.Handler) http.Handler
	// rateLimitMW, when non-nil, is applied to every request before auth.
	rateLimitMW func(http.Handler) http.Handler
	// userInfoURL is the OIDC userinfo endpoint URL, used by handleUserSync.
	userInfoURL string
	// defaultGroupID is Deps.DefaultGroupID; see resolveGroupID.
	defaultGroupID string
	userInfoClient *http.Client
	// eventQueue is the PostgreSQL event queue store.
	eventQueue *eventqueue.Store

	// taskStartTimeout is how long a task may sit unstarted before the derived
	// run state flags it stuck (queued · stuck) instead of letting views imply
	// it is running. Wired from TASK_START_TIMEOUT_SECONDS; see deriveRunState.
	taskStartTimeout time.Duration

	// telemetry reads the hub's own records to answer the event metrics panels.
	// It is the backend those panels ask by default. See metricsBackend in
	// handlers_observability.go, which decides between the two sources.
	telemetry *telemetry.Store

	// metricsSource pins which backend answers the event metric panels, from
	// HUB_METRICS_SOURCE. Empty means the default: the hub's own records, with
	// Prometheus behind them. See SetMetricsSource.
	metricsSource string

	// identityTracker guards automatic identity persistence so it does not
	// fire on every authenticated request. See identity_persist.go.
	identityTracker *IdentityPersistTracker

	// llmCheckTimeout caps one upstream probe made by POST /api/v1/llm/check.
	// New sets it to llmCheckDefaultTimeout; it is a field so a test can
	// shorten the cap instead of waiting out the real one.
	llmCheckTimeout time.Duration
}

// Deps is the complete dependency set of a Server.
//
// cmd/hub fills one of these in a single literal and hands it to New. There is
// no second phase of wiring: New refuses a Deps that is missing a required
// field and names every missing one in the error, so a half-built server
// cannot reach a request handler. That is what lets the handlers drop their
// "is this dependency present" branches — see AuthzStore.
//
// Required versus optional is a statement about deployment, not about wiring
// convenience. Everything the hub always has once HUB_DB_URL is set is
// required, because cmd/hub refuses to start without a database. The optional
// externals are supported deployment modes: Prometheus (HUB_PROMETHEUS_URL) and
// plugin credentials (HUB_PLUGIN_CREDENTIAL_SECRET) are each off unless an
// operator turns them on, and the handlers that use them keep their "not
// configured" answers for that reason.
type Deps struct {
	// --- Kubernetes ---

	Client       client.Client
	Namespace    string
	ConnectorCfg ConnectorConfig

	// --- Authorization (required) ---

	// AuthzStore is the user/group/resource-group registry. Auth mode "none"
	// supplies authz.NewPermissiveStore() rather than nothing, so the store is
	// never absent.
	AuthzStore AuthzStore
	// AuthzChecker answers the read/write/group guards. Auth mode "none"
	// supplies a real Checker over the permissive store.
	AuthzChecker *authz.Checker

	// AuthMiddleware authenticates every /api/v1/* request. Required: auth mode
	// "none" supplies a middleware that injects a synthetic admin caller
	// (see cmd/hub's wireAuthPlan) instead of leaving the chain empty.
	AuthMiddleware func(http.Handler) http.Handler

	// --- Services (required) ---

	Invocations invocations.Store
	MCP         *mcpservers.Service
	Personas    PersonaService
	Skills      SkillService
	// SkillDiscovery backs the read-only skill catalogue MCP. Optional: when
	// nil, New uses Skills if it implements SkillDiscovery. The endpoint stays
	// closed unless SkillsMCPToken is set as well, so a nil here is a feature
	// toggle rather than a wiring gap.
	SkillDiscovery SkillDiscovery

	// --- Postgres-backed stores (required) ---

	EventQueue      *eventqueue.Store
	Chat            *chat.Store
	TriggerStore    *triggers.Store
	ChannelSvc      *channels.Service
	ChannelTransfer *channels.Transfer
	UserTokens      *usertokens.Store
	SignIn          signin.Store
	TaskLogs        *tasklogs.Store
	AuditEvents     authEventsStore
	// Telemetry answers the event metric panels from the hub's own records.
	// Required as a store; whether it *answers* is decided per request by
	// metricsBackend, which also weighs MetricsSource and Prom.
	Telemetry *telemetry.Store

	// --- Optional externals (deployment modes) ---

	// Prom is the Prometheus client. nil means HUB_PROMETHEUS_URL was unset:
	// raw PromQL and the Prometheus-backed panels answer "not configured".
	Prom *prometheus.Client
	// PluginCreds is the plugin credential issuer. nil means
	// HUB_PLUGIN_CREDENTIAL_SECRET was unset and plugin credentials are off.
	PluginCreds *plugincreds.Issuer
	// PluginMCP overrides the plugin MCP client. Normally nil: the client is a
	// lazily built HTTP client with a tool cache (see getPluginMCPClient), and
	// only tests need to hand one in.
	PluginMCP *pluginmcp.Client

	// --- Configuration ---

	// TaskStartTimeout is how long a task may sit unstarted before the derived
	// run state calls it stuck. Zero applies DefaultTaskStartTimeout; a
	// negative value disables the stuck flag.
	TaskStartTimeout time.Duration
	// SkillsMCPToken is HUB_SKILLS_MCP_TOKEN. Empty keeps the catalogue MCP
	// closed rather than open. See requireCatalogueToken.
	SkillsMCPToken string
	// AgentMCPEnabled is HUB_AGENT_MCP_ENABLED. It gates the platform read
	// plane at AgentMCPPath. Off by default: a hub whose operator never opted
	// in answers 503 there rather than serving platform state to anything in
	// the namespace that carries the internal secret.
	AgentMCPEnabled bool
	// InternalValidateSecret is HUB_INTERNAL_VALIDATE_SECRET. Empty closes the
	// internal endpoints that authenticate with it.
	InternalValidateSecret string
	// WSAllowedOrigins are Origins allowed to open /api/v1/ws in addition to
	// same-origin. See wsOriginAllowed.
	WSAllowedOrigins []string
	// MetricsSource pins which backend answers the event metric panels, from
	// HUB_METRICS_SOURCE. Empty means the default (the hub's own records).
	MetricsSource string
	// RateLimitRPS / RateLimitBurst configure the per-IP limiter. RPS <= 0
	// disables it.
	RateLimitRPS   float64
	RateLimitBurst int
	// LocalAuthSecret is the HS256 signing key for local session JWTs.
	// Non-empty enables /api/v1/auth/login (auth.mode=local) and installs the
	// per-username login throttle.
	LocalAuthSecret []byte
	// PasswordPolicy is the configured password rule set. nil means
	// DefaultPasswordPolicy() — the historical behaviour.
	PasswordPolicy *PasswordPolicy
	// UserInfoURL is the OIDC userinfo endpoint used by handleUserSync. Empty
	// in local-only and no-auth modes, which have no userinfo endpoint.
	UserInfoURL string
	// DefaultGroupID is the group an install with the group model switched off
	// files resources under when the caller omits groupId.
	//
	// It is a deployment setting, not a fallback for a missing store: an install
	// in auth mode "none" (authz.PermissiveStore) has exactly one group, so
	// "no groupId" has a meaning there. With the group model on this is empty and
	// a create without groupId is a 400 — the caller has to say who owns the
	// resource. cmd/hub sets it from the resolved auth mode.
	DefaultGroupID string

	// IdentityTracker is the TTL guard behind NewIdentityPersistMiddleware.
	// cmd/hub builds one, uses it to compose the auth chain, and passes it here
	// so the admin surface (ClearUsername) can invalidate a sub the chain is
	// otherwise holding back. Zero applies a fresh tracker, which is what a
	// server that composes its chain after construction wants.
	IdentityTracker *IdentityPersistTracker
}

// missingRequired names the Deps fields New refuses to build without, in
// declaration order so the error reads like the struct.
func (d Deps) missingRequired() []string {
	present := []struct {
		name string
		ok   bool
	}{
		{"Client", d.Client != nil},
		{"Namespace", d.Namespace != ""},
		{"AuthzStore", d.AuthzStore != nil},
		{"AuthzChecker", d.AuthzChecker != nil},
		{"AuthMiddleware", d.AuthMiddleware != nil},
		{"Invocations", d.Invocations != nil},
		{"MCP", d.MCP != nil},
		{"Personas", d.Personas != nil},
		{"Skills", d.Skills != nil},
		{"EventQueue", d.EventQueue != nil},
		{"Chat", d.Chat != nil},
		{"TriggerStore", d.TriggerStore != nil},
		{"ChannelSvc", d.ChannelSvc != nil},
		{"ChannelTransfer", d.ChannelTransfer != nil},
		{"UserTokens", d.UserTokens != nil},
		{"SignIn", d.SignIn != nil},
		{"TaskLogs", d.TaskLogs != nil},
		{"AuditEvents", d.AuditEvents != nil},
		{"Telemetry", d.Telemetry != nil},
	}
	var missing []string
	for _, p := range present {
		if !p.ok {
			missing = append(missing, p.name)
		}
	}
	return missing
}

// getPluginMCPClient returns the plugin MCP client, building the default on
// first use: plugin tool calls are in-cluster HTTP with a bounded dial, in the
// same shape as the plugin proxy transport.
func (s *Server) getPluginMCPClient() *pluginmcp.Client {
	// Once: concurrent first requests must share one client (and its tool cache).
	s.pluginMCPOnce.Do(func() {
		if s.pluginMCP == nil {
			s.pluginMCP = pluginmcp.New(&http.Client{Timeout: 30 * time.Second})
		}
	})
	return s.pluginMCP
}

// ErrInsecureAuthConfig is returned by ValidateAuthConfig when the hub would
// serve the API with no authenticating middleware and the operator has not
// explicitly opted into running without auth.
var ErrInsecureAuthConfig = errors.New("api auth middleware is not configured")

// ValidateAuthConfig fails fast when the hub would serve /api/v1/* with no
// authenticating middleware and the operator has not explicitly opted into
// running without auth.
//
// It takes the middleware rather than a Server because the answer is about the
// deployment, not the struct: auth mode "none" hands New a middleware that
// injects a synthetic admin caller, which is exactly what
// HUB_ALLOW_INSECURE_NO_AUTH opts into. cmd/hub therefore passes the
// *authenticating* middleware here — nil in mode none — and the identity
// injector to Deps.
func ValidateAuthConfig(mw func(http.Handler) http.Handler, allowInsecureNoAuth bool) error {
	if mw != nil {
		return nil
	}
	if allowInsecureNoAuth {
		return nil
	}
	return fmt.Errorf("%w: auth middleware is not configured; set HUB_ALLOW_INSECURE_NO_AUTH=true to override", ErrInsecureAuthConfig)
}

// AuthMiddlewareConfigured reports whether an auth middleware was installed.
//
// Every server built by New has one — mode "none" installs the synthetic-admin
// injector — so this is true for a production server in every mode. The chat
// handlers use it as the second half of "may a request with no caller identity
// act as the shared local user": with a caller-injecting middleware there is
// always a caller, and the branch only fires on a hand-built server that was
// given no chain at all.
func (s *Server) AuthMiddlewareConfigured() bool {
	return s.authMW != nil
}

// New builds a fully wired API server and registers every route on it.
//
// Construction is one step: Deps carries the whole dependency set, New
// validates it, and there is no setter phase. A missing required dependency is
// a startup error naming the field, never a nil a handler has to survive.
//
// Route table. Conventions (issue #265 — see also docs/conventions.md,
// "Routing"):
//
//   - A route is declared as a Go method + wildcard pattern
//     ("GET /api/v1/agents/{name}"), so the mux owns method matching; a
//     handler must not switch on r.Method or re-parse sub-paths. The two
//     deliberate exceptions are documented at their registration below
//     (/health, /api/v1/ws).
//   - The single resource identifier of a path always uses the placeholder
//     name {name}, including for DB-id resources. ServeMux panics at
//     registration when the same path position carries two different
//     wildcard names, so one convention removes that class of startup
//     failure and lets the guard helpers below read one key.
//   - Authz guards are attached here at registration (s.read / s.write /
//     s.internalTask), not inside a branch of a method switch — a guard a
//     handler has to remember is a guard a new sub-route forgets.
//   - Deleting a collection's old `collection` + `collection/` pair is part of
//     converting it: a method-less pattern matches every method, so leaving it
//     registered stops the mux from answering 405 at all.
//
// Every route group is registered unconditionally. The channel registry, the
// persona service and the skill service are required Deps, so the last
// "mounted only when wired" group is gone; the two groups still gated on
// configuration (the skill catalogue MCP, the plugin credential endpoints) are
// feature toggles, documented at their registration.
//
// The route inventory test (router_test.go) reads back every registration and
// fails when one drifts from the snapshot or from these rules.
func New(deps Deps) (*Server, error) {
	if missing := deps.missingRequired(); len(missing) > 0 {
		return nil, fmt.Errorf("api.New: incomplete Deps, missing required field(s): %s", strings.Join(missing, ", "))
	}

	s := &Server{
		client:                 deps.Client,
		mux:                    http.NewServeMux(),
		ns:                     deps.Namespace,
		connectorCfg:           deps.ConnectorCfg,
		prom:                   deps.Prom,
		invocations:            deps.Invocations,
		mcp:                    deps.MCP,
		personas:               deps.Personas,
		skills:                 deps.Skills,
		triggerStore:           deps.TriggerStore,
		channelSvc:             deps.ChannelSvc,
		channelTransfer:        deps.ChannelTransfer,
		chat:                   deps.Chat,
		taskLogs:               deps.TaskLogs,
		authEvents:             deps.AuditEvents,
		authzStore:             deps.AuthzStore,
		authzChecker:           deps.AuthzChecker,
		userTokens:             deps.UserTokens,
		signIn:                 deps.SignIn,
		eventQueue:             deps.EventQueue,
		telemetry:              deps.Telemetry,
		pluginCreds:            deps.PluginCreds,
		pluginMCP:              deps.PluginMCP,
		internalValidateSecret: deps.InternalValidateSecret,
		skillsMCPToken:         deps.SkillsMCPToken,
		agentMCPEnabled:        deps.AgentMCPEnabled,
		wsAllowedOrigins:       deps.WSAllowedOrigins,
		metricsSource:          deps.MetricsSource,
		userInfoURL:            deps.UserInfoURL,
		passwordPolicy:         deps.PasswordPolicy,
		defaultGroupID:         deps.DefaultGroupID,
		authMW:                 deps.AuthMiddleware,
	}

	// Config defaults, applied here rather than at the call site so the zero
	// value of Deps means "the historical behaviour" instead of "off".
	s.taskStartTimeout = deps.TaskStartTimeout
	if s.taskStartTimeout == 0 {
		s.taskStartTimeout = DefaultTaskStartTimeout
	}
	s.llmCheckTimeout = llmCheckDefaultTimeout
	if len(deps.LocalAuthSecret) > 0 {
		s.localAuthSecret = deps.LocalAuthSecret
		s.loginThrottle = newLoginThrottle()
	}
	if deps.UserInfoURL != "" {
		s.userInfoClient = &http.Client{Timeout: 5 * time.Second}
	}
	if deps.RateLimitRPS > 0 {
		s.rateLimitMW = RateLimitMiddleware(deps.RateLimitRPS, deps.RateLimitBurst)
	}

	// The read-only skill catalogue MCP. Deps.Skills is the concrete service,
	// which satisfies SkillDiscovery; going through the narrower interface
	// keeps Create/Update/Delete out of the catalogue's reach.
	disc := deps.SkillDiscovery
	if disc == nil {
		if d, ok := deps.Skills.(SkillDiscovery); ok {
			disc = d
		}
	}
	if disc != nil {
		s.skillDiscovery = disc
		s.skillMCPHandler = s.requireCatalogueToken(skillMCPServer(disc).ServeHTTP)
	}

	mux := s.routes()
	s.router = jsonRouterErrors(s.mux)
	s.wsHub = newWsHub()
	s.observabilityCache = newPromCache(observabilityCacheTTL)
	s.identityTracker = deps.IdentityTracker
	if s.identityTracker == nil {
		s.identityTracker = NewIdentityPersistTracker()
	}
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/v1/agents", s.handleAgents)
	mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	mux.HandleFunc("/api/v1/llm/check", s.handleLLMCheck)
	mux.HandleFunc("/api/v1/agent-images", s.handleAgentImages)
	mux.HandleFunc("/api/v1/agent-images/", s.handleAgentImagePath)
	mux.HandleFunc("/api/v1/connectors", s.handleConnectors)
	mux.HandleFunc("/api/v1/connectors/", s.handleConnector)
	registerPluginRoutes(s)
	mux.HandleFunc("/api/v1/triggers", s.handleTriggers)
	mux.HandleFunc("/api/v1/triggers/", s.handleTrigger)
	mux.HandleFunc("/api/v1/cron-triggers", s.handleCronTriggers)
	mux.HandleFunc("/api/v1/cron-triggers/", s.handleCronTrigger)
	mux.HandleFunc("/api/v1/errors", s.handleErrors)
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/events/", s.handleEvent)
	mux.HandleFunc("/api/v1/invocations", s.handleInvocations)
	mux.HandleFunc("/api/v1/invocations/", s.handleInvocation)
	mux.HandleFunc("/api/v1/mcp-servers", s.handleMCPServers)
	mux.HandleFunc("/api/v1/mcp-servers/", s.handleMCPServer)
	mux.HandleFunc("/api/v1/tokens", s.handleTokens)
	mux.HandleFunc("/api/v1/stats", s.handleStats)
	mux.HandleFunc("/api/v1/ws", s.handleWs)
	mux.HandleFunc("/api/v1/me", s.handleMe)
	mux.HandleFunc("/api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/v1/plugin-context", s.handlePluginContext)
	mux.HandleFunc("/api/v1/plugin-context/visible", s.handlePluginContextVisible)
	mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginRoutes)
	mux.HandleFunc("/api/internal/plugin-tools", s.handleInternalPluginTools)
	mux.HandleFunc("/api/v1/plugin-tools/call", s.handlePluginToolCall)
	mux.HandleFunc("/api/v1/observability/metrics/summary", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/timeseries", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/agents", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/tokens/summary", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/tokens/timeseries", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/tokens/by-subject", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/metrics/tokens/by-event", s.handleObservability)
	mux.HandleFunc("/api/v1/observability/conversations", s.handleConversations)
	mux.HandleFunc("/api/v1/observability/logs", s.handleObservabilityLogs)
	mux.HandleFunc("/api/v1/observability/metrics/query", s.handleObservabilityMetricsQuery)
	mux.HandleFunc("/api/internal/events", s.handleIngestEvent)
	mux.HandleFunc(skillMCPPath, s.handleSkillMCP)
	mux.HandleFunc(AgentMCPPath, s.handleAgentMCP)
	mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)
	mux.HandleFunc("/api/v1/queue/info", s.handleQueueInfo)
	mux.HandleFunc("/api/v1/queue/recent", s.handleQueueRecent)
	mux.HandleFunc("/api/internal/task-logs", s.handleIngestTaskLog)
	mux.HandleFunc("/api/internal/task-messages", s.handleIngestTaskMessage)
	mux.HandleFunc("/api/v1/platform/health", s.handlePlatformHealth)
	mux.HandleFunc("/api/v1/users", s.handleUsers)
	mux.HandleFunc("/api/v1/users/", s.handleUser)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("/api/v1/auth/password", s.handleChangePassword)
	mux.HandleFunc("/api/v1/auth/sign-out-all", s.handleSignOutAll)
	mux.HandleFunc("/api/v1/auth/password-policy", s.handlePasswordPolicy)
	mux.HandleFunc("/api/v1/user-tokens", s.handleUserTokens)
	mux.HandleFunc("/api/v1/user-tokens/", s.handleUserTokenDelete)
	mux.HandleFunc("/api/internal/user-tokens/validate", s.handleUserTokenValidate)
	mux.HandleFunc("/api/internal/chat/sessions", s.handleInternalChatSessions)
	mux.HandleFunc("/api/internal/chat/sessions/", s.handleInternalChatSession)
	mux.HandleFunc("/api/v1/groups", s.handleGroups)
	mux.HandleFunc("/api/v1/groups/", s.handleGroup)
	mux.HandleFunc("/api/v1/me/resources", s.handleMyResources)
	mux.HandleFunc("/api/v1/audit/auth-events", s.handleAuditAuthEvents)
	mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)
	s.registerChannelRoutes()
	RegisterPersonaRoutes(mux, deps.Personas, deps.AuthzStore, deps.AuthzChecker, deps.DefaultGroupID)
	RegisterSkillRoutes(mux, deps.Skills, deps.AuthzStore, deps.AuthzChecker, deps.DefaultGroupID)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.rateLimitMW != nil {
		s.rateLimitMW(http.HandlerFunc(s.serveHTTPInner)).ServeHTTP(w, r)
		return
	}
	s.serveHTTPInner(w, r)
}

func (s *Server) serveHTTPInner(w http.ResponseWriter, r *http.Request) {
	if s.authMW != nil && strings.HasPrefix(r.URL.Path, "/api/v1/") && !isAuthExempt(r) {
		// The forced-change gate sits inside the auth chain because it decides on
		// the context user, and in front of the mux because a gate the handlers
		// have to opt into is a gate new endpoints forget.
		s.authMW(s.passwordChangeGate(s.routerHandler())).ServeHTTP(w, r)
		return
	}
	s.routerHandler().ServeHTTP(w, r)
}

// routerHandler returns the route mux behind the JSON router-error wrapper.
// New builds it; a Server assembled by hand (the tests do) gets it on first use,
// so no request path can dispatch to the bare mux and answer a router-generated
// 404/405 in the mux's own plain text.
func (s *Server) routerHandler() http.Handler {
	s.routerOnce.Do(func() {
		if s.router == nil {
			s.router = jsonRouterErrors(s.mux)
		}
	})
	return s.router
}

// isAuthExempt lists /api/v1/* requests that must stay reachable without a
// session. The login endpoint qualifies: it validates credentials itself and
// is still protected by the global per-IP rate limiter plus a per-username
// throttle. Login stays exempt even for a flagged user — they need it to
// reach the forced change screen. Plugin UI requests are also exempt
// (the handler will return 405 for non-GET/HEAD methods).
func isAuthExempt(r *http.Request) bool {
	if r.URL.Path == "/api/v1/auth/login" {
		return true
	}

	// Plugin UI requests are auth-exempt
	// Pattern: /api/v1/plugins/<id>/ui/<non-empty>
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, "/api/v1/plugins/") {
		return false
	}

	// Parse the path: /api/v1/plugins/<id>/ui/<rest>
	rest := strings.TrimPrefix(escapedPath, "/api/v1/plugins/")
	parts := strings.SplitN(rest, "/", 3)

	// Need at least 3 parts: id, "ui", rest
	if len(parts) < 3 {
		return false
	}

	if parts[1] != "ui" {
		return false
	}

	// rest must be non-empty (UI with no rest is invalid)
	if parts[2] == "" {
		return false
	}

	return true
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// writeAuthzError maps the authz store sentinel errors to their HTTP
// responses: authz.ErrNotFound becomes 404 "<resource> not found" and
// authz.ErrAlreadyExists becomes 409 "<resource> already exists". It
// returns true when it wrote a response, so callers use it as:
//
//	if writeAuthzError(w, err, "user") {
//		return
//	}
//	httpx.WriteError(w, http.StatusInternalServerError, err.Error())
func writeAuthzError(w http.ResponseWriter, err error, resource string) bool {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, resource+" not found")
		return true
	case errors.Is(err, authz.ErrAlreadyExists):
		httpx.WriteError(w, http.StatusConflict, resource+" already exists")
		return true
	}
	return false
}

func extractName(path, prefix string) string {
	return strings.TrimPrefix(path, prefix)
}

func sanitizeLabelValue(v string) string {
	return strings.ReplaceAll(v, `"`, "")
}

// --- Authorization helpers ---

// resolveGroupID fills in the install's default group for a create that named
// none. It returns "" when the deployment has no default group, which is what
// makes a missing groupId a 400 rather than a silent filing.
func (s *Server) resolveGroupID(id string) string {
	if id != "" {
		return id
	}
	return s.defaultGroupID
}

// requireRead checks that the authenticated user can read the resource.
// Returns true if access is granted (caller should proceed).
//
// The checker is always present (Deps requires it), so there is no "authz not
// wired, allow" branch: auth mode "none" gets a Checker over
// authz.PermissiveStore, which answers every subject as admin.
func (s *Server) requireRead(w http.ResponseWriter, r *http.Request, resourceType, resourceName string) bool {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanRead(r.Context(), u.Sub, resourceType, resourceName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		// Denied reads answer 404, not 403: a deleted resource has no ownership
		// row, so a 403 could not be told apart from "gone" (issue #390). The
		// body shape matches the handlers' own not-found answers so guard and
		// handler 404s are interchangeable for clients.
		httpx.WriteError(w, http.StatusNotFound, resourceName+" not found")
		return false
	}
	return true
}

// requireWrite checks that the authenticated user can modify the resource.
func (s *Server) requireWrite(w http.ResponseWriter, r *http.Request, resourceType, resourceName string) bool {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanWrite(r.Context(), u.Sub, resourceType, resourceName)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		// Same opaque 404 as requireRead (issue #390): a denied write addresses
		// the resource as not-found, so deleted and foreign-group resources are
		// indistinguishable to non-admins.
		httpx.WriteError(w, http.StatusNotFound, resourceName+" not found")
		return false
	}
	return true
}

// requireGroupOwner checks that the authenticated user is an owner of the group.
func (s *Server) requireGroupOwner(w http.ResponseWriter, r *http.Request, groupID string) bool {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanManageGroup(r.Context(), u.Sub, groupID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		httpx.WriteError(w, http.StatusForbidden, "group owner or admin required")
		return false
	}
	return true
}

// checkGroupWrite is the error-returning twin of requireGroupWrite: it reports
// the same three failures as *apiError values and writes nothing, so it can run
// inside a validation pipeline that must not touch the response. requireGroupWrite
// is the wrapper for handlers that respond directly; the remaining guards
// migrate to this shape the same way.
func (s *Server) checkGroupWrite(r *http.Request, groupID string) error {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		return newAPIError(http.StatusUnauthorized, "unauthorized")
	}
	ok, err := s.authzChecker.CanWriteGroup(r.Context(), u.Sub, groupID)
	if err != nil {
		return wrapAPIError(http.StatusInternalServerError, err.Error(), err)
	}
	if !ok {
		return newAPIError(http.StatusForbidden, "writer or owner of the group required")
	}
	return nil
}

// requireGroupWrite checks that the authenticated user can create resources
// in the given group (writer or owner, or admin).
func (s *Server) requireGroupWrite(w http.ResponseWriter, r *http.Request, groupID string) bool {
	if err := s.checkGroupWrite(r, groupID); err != nil {
		writeErr(w, err)
		return false
	}
	return true
}

// callerGroupIDs returns the group IDs for the authenticated user.
func (s *Server) callerGroupIDs(r *http.Request) []string {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		return nil
	}
	// Check if there's a principal (agent or plugin caller)
	if ids, ok := authz.PrincipalGroupIDs(r.Context(), u.Sub); ok {
		return ids
	}
	// Fall back to store lookup for user callers
	ids, _ := s.authzStore.UserGroupIDs(r.Context(), u.Sub)
	return ids
}

// callerIsAdmin returns true if the authenticated user is an admin.
func (s *Server) callerIsAdmin(r *http.Request) bool {
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		return false
	}
	admin, _ := s.authzChecker.IsAdmin(r.Context(), u.Sub)
	return admin
}

// filterByAccess filters a list of resource names to those accessible by the caller.
func (s *Server) filterByAccess(r *http.Request, resourceType string, names []string) []string {
	if s.callerIsAdmin(r) {
		return names
	}
	groupIDs := s.callerGroupIDs(r)
	includePublic := r.URL.Query().Get("includePublic") == "true"
	accessible, err := s.authzStore.ListResourcesByGroups(r.Context(), resourceType, groupIDs, includePublic)
	if err != nil {
		return nil
	}
	set := make(map[string]bool, len(accessible))
	for _, n := range accessible {
		set[n] = true
	}
	var out []string
	for _, n := range names {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}

// accessibleIDs returns the resource names the caller may read, for list
// endpoints that apply the authz filter inside the query (id = ANY($n))
// instead of post-filtering loaded rows. The result follows the same
// nil-vs-empty contract as the persona/skill handlers' accessibleSet, and
// it is pinned by the per-kind list_authz_parity_test.go files:
//   - nil → unrestricted (the caller is an admin, which in auth mode "none"
//     is every caller)
//   - empty non-nil → the caller may see nothing; lookup errors land here
//     so the filter stays fail-closed
//   - non-empty → exactly the accessible resource names
func (s *Server) accessibleIDs(r *http.Request, resourceType string) []string {
	if s.callerIsAdmin(r) {
		return nil
	}
	groupIDs := s.callerGroupIDs(r)
	includePublic := r.URL.Query().Get("includePublic") == "true"
	accessible, err := s.authzStore.ListResourcesByGroups(r.Context(), resourceType, groupIDs, includePublic)
	if err != nil {
		return []string{}
	}
	if accessible == nil {
		return []string{}
	}
	return accessible
}

// toAccessSet converts a slice of names to a lookup set.
func toAccessSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// accessKeys converts an accessible-id set back into a slice for the SQL
// allow-list (id = ANY($n)). Order is unspecified; queries do not depend on it.
func accessKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// intersectNames keeps the entries of names that also appear in subset,
// preserving the order of names. Used to narrow an already authz-scoped name
// list by an explicit group filter without ever widening it.
func intersectNames(names, subset []string) []string {
	set := toAccessSet(subset)
	var out []string
	for _, n := range names {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}
