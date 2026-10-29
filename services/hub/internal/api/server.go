package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/services/hub/internal/prometheus"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	"github.com/ainsel-project/ainsel/services/hub/internal/telemetry"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
	"github.com/ainsel-project/ainsel/services/hub/internal/usertokens"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// authzStore is the interface satisfied by *authz.Store and test doubles.
type authzStore interface {
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
	client             client.Client
	mux                *http.ServeMux
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
	authzStore             authzStore
	authzChecker           *authz.Checker
	userTokens             *usertokens.Store
	internalValidateSecret string
	pluginCreds            *plugincreds.Issuer
	pluginProxyTransport   http.RoundTripper
	pluginProxyOnce        sync.Once
	pluginMCPOnce          sync.Once
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
	userInfoURL    string
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
	identityTracker *identityPersistTracker
}

// SetAuthZ wires the authorization store and checker.
func (s *Server) SetAuthZ(store authzStore, checker *authz.Checker) {
	s.authzStore = store
	s.authzChecker = checker
}

// SetUserTokenStore wires the user token store.
func (s *Server) SetUserTokenStore(store *usertokens.Store) {
	s.userTokens = store
}

// SetSkillsMCPToken sets the bearer token accepted by the read-only skill
// catalogue MCP (/api/internal/skills/mcp).
func (s *Server) SetSkillsMCPToken(token string) {
	s.skillsMCPToken = token
}

// SetInternalValidateSecret sets the shared secret for /api/internal/user-tokens/validate.
func (s *Server) SetInternalValidateSecret(secret string) {
	s.internalValidateSecret = secret
}

