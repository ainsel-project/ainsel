package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	validation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentImageRefInfo identifies which AgentImage an Agent uses.
type AgentImageRefInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

// AgentSkillsInfo is the API representation of the agent-scoped skill
// selection: present = explicit override (empty items = no skills),
// absent = inherit the referenced image's enabledSkills.
type AgentSkillsInfo struct {
	Items []string `json:"items"`
}

// AgentMCPServerInfo is the API representation of one agent-scoped MCP
// server: the definition is resolved from the hub's MCP registry at
// write time, so later registry edits do not silently change running
// agents.
type AgentMCPServerInfo struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv,omitempty"`
}

// AgentMCPInfo is the API representation of the agent-scoped MCP
// selection: present = explicit override (empty servers = no MCP
// connections), absent = inherit the referenced image's mcpServers
// (legacy behavior).
type AgentMCPInfo struct {
	Servers []AgentMCPServerInfo `json:"servers"`
}

// AgentMCPRequest selects MCP servers by registry name; the hub resolves
// each name to its full definition when writing the agent.
type AgentMCPRequest struct {
	Servers []string `json:"servers"`
}

// resolveMCPRequest resolves registry names into agent-scoped MCP
// definitions. A nil request means "leave unchanged"; a non-nil request
// (including an empty Servers list) is an explicit selection and returns
// a non-nil AgentMCP.
func (s *Server) resolveMCPRequest(ctx context.Context, w http.ResponseWriter, req *AgentMCPRequest) (*agentv1alpha1.AgentMCP, error) {
	if req == nil {
		return nil, nil
	}
	if s.mcp == nil {
		writeError(w, http.StatusServiceUnavailable, "mcp service not configured")
		return nil, fmt.Errorf("mcp service not configured")
	}
	mcp := &agentv1alpha1.AgentMCP{Servers: make([]agentv1alpha1.AgentMCPServer, 0, len(req.Servers))}
	for _, name := range req.Servers {
		server, err := s.mcp.Get(ctx, name)
		if err != nil {
			if errors.Is(err, mcpservers.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "mcp server not found: "+name)
				return nil, err
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return nil, err
		}
		mcp.Servers = append(mcp.Servers, agentv1alpha1.AgentMCPServer{
			Name:         server.Name,
			URL:          server.URL,
			TokenFromEnv: server.TokenFromEnv,
		})
	}
	return mcp, nil
}

// agentEnvFromRequest validates the requested env overrides and converts them
// to CR entries. existing supplies the values of secret entries whose request
// value is empty ("keep the existing value"), mirroring the image env contract.
// A nil result means "no overrides", which is also what an empty request list
// produces — clearing an agent's overrides returns it to the image defaults.
func agentEnvFromRequest(w http.ResponseWriter, in []AgentImageEnvVarInfo, existing []agentv1alpha1.AgentEnvVar) ([]agentv1alpha1.AgentEnvVar, error) {
	if len(in) == 0 {
		return nil, nil
	}
	secretValues := make(map[string]string, len(existing))
	for _, e := range existing {
		if e.Secret {
			secretValues[e.Name] = e.Value
		}
	}

	out := make([]agentv1alpha1.AgentEnvVar, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, e := range in {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "env name is required")
			return nil, fmt.Errorf("env name is required")
		}
		if errs := validation.IsEnvVarName(name); len(errs) > 0 {
			writeError(w, http.StatusBadRequest, "invalid env name "+name+": "+errs[0])
			return nil, fmt.Errorf("invalid env name %q", name)
		}
		if seen[name] {
			// The operator merges by name, so duplicates would silently keep
			// only one value. Reject instead of guessing.
			writeError(w, http.StatusBadRequest, "duplicate env name: "+name)
			return nil, fmt.Errorf("duplicate env name %q", name)
		}
		seen[name] = true

		value := e.Value
		if e.Secret && value == "" {
			value = secretValues[name]
		}
		out = append(out, agentv1alpha1.AgentEnvVar{Name: name, Value: value, Secret: e.Secret})
	}
	return out, nil
}