// SetPluginMCPClient sets the MCP client used to reach plugin MCP endpoints.
// Used by tests and by wiring that needs a custom transport.
func (s *Server) SetPluginMCPClient(c *pluginmcp.Client) {
	s.pluginMCP = c
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

// SetTriggerStore wires the trigger DB store for the /api/v1/triggers/* and /api/v1/cron-triggers/* endpoints.
func (s *Server) SetTriggerStore(store *triggers.Store) {
	s.triggerStore = store
}

// SetTaskStartTimeout overrides the stuck deadline used by the derived run
// state (see deriveRunState). A non-positive value disables the stuck flag.
func (s *Server) SetTaskStartTimeout(d time.Duration) {
	s.taskStartTimeout = d
}

// SetChatStore wires the chat session store for the /api/v1/chat/* endpoints.
func (s *Server) SetChatStore(store *chat.Store) {
	s.chat = store
}

// SetTaskLogStore wires the task log store for the observability logs and
// errors endpoints. When set, these endpoints serve structured log entries
// from the hub's own database instead of requiring an external log backend.
func (s *Server) SetTaskLogStore(store *tasklogs.Store) {
	s.taskLogs = store
}

// SetAuditEventStore wires the auth-event audit store. When unset (e.g. a hub
// without a database), the auth handlers skip row writes and the audit
// endpoint answers 503 — behaviour is exactly as before the store existed.
func (s *Server) SetAuditEventStore(store authEventsStore) {
	s.authEvents = store
}

// SetAuthMiddleware installs an HTTP middleware that wraps every /api/v1/*
// request. Pass nil (the default) to leave the API open. This follows the
// same pattern as other optional wiring: keep the constructor signature stable.
func (s *Server) SetAuthMiddleware(mw func(http.Handler) http.Handler) {
	s.authMW = mw
}

// SetUserInfoURL configures the OIDC userinfo endpoint used by the username
// sync handler. Must be called before the server starts serving.
func (s *Server) SetUserInfoURL(endpoint string) {
	s.userInfoURL = endpoint
	s.userInfoClient = &http.Client{Timeout: 5 * time.Second}
}

// ErrInsecureAuthConfig is returned by ValidateAuthConfig when the server
// has no auth middleware wired and the operator has not explicitly opted into
// running without auth.
var ErrInsecureAuthConfig = errors.New("api auth middleware is not configured")

// ValidateAuthConfig fails fast when the API would be served without any auth
// middleware and the operator has not explicitly opted into running without auth.
func (s *Server) ValidateAuthConfig(allowInsecureNoAuth bool) error {
	if s.authMW != nil {
		return nil
	}
	if allowInsecureNoAuth {
		return nil
	}
	return fmt.Errorf("%w: auth middleware is not configured; set HUB_ALLOW_INSECURE_NO_AUTH=true to override", ErrInsecureAuthConfig)
}

// AuthMiddlewareConfigured reports whether SetAuthMiddleware has been called
// with a non-nil middleware.
func (s *Server) AuthMiddlewareConfigured() bool {
	return s.authMW != nil
}

// Config carries optional dependencies for the API server.
type Config struct {
}

// New creates a new API server with routes registered.
func New(c client.Client, namespace string, connectorCfg ConnectorConfig, promClient *prometheus.Client, invStore invocations.Store, mcp *mcpservers.Service, personaSvc *personas.Service, skillSvc SkillService, cfg *Config) *Server {
	s := &Server{client: c, mux: http.NewServeMux(), ns: namespace, connectorCfg: connectorCfg, prom: promClient, invocations: invStore, mcp: mcp, skills: skillSvc}
	s.wsHub = newWsHub()
	s.observabilityCache = newPromCache(observabilityCacheTTL)
	s.identityTracker = newIdentityPersistTracker()
	s.taskStartTimeout = DefaultTaskStartTimeout
	s.mux.HandleFunc("/health", s.health)
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	s.mux.HandleFunc("/api/v1/agent-images", s.handleAgentImages)
	s.mux.HandleFunc("/api/v1/agent-images/", s.handleAgentImagePath)
	s.mux.HandleFunc("/api/v1/connectors", s.handleConnectors)
	s.mux.HandleFunc("/api/v1/connectors/", s.handleConnector)
	registerPluginRoutes(s)
	s.mux.HandleFunc("/api/v1/triggers", s.handleTriggers)
	s.mux.HandleFunc("/api/v1/triggers/", s.handleTrigger)
	s.mux.HandleFunc("/api/v1/cron-triggers", s.handleCronTriggers)
	s.mux.HandleFunc("/api/v1/cron-triggers/", s.handleCronTrigger)
	s.mux.HandleFunc("/api/v1/errors", s.handleErrors)
	s.mux.HandleFunc("/api/v1/events", s.handleEvents)
	s.mux.HandleFunc("/api/v1/events/", s.handleEvent)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)
	s.mux.HandleFunc("/api/v1/invocations/", s.handleInvocation)
	s.mux.HandleFunc("/api/v1/mcp-servers", s.handleMCPServers)
	s.mux.HandleFunc("/api/v1/mcp-servers/", s.handleMCPServer)
	s.mux.HandleFunc("/api/v1/tokens", s.handleTokens)
	s.mux.HandleFunc("/api/v1/stats", s.handleStats)
	s.mux.HandleFunc("/api/v1/ws", s.handleWs)
	s.mux.HandleFunc("/api/v1/me", s.handleMe)
	s.mux.HandleFunc("/api/v1/auth/me", s.handleAuthMe)
	s.mux.HandleFunc("/api/v1/plugin-context", s.handlePluginContext)
	s.mux.HandleFunc("/api/v1/plugin-context/visible", s.handlePluginContextVisible)
	s.mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginRoutes)
	s.mux.HandleFunc("/api/internal/plugin-tools", s.handleInternalPluginTools)
	s.mux.HandleFunc("/api/v1/plugin-tools/call", s.handlePluginToolCall)
	s.mux.HandleFunc("/api/v1/observability/metrics/summary", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/timeseries", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/agents", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/tokens/summary", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/tokens/timeseries", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/tokens/by-subject", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/metrics/tokens/by-event", s.handleObservability)
	s.mux.HandleFunc("/api/v1/observability/conversations", s.handleConversations)
	s.mux.HandleFunc("/api/v1/observability/logs", s.handleObservabilityLogs)
	s.mux.HandleFunc("/api/v1/observability/metrics/query", s.handleObservabilityMetricsQuery)
	s.mux.HandleFunc("/api/internal/events", s.handleIngestEvent)
	s.mux.HandleFunc(skillMCPPath, s.handleSkillMCP)
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)
	s.mux.HandleFunc("/api/v1/queue/info", s.handleQueueInfo)
	s.mux.HandleFunc("/api/v1/queue/recent", s.handleQueueRecent)
	s.mux.HandleFunc("/api/internal/task-logs", s.handleIngestTaskLog)
	s.mux.HandleFunc("/api/internal/task-messages", s.handleIngestTaskMessage)
	s.mux.HandleFunc("/api/v1/platform/health", s.handlePlatformHealth)
	s.mux.HandleFunc("/api/v1/users", s.handleUsers)
	s.mux.HandleFunc("/api/v1/users/", s.handleUser)
	s.mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/v1/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/v1/auth/password", s.handleChangePassword)
	s.mux.HandleFunc("/api/v1/auth/sign-out-all", s.handleSignOutAll)
	s.mux.HandleFunc("/api/v1/auth/password-policy", s.handlePasswordPolicy)
	s.mux.HandleFunc("/api/v1/user-tokens", s.handleUserTokens)
	s.mux.HandleFunc("/api/v1/user-tokens/", s.handleUserTokenDelete)
	s.mux.HandleFunc("/api/internal/user-tokens/validate", s.handleUserTokenValidate)
	s.mux.HandleFunc("/api/internal/chat/sessions", s.handleInternalChatSessions)
	s.mux.HandleFunc("/api/internal/chat/sessions/", s.handleInternalChatSession)
	s.mux.HandleFunc("/api/v1/groups", s.handleGroups)
	s.mux.HandleFunc("/api/v1/groups/", s.handleGroup)
	s.mux.HandleFunc("/api/v1/me/resources", s.handleMyResources)
	s.mux.HandleFunc("/api/v1/audit/auth-events", s.handleAuditAuthEvents)
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)
	if personaSvc != nil {
		// Assigned only when non-nil: a typed-nil *personas.Service in an
		// interface field would pass a nil check and panic on use.
		s.personas = personaSvc
		RegisterPersonaRoutes(s.mux, personaSvc, &s.authzStore, &s.authzChecker)
	}
	if skillSvc != nil {
		RegisterSkillRoutes(s.mux, skillSvc, &s.authzStore, &s.authzChecker)
	}
	return s
}

// ServeHTTP implements http.Handler.
// SetRateLimiter installs a per-IP rate limiting middleware applied to all
// requests. Pass rps=0 to disable (the default).
func (s *Server) SetRateLimiter(rps float64, burst int) {
	if rps > 0 {
		s.rateLimitMW = RateLimitMiddleware(rps, burst)
	}
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
		s.authMW(s.passwordChangeGate(s.mux)).ServeHTTP(w, r)
		return
	}
	s.mux.ServeHTTP(w, r)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeAuthzError maps the authz store sentinel errors to their HTTP
// responses: authz.ErrNotFound becomes 404 "<resource> not found" and
// authz.ErrAlreadyExists becomes 409 "<resource> already exists". It
// returns true when it wrote a response, so callers use it as:
//
//	if writeAuthzError(w, err, "user") {
//		return
//	}
//	writeError(w, http.StatusInternalServerError, err.Error())
func writeAuthzError(w http.ResponseWriter, err error, resource string) bool {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		writeError(w, http.StatusNotFound, resource+" not found")
		return true
	case errors.Is(err, authz.ErrAlreadyExists):
		writeError(w, http.StatusConflict, resource+" already exists")
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

// requireRead checks that the authenticated user can read the resource.
// Returns true if access is granted (caller should proceed).
func (s *Server) requireRead(w http.ResponseWriter, r *http.Request, resourceType, resourceName string) bool {
	if s.authzChecker == nil {
		return true // authz not configured (dev mode)
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanRead(r.Context(), u.Sub, resourceType, resourceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// requireWrite checks that the authenticated user can modify the resource.
func (s *Server) requireWrite(w http.ResponseWriter, r *http.Request, resourceType, resourceName string) bool {
	if s.authzChecker == nil {
		return true
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanWrite(r.Context(), u.Sub, resourceType, resourceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// requireGroupOwner checks that the authenticated user is an owner of the group.
func (s *Server) requireGroupOwner(w http.ResponseWriter, r *http.Request, groupID string) bool {
	if s.authzChecker == nil {
		return true
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanManageGroup(r.Context(), u.Sub, groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "group owner or admin required")
		return false
	}
	return true
}

// requireGroupWrite checks that the authenticated user can create resources
// in the given group (writer or owner, or admin).
func (s *Server) requireGroupWrite(w http.ResponseWriter, r *http.Request, groupID string) bool {
	if s.authzChecker == nil {
		return true
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	ok, err := s.authzChecker.CanWriteGroup(r.Context(), u.Sub, groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "writer or owner of the group required")
		return false
	}
	return true
}

// callerGroupIDs returns the group IDs for the authenticated user.
func (s *Server) callerGroupIDs(r *http.Request) []string {
	if s.authzStore == nil {
		return nil
	}
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
	if s.authzChecker == nil {
		return false
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		return false
	}
	admin, _ := s.authzChecker.IsAdmin(r.Context(), u.Sub)
	return admin
}

// filterByAccess filters a list of resource names to those accessible by the caller.
func (s *Server) filterByAccess(r *http.Request, resourceType string, names []string) []string {
	if s.authzStore == nil || s.callerIsAdmin(r) {
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

// toAccessSet converts a slice of names to a lookup set.
func toAccessSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
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