// SimpleAgentResponse is the simplified API representation of an Agent.
type SimpleAgentResponse struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	ImageRef     AgentImageRefInfo `json:"imageRef"`
	LLM          AgentLLMInfo      `json:"llm"`
	Persona      *AgentPersonaInfo `json:"persona,omitempty"`
	EnabledTools []string          `json:"enabledTools,omitempty"`
	Skills       *AgentSkillsInfo  `json:"skills,omitempty"`
	MCP          *AgentMCPInfo     `json:"mcp,omitempty"`
	// Env lists this agent's environment overrides on top of the referenced
	// image's env. Values of entries marked Secret are never returned.
	Env []AgentImageEnvVarInfo `json:"env,omitempty"`
	// Replicas caps the number of task Jobs running at once for this agent.
	Replicas *int32 `json:"replicas,omitempty"`
	// Paused reports whether task delivery is currently withheld: spec.pause
	// evaluated at read time, so an expiring pause reads false on its own.
	// Paused agents still accumulate their routed tasks as a backlog; the
	// queue-depth fields show what has piled up.
	Paused bool `json:"paused"`
	// PausedUntil is when an expiring pause ends (RFC3339). Empty for a
	// running agent and for an indefinite pause, which must be lifted
	// explicitly.
	PausedUntil    string                   `json:"pausedUntil,omitempty"`
	Memory         *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud    *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	CustomProvider *AgentCustomProviderInfo `json:"customProvider,omitempty"`
	Status         *SimpleAgentStatus       `json:"status,omitempty"`
	UpdatedAt      string                   `json:"updatedAt,omitempty"`
}

type AgentLLMInfo struct {
	Model       string   `json:"model"`
	Provider    string   `json:"provider,omitempty"`
	MaxTurns    int      `json:"maxTurns,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	// Vision mirrors spec.llm.vision: whether pi sends image input.
	// A pointer keeps an explicit false distinguishable from "unset".
	Vision *bool `json:"vision,omitempty"`
	// ContextWindow mirrors spec.llm.contextWindow: overrides the context
	// window (tokens) declared in the agent's generated models.json. It
	// should match the context limit the actual LLM endpoint enforces. A
	// pointer keeps it distinguishable from "unset".
	ContextWindow *int `json:"contextWindow,omitempty"`
}

// AgentPersonaInfo is the REST representation of an Agent's persona reference.
// ID points at a persona managed by the hub at /api/v1/personas.
type AgentPersonaInfo struct {
	ID string `json:"id"`
}

type AgentMemoryInfo struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
}

type AgentOllamaCloudInfo struct {
	// APIKey is the Ollama Cloud API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
}

type AgentOpenCodeInfo struct {
	// APIKey is the OpenCode API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
}

type AgentAlibabaCloudInfo struct {
	// APIKey is the Alibaba Token Plan API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
}

// AgentCustomProviderInfo is the REST representation of a custom LLM provider.
type AgentCustomProviderInfo struct {
	// URL is the custom LLM API base URL.
	URL string `json:"url"`
	// APIKey is the custom API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
}

type SimpleAgentStatus struct {
	Ready    bool  `json:"ready"`
	Replicas int32 `json:"replicas"`
	// Desired is the number of task Jobs the operator is keeping in flight.
	Desired int32 `json:"desired,omitempty"`
	// Reason is a stable code for the scaling decision: JobPerTask or Capped.
	Reason string `json:"reason,omitempty"`
	// Message is a short explanation safe to show a user.
	Message string `json:"message,omitempty"`
}

// scalingProblem explains a concurrency cap the operator cannot honour, or
// returns "" when the value is workable. An unset replicas means one.
func scalingProblem(scaling *agentv1alpha1.AgentScaling) string {
	if scaling == nil {
		return ""
	}
	if scaling.Replicas != nil && *scaling.Replicas < 0 {
		return "replicas must not be negative"
	}
	return ""
}

// SimpleAgentCreateRequest is used to create a new Agent.
type SimpleAgentCreateRequest struct {
	Name         string            `json:"name"`
	GroupID      string            `json:"groupId"`
	Description  string            `json:"description,omitempty"`
	ImageRef     AgentImageRefInfo `json:"imageRef"`
	LLM          AgentLLMInfo      `json:"llm"`
	Persona      *AgentPersonaInfo `json:"persona,omitempty"`
	EnabledTools []string          `json:"enabledTools,omitempty"`
	Skills       *AgentSkillsInfo  `json:"skills,omitempty"`
	MCP          *AgentMCPRequest  `json:"mcp,omitempty"`
	// Env sets this agent's environment overrides. For an entry marked
	// Secret, an empty Value means "keep the existing value".
	Env []AgentImageEnvVarInfo `json:"env,omitempty"`
	// Replicas caps the number of task Jobs running at once. Defaults to 1;
	// 0 pauses the agent (due tasks stay queued).
	Replicas       *int32                   `json:"replicas,omitempty"`
	Memory         *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud    *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	CustomProvider *AgentCustomProviderInfo `json:"customProvider,omitempty"`
}

// SimpleAgentUpdateRequest is used to update an existing Agent. All fields are optional.
type SimpleAgentUpdateRequest struct {
	Name         *string            `json:"name,omitempty"`
	Description  *string            `json:"description,omitempty"`
	ImageRef     *AgentImageRefInfo `json:"imageRef,omitempty"`
	LLM          *AgentLLMInfo      `json:"llm,omitempty"`
	Persona      *AgentPersonaInfo  `json:"persona,omitempty"`
	EnabledTools *[]string          `json:"enabledTools,omitempty"`
	Skills       *AgentSkillsInfo   `json:"skills,omitempty"`
	MCP          *AgentMCPRequest   `json:"mcp,omitempty"`
	// Env replaces this agent's environment overrides; present-but-empty
	// clears them, so the agent runs on the image defaults again. For an
	// entry marked Secret, an empty Value means "keep the existing value".
	Env *[]AgentImageEnvVarInfo `json:"env,omitempty"`
	// Replicas caps the number of task Jobs running at once; 0 pauses the
	// agent (due tasks stay queued).
	Replicas       *int32                   `json:"replicas,omitempty"`
	Memory         *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud    *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	CustomProvider *AgentCustomProviderInfo `json:"customProvider,omitempty"`
}

// AgentUpdatedAtAnnotation is stamped by the hub on every agent write so the
// API can expose a meaningful updatedAt for "recently updated" UIs.
// Kubernetes objects carry no spec-change timestamp of their own, and all
// user-driven updates flow through the hub, so the hub is the right place to
// track it. A missing annotation falls back to the creation timestamp.
const AgentUpdatedAtAnnotation = "ainsel.dev/updated-at"

func stampAgentUpdatedAt() string {
	return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}

func agentUpdatedAt(a agentv1alpha1.Agent) string {
	if v, ok := a.Annotations[AgentUpdatedAtAnnotation]; ok {
		return v
	}
	return a.CreationTimestamp.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func toSimpleAgentResponse(a agentv1alpha1.Agent, imageDisplayName string) SimpleAgentResponse {
	resp := SimpleAgentResponse{
		ID:          a.Name,
		Name:        a.Spec.DisplayName,
		Description: a.Spec.Description,
		ImageRef:    AgentImageRefInfo{Name: a.Spec.ImageRef.Name, DisplayName: imageDisplayName},
		LLM: AgentLLMInfo{
			Model:         a.Spec.LLM.Model,
			Provider:      a.Spec.LLM.Provider,
			MaxTurns:      a.Spec.LLM.MaxTurns,
			Temperature:   a.Spec.LLM.Temperature,
			Vision:        a.Spec.LLM.Vision,
			ContextWindow: a.Spec.LLM.ContextWindow,
		},
		EnabledTools: a.Spec.EnabledTools,
		UpdatedAt:    agentUpdatedAt(a),
	}

	if a.Spec.Persona.ID != "" {
		resp.Persona = &AgentPersonaInfo{ID: a.Spec.Persona.ID}
	}

	// Skills: nil = inherit from the image (legacy), present = explicit.
	if a.Spec.Skills != nil {
		resp.Skills = &AgentSkillsInfo{Items: a.Spec.Skills.Items}
	}

	// MCP: nil = inherit from the image (legacy), present = explicit.
	if a.Spec.MCP != nil {
		mcpInfo := &AgentMCPInfo{Servers: make([]AgentMCPServerInfo, 0, len(a.Spec.MCP.Servers))}
		for _, srv := range a.Spec.MCP.Servers {
			mcpInfo.Servers = append(mcpInfo.Servers, AgentMCPServerInfo{
				Name:         srv.Name,
				URL:          srv.URL,
				TokenFromEnv: srv.TokenFromEnv,
			})
		}
		resp.MCP = mcpInfo
	}

	// Env overrides: secret values are masked, matching the image env
	// contract, so a response can never expose them.
	if len(a.Spec.Env) > 0 {
		resp.Env = make([]AgentImageEnvVarInfo, 0, len(a.Spec.Env))
		for _, e := range a.Spec.Env {
			info := AgentImageEnvVarInfo{Name: e.Name, Secret: e.Secret}
			if !e.Secret {
				info.Value = e.Value
			}
			resp.Env = append(resp.Env, info)
		}
	}

	// Replicas
	if a.Spec.Scaling != nil {
		resp.Replicas = a.Spec.Scaling.Replicas
	}

	// Pause state is computed from spec at read time, not mirrored from
	// status: the CRD is the source of truth and expiry needs no controller.
	resp.Paused = a.IsPaused(time.Now())
	if resp.Paused && a.Spec.Pause.PausedUntil != nil {
		resp.PausedUntil = a.Spec.Pause.PausedUntil.UTC().Format(time.RFC3339)
	}

	// Memory
	if a.Spec.Memory != nil {
		resp.Memory = &AgentMemoryInfo{
			Enabled:  a.Spec.Memory.Enabled,
			Provider: a.Spec.Memory.Provider,
		}
	}

	// OllamaCloud - note: API key is never returned in responses for security
	if a.Spec.OllamaCloud != nil {
		resp.OllamaCloud = &AgentOllamaCloudInfo{}
	}

	// OpenCode - note: API key is never returned in responses for security
	if a.Spec.OpenCode != nil {
		resp.OpenCode = &AgentOpenCodeInfo{}
	}

	// AlibabaCloud - note: API key is never returned in responses for security
	if a.Spec.AlibabaCloud != nil {
		resp.AlibabaCloud = &AgentAlibabaCloudInfo{}
	}

	// CustomProvider - note: API key is never returned in responses for security
	if a.Spec.CustomProvider != nil {
		resp.CustomProvider = &AgentCustomProviderInfo{
			URL: a.Spec.CustomProvider.URL,
		}
	}

	// Status
	ready := false
	for _, c := range a.Status.Conditions {
		if c.Type == agentv1alpha1.AgentConditionReady && c.Status == metav1.ConditionTrue {
			ready = true
			break
		}
	}
	resp.Status = &SimpleAgentStatus{
		Ready:    ready,
		Replicas: a.Status.Replicas,
	}
	// How many task Jobs the operator is keeping in flight, and why. With no
	// pods running this is the only way a reader can tell a paused agent from
	// one whose Jobs cannot get scheduled.
	if sc := a.Status.Scaling; sc != nil {
		resp.Status.Desired = sc.Desired
		resp.Status.Reason = sc.Reason
		resp.Status.Message = sc.Message
	}

	return resp
}

// validateAgentImageRef checks that refName references an existing AgentImage and that
// all entries in enabled are tools declared by that image. Returns the image's display
// name along with a non-zero status code and error message on failure; returns
// displayName, 0, "" on success.
func (s *Server) validateAgentImageRef(ctx context.Context, refName string, enabled []string) (displayName string, statusCode int, errMessage string) {
	if strings.TrimSpace(refName) == "" {
		return "", http.StatusBadRequest, "imageRef.name is required"
	}
	var img agentv1alpha1.AgentImage
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: refName}, &img); err != nil {
		if apierrors.IsNotFound(err) {
			return "", http.StatusBadRequest, fmt.Sprintf("imageRef.name %q does not match any AgentImage", refName)
		}
		return "", http.StatusInternalServerError, "lookup image: " + err.Error()
	}
	valid := make(map[string]struct{}, len(img.Spec.Tools))
	for _, t := range img.Spec.Tools {
		valid[t.Name] = struct{}{}
	}
	var unknown []string
	for _, t := range enabled {
		if _, ok := valid[t]; !ok {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) > 0 {
		return "", http.StatusBadRequest, "enabledTools not in image: " + strings.Join(unknown, ",")
	}
	return img.Spec.DisplayName, 0, ""
}

// resolveImageDisplayName looks up the AgentImage by resource name and returns
// its Spec.DisplayName. Returns empty string on any lookup error (not found,
// RBAC, network, etc.).
func (s *Server) resolveImageDisplayName(ctx context.Context, imageName string) string {
	var img agentv1alpha1.AgentImage
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: imageName}, &img); err != nil {
		return ""
	}
	return img.Spec.DisplayName
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		s.listAgents(ctx, w, r)
	case http.MethodPost:
		s.createAgent(ctx, w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := extractName(r.URL.Path, "/api/v1/agents/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	// The agent task endpoints (next-task, tasks/{id}[/ack|/nack]) live solely
	// on /api/internal/agents/ (handleInternalAgent). The old /api/v1 aliases
	// are gone: they dispatched into the task handlers ahead of the CRUD
	// requireRead/requireWrite checks below, letting any authenticated user
	// drive another tenant's task queue. 404 any such shape here so a stale
	// agent image gets an honest "not found" instead of a nonsense
	// "agent not found" from the CRUD path.
	if strings.HasSuffix(name, "/next-task") || strings.Contains(name, "/tasks/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// Agent-scoped persona: inline content editing with copy-on-write
	// ownership (see handlers_agent_persona.go).
	if strings.HasSuffix(name, "/persona") {
		s.handleAgentPersona(w, r, strings.TrimSuffix(name, "/persona"))
		return
	}

	// Agent-scoped pause: withhold or restore task delivery (see
	// handlers_agent_pause.go). Write-scoped like any agent config change.
	if strings.HasSuffix(name, "/pause") {
		paused := strings.TrimSuffix(name, "/pause")
		if paused == "" {
			writeError(w, http.StatusBadRequest, "missing agent name")
			return
		}
		if !s.requireWrite(w, r, "agent", paused) {
			return
		}
		s.handleAgentPause(w, r, paused)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if !s.requireRead(w, r, "agent", name) {
			return
		}
		s.getAgent(ctx, w, name)
	case http.MethodPut:
		if !s.requireWrite(w, r, "agent", name) {
			return
		}
		s.updateAgent(ctx, w, r, name)
	case http.MethodDelete:
		if !s.requireWrite(w, r, "agent", name) {
			return
		}
		s.deleteAgent(ctx, w, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleInternalAgent serves agent task endpoints under /api/internal/agents/.
// These paths bypass the OIDC middleware (they don't match /api/v1/) and are
// protected by X-Internal-Token at the handler level. Only task operations
// (next-task, task fetch, ack, nack) are exposed; CRUD operations remain on
// /api/v1/.
//
// This is the sole entry point for the task handlers, and the token check runs
// here, before any sub-route dispatch, so that adding a new task sub-route
// cannot accidentally ship unauthenticated.
func (s *Server) handleInternalAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requireInternalToken(w, r) {
		return
	}
	name := extractName(r.URL.Path, "/api/internal/agents/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	if strings.HasSuffix(name, "/next-task") {
		s.handleAgentNextTask(w, r)
		return
	}
	if strings.Contains(name, "/tasks/") {
		if strings.HasSuffix(name, "/ack") {
			s.handleAgentTaskAck(w, r)
			return
		}
		if strings.HasSuffix(name, "/nack") {
			s.handleAgentTaskNack(w, r)
			return
		}
		// {name}/tasks/{id} without a verb is the jobPerTask fetch.
		s.handleAgentTaskGet(w, r)
		return
	}

	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) listAgents(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	page, err := ParsePageParams(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var list agentv1alpha1.AgentList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sort by resource name for stable pagination across requests. Without an
	// explicit sort the underlying client returns items in map-iteration order
	// (in tests) or arbitrary kube-apiserver order, so successive page= calls
	// could see the same item twice or miss it entirely.
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})

	// Filter to resources the caller can access (own groups + optional public).
	if s.authzStore != nil && !s.callerIsAdmin(r) {
		names := make([]string, len(list.Items))
		for i, a := range list.Items {
			names[i] = a.Name
		}
		set := toAccessSet(s.filterByAccess(r, "agent", names))
		filtered := list.Items[:0]
		for _, a := range list.Items {
			if set[a.Name] {
				filtered = append(filtered, a)
			}
		}
		list.Items = filtered
	}

	// Build image id→displayName map for enrichment. A single namespace-scoped
	// List avoids N+1 Gets per agent.
	imageDisplayNames := make(map[string]string)
	var imageList agentv1alpha1.AgentImageList
	if err := s.client.List(ctx, &imageList, client.InNamespace(s.ns)); err == nil {
		for _, img := range imageList.Items {
			imageDisplayNames[img.Name] = img.Spec.DisplayName
		}
	} else {
		slog.Warn("failed to list AgentImages for display name enrichment", "error", err)
	}

	total := len(list.Items)
	lo, hi := page.Slice(total)
	items := make([]SimpleAgentResponse, 0, hi-lo)
	for _, a := range list.Items[lo:hi] {
		items = append(items, toSimpleAgentResponse(a, imageDisplayNames[a.Spec.ImageRef.Name]))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      items,
		"total":      total,
		"page":       page.Page,
		"pageSize":   page.PageSize,
		"totalPages": page.TotalPages(total),
	})
}

func (s *Server) getAgent(ctx context.Context, w http.ResponseWriter, name string) {
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &agent); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	imageDisplayName := s.resolveImageDisplayName(ctx, agent.Spec.ImageRef.Name)
	writeJSON(w, http.StatusOK, toSimpleAgentResponse(agent, imageDisplayName))
}

// agentValidationInput carries the pre-normalized effective values for the
// validation pipeline shared by createAgent and updateAgent. Callers resolve
// the create-vs-update patch semantics before calling: create always
// validates the image ref and resolves env against a nil existing env;
// update passes the effective image ref/tools, validates the image only when
// either is being changed, and resolves env against the stored spec.
type agentValidationInput struct {
	validateImage bool
	imageRef      string
	enabledTools  []string
	skills        *AgentSkillsInfo
	mcp           *AgentMCPRequest
	resolveEnv    bool
	env           []AgentImageEnvVarInfo
	// existingEnv supplies the values of secret env entries whose request
	// value is empty ("keep the existing value"); nil on create.
	existingEnv []agentv1alpha1.AgentEnvVar
	// groupCheck, when non-nil, runs after image-ref validation and before
	// skills validation. createAgent uses it for the group-membership check
	// so the error precedence (image → group → skills → MCP → env) matches
	// the previous inline sequence; it is the only validation step that
	// needs the request, so it stays caller-side.
	groupCheck func() bool
}

// agentValidationResult carries what the validation pipeline derived; the
// spec assembler consumes it.
type agentValidationResult struct {
	// displayName is the validated image's display name; empty when
	// validateImage was false.
	displayName string
	// mcpSelection is nil when no MCP selection was requested (inherit from
	// the image); non-nil is an explicit selection, possibly with no servers.
	mcpSelection *agentv1alpha1.AgentMCP
	// agentEnv is the resolved env overrides; nil means no overrides, which
	// is also what an empty request list produces (image defaults).
	agentEnv []agentv1alpha1.AgentEnvVar
}

// validateAgentRequest runs the shared validate-then-resolve sequence both
// createAgent and updateAgent used to inline: image ref → (optional group
// check) → enabled skills → MCP selection → env overrides. Each step writes
// its own HTTP error; ok=false means a response has been written and the
// caller must return without further output.
func (s *Server) validateAgentRequest(ctx context.Context, w http.ResponseWriter, in agentValidationInput) (*agentValidationResult, bool) {
	res := &agentValidationResult{}
	if in.validateImage {
		displayName, code, msg := s.validateAgentImageRef(ctx, in.imageRef, in.enabledTools)
		if code != 0 {
			http.Error(w, msg, code)
			return nil, false
		}
		res.displayName = displayName
	}
	if in.groupCheck != nil && !in.groupCheck() {
		return nil, false
	}
	if in.skills != nil {
		if err := s.validateEnabledSkills(ctx, w, in.skills.Items); err != nil {
			return nil, false
		}
	}
	mcpSelection, err := s.resolveMCPRequest(ctx, w, in.mcp)
	if err != nil {
		return nil, false
	}
	res.mcpSelection = mcpSelection
	if in.resolveEnv {
		agentEnv, err := agentEnvFromRequest(w, in.env, in.existingEnv)
		if err != nil {
			return nil, false
		}
		res.agentEnv = agentEnv
	}
	return res, true
}

// toUpdateFields converts a create request into the pointer-shaped field set
// applyAgentRequest consumes, with every field "present" so the assembler
// writes all of them — reproducing create's unconditional assignment against
// a zero spec.
func (r *SimpleAgentCreateRequest) toUpdateFields() *SimpleAgentUpdateRequest {
	return &SimpleAgentUpdateRequest{
		Name:           &r.Name,
		Description:    &r.Description,
		ImageRef:       &r.ImageRef,
		LLM:            &r.LLM,
		Persona:        r.Persona,
		EnabledTools:   &r.EnabledTools,
		Skills:         r.Skills,
		Env:            &r.Env,
		Replicas:       r.Replicas,
		Memory:         r.Memory,
		OllamaCloud:    r.OllamaCloud,
		OpenCode:       r.OpenCode,
		AlibabaCloud:   r.AlibabaCloud,
		CustomProvider: r.CustomProvider,
	}
}

// putAgentSecret stores a provider API key under name, creating the secret or
// replacing an existing one (upsert). Provider keys never live in the CR:
// each becomes a {agent-name}-{suffix}-key Secret and the spec stores only a
// SecretKeySelector. Update needs the replace path to rotate keys; on create
// the name embeds a freshly generated agent id, so the create path always
// takes the Create branch.
func (s *Server) putAgentSecret(ctx context.Context, name, value string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.ns,
		},
		StringData: map[string]string{
			"api-key": value,
		},
	}
	if err := s.client.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		return s.client.Update(ctx, secret)
	}
	return nil
}

// applyAgentRequest assembles the requested fields onto spec, shared by
// createAgent (fresh zero spec, newAgent=true, req from
// SimpleAgentCreateRequest.toUpdateFields) and updateAgent (existing.Spec,
// newAgent=false, req decoded from the update body). The pointer-shaped req
// encodes patch semantics: a nil field means "leave unchanged" on update and
// is never nil on create. newAgent covers the one delta pointer presence
// does not: persona is applied on create even with an empty ID, on update
// only with a non-empty one. res carries the MCP selection and resolved env
// from validateAgentRequest; name is the agent resource name used for
// provider secret names. It returns false after writing an HTTP error.
func (s *Server) applyAgentRequest(ctx context.Context, w http.ResponseWriter, spec *agentv1alpha1.AgentSpec, req *SimpleAgentUpdateRequest, res *agentValidationResult, name string, newAgent bool) bool {
	if req.Name != nil {
		spec.DisplayName = *req.Name
	}
	if req.Description != nil {
		spec.Description = *req.Description
	}
	if req.ImageRef != nil {
		spec.ImageRef = agentv1alpha1.AgentImageRef{Name: req.ImageRef.Name}
	}
	if req.EnabledTools != nil {
		spec.EnabledTools = *req.EnabledTools
	}
	if req.Skills != nil {
		items := make([]string, len(req.Skills.Items))
		copy(items, req.Skills.Items)
		spec.Skills = &agentv1alpha1.AgentSkills{Items: items}
	}
	if res.mcpSelection != nil {
		spec.MCP = res.mcpSelection
	}
	if req.Env != nil {
		// Present-but-empty clears the overrides: the agent then runs on the
		// referenced image's env alone. agentEnvFromRequest returns nil for an
		// empty input list, so create never writes an empty non-nil spec.Env.
		spec.Env = res.agentEnv
	}
	if req.LLM != nil {
		if req.LLM.Model != "" {
			spec.LLM.Model = req.LLM.Model
		}
		if req.LLM.Provider != "" {
			spec.LLM.Provider = req.LLM.Provider
		}
		if req.LLM.MaxTurns != 0 {
			spec.LLM.MaxTurns = req.LLM.MaxTurns
		}
		if req.LLM.Temperature != nil {
			spec.LLM.Temperature = req.LLM.Temperature
		}
		if req.LLM.Vision != nil {
			spec.LLM.Vision = req.LLM.Vision
		}
		if req.LLM.ContextWindow != nil {
			spec.LLM.ContextWindow = req.LLM.ContextWindow
		}
	}
	if req.Persona != nil && (newAgent || req.Persona.ID != "") {
		spec.Persona = agentv1alpha1.AgentPersona{ID: req.Persona.ID}
	}
	if req.Replicas != nil {
		if spec.Scaling == nil {
			spec.Scaling = &agentv1alpha1.AgentScaling{}
		}
		spec.Scaling.Replicas = req.Replicas
	}
	if msg := scalingProblem(spec.Scaling); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return false
	}
	if req.Memory != nil {
		spec.Memory = &agentv1alpha1.AgentMemory{
			Enabled:  req.Memory.Enabled,
			Provider: req.Memory.Provider,
		}
	}

	// Provider API keys: a non-empty key stores a secret and points the spec
	// at it. The secret names embed the agent name, so repeated updates
	// rotate the value in place. API keys are never returned in responses.
	if req.OllamaCloud != nil && req.OllamaCloud.APIKey != "" {
		secretName := fmt.Sprintf("%s-llm-key", name)
		if err := s.putAgentSecret(ctx, secretName, req.OllamaCloud.APIKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create Ollama Cloud secret: "+err.Error())
			return false
		}
		spec.OllamaCloud = &agentv1alpha1.AgentOllamaCloud{
			APIKeySecretRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  "api-key",
			},
		}
	}
	if req.OpenCode != nil && req.OpenCode.APIKey != "" {
		secretName := fmt.Sprintf("%s-opencode-key", name)
		if err := s.putAgentSecret(ctx, secretName, req.OpenCode.APIKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create OpenCode secret: "+err.Error())
			return false
		}
		spec.OpenCode = &agentv1alpha1.AgentOpenCode{
			APIKeySecretRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  "api-key",
			},
		}
	}
	if req.AlibabaCloud != nil && req.AlibabaCloud.APIKey != "" {
		secretName := fmt.Sprintf("%s-alibaba-key", name)
		if err := s.putAgentSecret(ctx, secretName, req.AlibabaCloud.APIKey); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create Alibaba Token Plan secret: "+err.Error())
			return false
		}
		spec.AlibabaCloud = &agentv1alpha1.AgentAlibabaCloud{
			APIKeySecretRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  "api-key",
			},
		}
	}

	// Custom provider (URL and optional API key). When the agent has no
	// existing custom provider, URL is required: otherwise we'd create an
	// orphan API-key secret while leaving spec.CustomProvider nil (and
	// dereferencing it crashed the handler). On create the spec starts
	// empty, so this rule reproduces create's former "URL required whenever
	// customProvider is present".
	if req.CustomProvider != nil {
		if spec.CustomProvider == nil {
			if req.CustomProvider.URL == "" {
				writeError(w, http.StatusBadRequest, "custom provider URL is required")
				return false
			}
			spec.CustomProvider = &agentv1alpha1.AgentCustomProvider{}
		}
		if req.CustomProvider.URL != "" {
			spec.CustomProvider.URL = req.CustomProvider.URL
		}
		if req.CustomProvider.APIKey != "" {
			secretName := fmt.Sprintf("%s-custom-provider-key", name)
			if err := s.putAgentSecret(ctx, secretName, req.CustomProvider.APIKey); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create custom provider secret: "+err.Error())
				return false
			}
			spec.CustomProvider.APIKeySecretRef = &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  "api-key",
			}
		}
	}
	return true
}

func (s *Server) createAgent(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var req SimpleAgentCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate group membership for resource creation. The check runs
	// between image-ref and skills validation to keep the error precedence
	// of the previous inline sequence.
	in := agentValidationInput{
		validateImage: true,
		imageRef:      req.ImageRef.Name,
		enabledTools:  req.EnabledTools,
		skills:        req.Skills,
		mcp:           req.MCP,
		resolveEnv:    true,
		env:           req.Env,
	}
	if s.authzChecker != nil {
		in.groupCheck = func() bool {
			if req.GroupID == "" {
				writeError(w, http.StatusBadRequest, "groupId is required")
				return false
			}
			return s.requireGroupWrite(w, r, req.GroupID)
		}
	}
	res, ok := s.validateAgentRequest(ctx, w, in)
	if !ok {
		return
	}

	id := generateID("a")

	agent := agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: s.ns,
			Annotations: map[string]string{
				AgentUpdatedAtAnnotation: stampAgentUpdatedAt(),
			},
		},
	}
	agent.APIVersion = "ainsel.dev/v1alpha1"
	agent.Kind = "Agent"

	if !s.applyAgentRequest(ctx, w, &agent.Spec, req.toUpdateFields(), res, id, true) {
		return
	}

	if err := s.client.Create(ctx, &agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Assign the agent to the creator's chosen group.
	if s.authzStore != nil {
		groupID := req.GroupID
		if groupID == "" {
			groupID = "legacy"
		}
		if err := s.authzStore.SetResourceGroup(r.Context(), "agent", id, groupID, false); err != nil {
			slog.Error("set resource group on create", "error", err, "resource", id)
		}
	}

	writeJSON(w, http.StatusCreated, toSimpleAgentResponse(agent, res.displayName))
}

func (s *Server) updateAgent(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var existing agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &existing); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req SimpleAgentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate imageRef/enabledTools if either is being changed, against the
	// effective combination of stored and requested values.
	effectiveRef := existing.Spec.ImageRef.Name
	if req.ImageRef != nil {
		effectiveRef = req.ImageRef.Name
	}
	effectiveTools := existing.Spec.EnabledTools
	if req.EnabledTools != nil {
		effectiveTools = *req.EnabledTools
	}
	in := agentValidationInput{
		validateImage: req.ImageRef != nil || req.EnabledTools != nil,
		imageRef:      effectiveRef,
		enabledTools:  effectiveTools,
		skills:        req.Skills,
		mcp:           req.MCP,
		// Env overrides are resolved against the current spec so a secret
		// entry submitted with an empty value keeps its stored value.
		resolveEnv:  req.Env != nil,
		existingEnv: existing.Spec.Env,
	}
	if req.Env != nil {
		in.env = *req.Env
	}
	res, ok := s.validateAgentRequest(ctx, w, in)
	if !ok {
		return
	}

	if !s.applyAgentRequest(ctx, w, &existing.Spec, &req, res, name, false) {
		return
	}

	// Stamp the hub-managed updated-at annotation so the list API can surface
	// recently updated agents.
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	existing.Annotations[AgentUpdatedAtAnnotation] = stampAgentUpdatedAt()

	if err := s.client.Update(ctx, &existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Re-pointing away from the agent's own persona orphans it — owned
	// personas are invisible to the library, so reclaim it here.
	if req.Persona != nil && req.Persona.ID != "" {
		s.cleanupOwnedPersona(ctx, name, req.Persona.ID)
	}

	// Resolve image display name for the response. If we already validated the
	// image ref above, reuse that display name; otherwise look it up.
	imageDisplayName := res.displayName
	if imageDisplayName == "" && req.ImageRef == nil {
		imageDisplayName = s.resolveImageDisplayName(ctx, existing.Spec.ImageRef.Name)
	}
	writeJSON(w, http.StatusOK, toSimpleAgentResponse(existing, imageDisplayName))
}

func (s *Server) deleteAgent(ctx context.Context, w http.ResponseWriter, name string) {
	agent := &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.ns},
	}
	if err := s.client.Delete(ctx, agent); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	// Reclaim the agent's owned persona, if any: it is invisible to the
	// library, so nothing else would ever delete it.
	s.cleanupOwnedPersona(ctx, name, "")

	// Clean up ownership record.
	if s.authzStore != nil {
		if err := s.authzStore.DeleteResourceGroup(ctx, "agent", name); err != nil {
			slog.Error("delete ownership", "error", err, "resource", name)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
