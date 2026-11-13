package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/api/llmprovider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	validation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentImageRefInfo identifies which AgentImage an Agent uses.
type AgentImageRefInfo struct {
	// ID is the AgentImage's API id (its resource name) — the route key the
	// console links the reference onto. Same value as Name today; carried
	// explicitly so views never have to assume it.
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
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
		httpx.WriteError(w, http.StatusServiceUnavailable, "mcp service not configured")
		return nil, fmt.Errorf("mcp service not configured")
	}
	mcp := &agentv1alpha1.AgentMCP{Servers: make([]agentv1alpha1.AgentMCPServer, 0, len(req.Servers))}
	for _, name := range req.Servers {
		server, err := s.mcp.Get(ctx, name)
		if err != nil {
			if errors.Is(err, mcpservers.ErrNotFound) {
				httpx.WriteError(w, http.StatusBadRequest, "mcp server not found: "+name)
				return nil, err
			}
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
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
			httpx.WriteError(w, http.StatusBadRequest, "env name is required")
			return nil, fmt.Errorf("env name is required")
		}
		if errs := validation.IsEnvVarName(name); len(errs) > 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid env name "+name+": "+errs[0])
			return nil, fmt.Errorf("invalid env name %q", name)
		}
		if seen[name] {
			// The operator merges by name, so duplicates would silently keep
			// only one value. Reject instead of guessing.
			httpx.WriteError(w, http.StatusBadRequest, "duplicate env name: "+name)
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
	// Skills is this agent's own skill selection. The referenced image
	// carries none: skills are chosen per agent, so an empty list means the
	// agent runs with no skills.
	Skills []string      `json:"skills,omitempty"`
	MCP    *AgentMCPInfo `json:"mcp,omitempty"`
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
	PausedUntil string `json:"pausedUntil,omitempty"`
	// WorkingHours is the weekly window inside which the hub may hand this agent
	// work. Absent means it may be handed work at any hour.
	WorkingHours *AgentWorkingHoursInfo `json:"workingHours,omitempty"`
	// HoursOpen reports whether the agent may be handed work right now - true for
	// an agent with no window. Hours outside a window are not an error: the tasks
	// queue and drain when the window opens, so a console should say when the
	// agent will get to them rather than showing it as broken.
	HoursOpen bool `json:"hoursOpen"`
	// HoursClosesAt is when the open window ends (RFC3339). Empty when no window
	// is configured or the agent is outside it.
	HoursClosesAt string `json:"hoursClosesAt,omitempty"`
	// HoursNextOpenAt is when the window next opens (RFC3339) while the agent is
	// outside it - when queued work starts moving.
	HoursNextOpenAt string                   `json:"hoursNextOpenAt,omitempty"`
	Memory          *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud     *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode        *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud    *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	OpenAI          *AgentAPIKeyInfo         `json:"openAI,omitempty"`
	Anthropic       *AgentAPIKeyInfo         `json:"anthropic,omitempty"`
	Google          *AgentAPIKeyInfo         `json:"google,omitempty"`
	CustomProvider  *AgentCustomProviderInfo `json:"customProvider,omitempty"`
	Status          *SimpleAgentStatus       `json:"status,omitempty"`
	UpdatedAt       string                   `json:"updatedAt,omitempty"`
	// ManagedBy names the external system that authors this agent (spec.managedBy).
	// Any non-empty value other than "user" means the console renders the agent
	// read-only and the hub refuses every mutation with 409. Read-only: the
	// create/update request shapes have no managedBy field — only the external
	// manager (kubectl/GitOps/Helm) sets it.
	ManagedBy string `json:"managedBy,omitempty"`
	// GroupID is the authz group this agent belongs to. Populated on the list
	// endpoint (batched lookup); empty elsewhere and when authz is disabled.
	GroupID string `json:"groupId,omitempty"`
	// GroupName is the display name of GroupID, for roster rendering. Falls
	// back to empty when the group cannot be resolved; the UI shows the id.
	GroupName string `json:"groupName,omitempty"`
}

// AgentLLMInfo is the REST representation of an Agent's LLM backend: model,
// provider, and the sampling/capability overrides written to the agent's
// generated pi models.json.
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
	// ModelLicense mirrors spec.llm.modelLicense: the licence the named model
	// arrives under, as reviewed by whoever set it. Mirrored verbatim — the
	// hub never verifies the value (docs/model-licensing.md).
	ModelLicense string `json:"modelLicense,omitempty"`
}

// AgentPersonaInfo is the REST representation of an Agent's persona reference.
// ID points at a persona managed by the hub at /api/v1/personas.
type AgentPersonaInfo struct {
	ID string `json:"id"`
}

// AgentPersonaCreateInfo is the persona block of a create request: either the
// id of a shared persona template, or inline instructions (Text) the hub
// stores as a persona owned by the new agent — the same agent-owned persona
// an inline edit forks later (see handlers_agent_persona.go). Exactly one of
// the two may be set.
type AgentPersonaCreateInfo struct {
	ID   string `json:"id,omitempty"`
	Text string `json:"text,omitempty"`
}

// AgentMemoryInfo is the REST representation of an Agent's memory backend.
type AgentMemoryInfo struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
}

// AgentAPIKeyInfo is the REST representation of an API-key provider block
// (Ollama Cloud, OpenCode, Alibaba Token Plan, OpenAI, Anthropic, Google).
type AgentAPIKeyInfo struct {
	// APIKey is the provider API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
	// AuthMethod is only read on the openAI block: "APIKey" (default) or
	// "ChatGPTSignIn". Empty on a write leaves the stored method unchanged.
	AuthMethod string `json:"authMethod,omitempty"`
	// KeyStored is response-only: whether an API key is stored for this
	// provider. A block can exist without one (a base URL, or ChatGPT
	// sign-in), so presence alone does not say.
	KeyStored bool `json:"keyStored"`
}

// AgentOllamaCloudInfo is the REST representation of Ollama Cloud credentials.
type AgentOllamaCloudInfo = AgentAPIKeyInfo

// AgentOpenCodeInfo is the REST representation of OpenCode credentials.
type AgentOpenCodeInfo = AgentAPIKeyInfo

// AgentAlibabaCloudInfo is the REST representation of Alibaba Token Plan
// credentials.
type AgentAlibabaCloudInfo = AgentAPIKeyInfo

// AgentCustomProviderInfo is the REST representation of a custom LLM provider.
type AgentCustomProviderInfo struct {
	// URL is the custom LLM API base URL.
	URL string `json:"url"`
	// APIKey is the custom API key. If provided, the backend will create
	// a secret to store it. On response, this field is always empty for security.
	APIKey string `json:"apiKey,omitempty"`
}

// SimpleAgentStatus is the REST representation of an Agent's runtime status,
// carrying what the operator observed about its Deployment or task Jobs.
type SimpleAgentStatus struct {
	Ready    bool  `json:"ready"`
	Replicas int32 `json:"replicas"`
	// Desired is the number of task Jobs the operator is keeping in flight.
	Desired int32 `json:"desired,omitempty"`
	// Reason is a stable code for the scaling decision: JobPerTask or Capped.
	Reason string `json:"reason,omitempty"`
	// Message is a short explanation safe to show a user.
	Message string `json:"message,omitempty"`
	// SignInRequired mirrors the SignInRequired condition: the agent uses
	// ChatGPT sign-in and runs nothing until someone signs in.
	SignInRequired bool `json:"signInRequired,omitempty"`
	// SignInReason is the condition's reason: NotSignedIn or SignInExpired
	// while required, SignedIn otherwise. Empty for API-key agents.
	SignInReason string `json:"signInReason,omitempty"`
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

// signInScalingProblem refuses more than one concurrent run for a ChatGPT
// sign-in agent: its credential volume is ReadWriteOnce and two pods must not
// race on a rotating refresh token. The value is rejected, never changed.
func signInScalingProblem(spec *agentv1alpha1.AgentSpec) string {
	a := agentv1alpha1.Agent{Spec: *spec}
	if !a.UsesChatGPTSignIn() || spec.Scaling == nil || spec.Scaling.Replicas == nil {
		return ""
	}
	if *spec.Scaling.Replicas > 1 {
		return "ChatGPT sign-in runs one task at a time; set replicas to 1"
	}
	return ""
}

// SimpleAgentCreateRequest is used to create a new Agent.
type SimpleAgentCreateRequest struct {
	Name         string                  `json:"name"`
	GroupID      string                  `json:"groupId"`
	Description  string                  `json:"description,omitempty"`
	ImageRef     AgentImageRefInfo       `json:"imageRef"`
	LLM          AgentLLMInfo            `json:"llm"`
	Persona      *AgentPersonaCreateInfo `json:"persona,omitempty"`
	EnabledTools []string                `json:"enabledTools,omitempty"`
	Skills       []string                `json:"skills,omitempty"`
	MCP          *AgentMCPRequest        `json:"mcp,omitempty"`
	// Env sets this agent's environment overrides. For an entry marked
	// Secret, an empty Value means "keep the existing value".
	Env []AgentImageEnvVarInfo `json:"env,omitempty"`
	// Replicas caps the number of task Jobs running at once. Defaults to 1;
	// 0 pauses the agent (due tasks stay queued).
	Replicas *int32 `json:"replicas,omitempty"`
	// WorkingHours confines delivery to a weekly window in an IANA timezone; work
	// outside it stays queued until the window opens. Absent means any hour.
	WorkingHours   *AgentWorkingHoursInfo   `json:"workingHours,omitempty"`
	Memory         *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud    *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	OpenAI         *AgentAPIKeyInfo         `json:"openAI,omitempty"`
	Anthropic      *AgentAPIKeyInfo         `json:"anthropic,omitempty"`
	Google         *AgentAPIKeyInfo         `json:"google,omitempty"`
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
	// Skills replaces this agent's skill selection; present-but-empty clears
	// it, so the agent runs with no skills.
	Skills *[]string        `json:"skills,omitempty"`
	MCP    *AgentMCPRequest `json:"mcp,omitempty"`
	// Env replaces this agent's environment overrides; present-but-empty
	// clears them, so the agent runs on the image defaults again. For an
	// entry marked Secret, an empty Value means "keep the existing value".
	Env *[]AgentImageEnvVarInfo `json:"env,omitempty"`
	// Replicas caps the number of task Jobs running at once; 0 pauses the
	// agent (due tasks stay queued).
	Replicas *int32 `json:"replicas,omitempty"`
	// WorkingHours confines delivery to a weekly window; work outside it stays
	// queued until the window opens. Absent leaves it unchanged; present-but-empty
	// removes the window, so the agent may run at any hour again.
	WorkingHours   *AgentWorkingHoursInfo   `json:"workingHours,omitempty"`
	Memory         *AgentMemoryInfo         `json:"memory,omitempty"`
	OllamaCloud    *AgentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *AgentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *AgentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	OpenAI         *AgentAPIKeyInfo         `json:"openAI,omitempty"`
	Anthropic      *AgentAPIKeyInfo         `json:"anthropic,omitempty"`
	Google         *AgentAPIKeyInfo         `json:"google,omitempty"`
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
		ManagedBy:   a.Spec.ManagedBy,
		ImageRef:    AgentImageRefInfo{ID: a.Spec.ImageRef.Name, Name: a.Spec.ImageRef.Name, DisplayName: imageDisplayName},
		LLM: AgentLLMInfo{
			Model:         a.Spec.LLM.Model,
			Provider:      a.Spec.LLM.Provider,
			MaxTurns:      a.Spec.LLM.MaxTurns,
			Temperature:   a.Spec.LLM.Temperature,
			Vision:        a.Spec.LLM.Vision,
			ContextWindow: a.Spec.LLM.ContextWindow,
			ModelLicense:  a.Spec.LLM.ModelLicense,
		},
		EnabledTools: a.Spec.EnabledTools,
		UpdatedAt:    agentUpdatedAt(a),
	}

	if a.Spec.Persona.ID != "" {
		resp.Persona = &AgentPersonaInfo{ID: a.Spec.Persona.ID}
	}

	// Skills is the agent's own selection; empty means no skills.
	resp.Skills = a.Spec.Skills

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

	// Working hours, computed the same way: the CRD is the source of truth and a
	// window that opens or closes needs no controller to be reported honestly. An
	// unreadable window reads as open, because that is what the delivery path does -
	// the console must not show a hold that is not real.
	resp.WorkingHours = workingHoursFromSpec(a.Spec.WorkingHours)
	open, next, hoursErr := a.WorkingHoursState(time.Now())
	if hoursErr != nil {
		open = true
	}
	resp.HoursOpen = open
	if a.Spec.WorkingHours != nil && !next.IsZero() {
		stamp := next.UTC().Format(time.RFC3339)
		if open {
			resp.HoursClosesAt = stamp
		} else {
			resp.HoursNextOpenAt = stamp
		}
	}

	// Memory
	if a.Spec.Memory != nil {
		resp.Memory = &AgentMemoryInfo{
			Enabled:  a.Spec.Memory.Enabled,
			Provider: a.Spec.Memory.Provider,
		}
	}

	// API-key provider blocks: presence and whether a key is stored — the
	// key itself is never returned.
	for _, f := range apiKeyProviderFields {
		if p, ok := llmprovider.Lookup(f.id); ok {
			if cred, present := p.Credential(&a.Spec); present {
				*f.response(&resp) = &AgentAPIKeyInfo{KeyStored: cred.APIKeySecretRef != nil}
			}
		}
	}
	if resp.OpenAI != nil {
		resp.OpenAI.AuthMethod = string(a.Spec.OpenAI.AuthMethod)
		if resp.OpenAI.AuthMethod == "" {
			resp.OpenAI.AuthMethod = string(agentv1alpha1.OpenAIAuthMethodAPIKey)
		}
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
	if c := apimeta.FindStatusCondition(a.Status.Conditions, agentv1alpha1.AgentConditionSignInRequired); c != nil {
		resp.Status.SignInRequired = c.Status == metav1.ConditionTrue
		resp.Status.SignInReason = c.Reason
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
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := extractName(r.URL.Path, "/api/v1/agents/")
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	// The agent task endpoints (tasks/{id}[/ack|/nack]) live solely
	// on /api/internal/agents/ (handleInternalAgent). The old /api/v1 aliases
	// are gone: they dispatched into the task handlers ahead of the CRUD
	// requireRead/requireWrite checks below, letting any authenticated user
	// drive another tenant's task queue. 404 any such shape here so a stale
	// agent image gets an honest "not found" instead of a nonsense
	// "agent not found" from the CRUD path.
	if strings.HasSuffix(name, "/next-task") || strings.Contains(name, "/tasks/") {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	// Agent-scoped persona: inline content editing with copy-on-write
	// ownership (see handlers_agent_persona.go).
	if strings.HasSuffix(name, "/persona") {
		s.handleAgentPersona(w, r, strings.TrimSuffix(name, "/persona"))
		return
	}

	// ChatGPT sign-in: start, follow, finish or cancel a sign-in session.
	// Write-scoped: anyone who can edit the agent may sign it in.
	if before, rest, ok := strings.Cut(name, "/chatgpt-sign-in"); ok && (rest == "" || rest == "/redirect") {
		if before == "" {
			httpx.WriteError(w, http.StatusBadRequest, "missing agent name")
			return
		}
		if !s.requireWrite(w, r, "agent", before) {
			return
		}
		s.handleAgentSignIn(w, r, before, rest == "/redirect")
		return
	}

	// Agent-scoped pause: withhold or restore task delivery (see
	// handlers_agent_pause.go). Write-scoped like any agent config change.
	if strings.HasSuffix(name, "/pause") {
		paused := strings.TrimSuffix(name, "/pause")
		if paused == "" {
			httpx.WriteError(w, http.StatusBadRequest, "missing agent name")
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
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleInternalAgent serves agent task endpoints under /api/internal/agents/.
// These paths bypass the OIDC middleware (they don't match /api/v1/) and are
// protected by X-Internal-Token at the handler level. Only task operations
// (task fetch, ack, nack) are exposed; CRUD operations remain on
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
		httpx.WriteError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	if strings.Contains(name, "/chatgpt-sign-in/") {
		s.handleInternalSignIn(w, r, name)
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

	httpx.WriteError(w, http.StatusNotFound, "not found")
}

func (s *Server) listAgents(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	page, err := ParsePageParams(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	var list agentv1alpha1.AgentList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sort by resource name for stable pagination across requests. Without an
	// explicit sort the underlying client returns items in map-iteration order
	// (in tests) or arbitrary kube-apiserver order, so successive page= calls
	// could see the same item twice or miss it entirely.
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})

	// Filter to resources the caller can access (own groups + optional
	// public), then narrow to an explicit groupId if the caller asked for
	// one. Both filters run before pagination so Pager totals describe the
	// filtered set, and a group filter can never widen what authz scoped out.
	groupID := strings.TrimSpace(r.URL.Query().Get("groupId"))
	if s.authzStore != nil {
		names := make([]string, len(list.Items))
		for i, a := range list.Items {
			names[i] = a.Name
		}
		if !s.callerIsAdmin(r) {
			names = s.filterByAccess(r, "agent", names)
		}
		if groupID != "" {
			inGroup, err := s.authzStore.ListResourcesByGroups(r.Context(), "agent", []string{groupID}, false)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			names = intersectNames(names, inGroup)
		}
		set := toAccessSet(names)
		filtered := list.Items[:0]
		for _, a := range list.Items {
			if set[a.Name] {
				filtered = append(filtered, a)
			}
		}
		list.Items = filtered
	}

	// Case-insensitive substring filter on display name, resource name and
	// description. Applied before pagination for the same reason as the
	// group filter: paging over an unfiltered set would drop matches.
	if search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search"))); search != "" {
		filtered := list.Items[:0]
		for _, a := range list.Items {
			if strings.Contains(strings.ToLower(a.Spec.DisplayName), search) ||
				strings.Contains(strings.ToLower(a.Name), search) ||
				strings.Contains(strings.ToLower(a.Spec.Description), search) {
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

	// Build agent name→group and group id→name maps for the Group column.
	// Two batched store calls mirror the image display-name map above —
	// never one Get per row.
	agentGroupIDs := make(map[string]string)
	groupNames := make(map[string]string)
	if s.authzStore != nil {
		if m, err := s.authzStore.ListResourceGroupsByType(r.Context(), "agent"); err == nil {
			agentGroupIDs = m
		} else {
			slog.Warn("failed to list agent resource groups for group enrichment", "error", err)
		}
		if gs, err := s.authzStore.ListGroups(r.Context()); err == nil {
			for _, g := range gs {
				groupNames[g.ID] = g.Name
			}
		} else {
			slog.Warn("failed to list groups for group-name enrichment", "error", err)
		}
	}

	items := make([]SimpleAgentResponse, 0, hi-lo)
	for _, a := range list.Items[lo:hi] {
		item := toSimpleAgentResponse(a, imageDisplayNames[a.Spec.ImageRef.Name])
		if gid := agentGroupIDs[a.Name]; gid != "" {
			item.GroupID = gid
			item.GroupName = groupNames[gid]
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
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
		httpx.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	imageDisplayName := s.resolveImageDisplayName(ctx, agent.Spec.ImageRef.Name)
	httpx.WriteJSON(w, http.StatusOK, toSimpleAgentResponse(agent, imageDisplayName))
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
	skills        []string
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

// validateAgentSkills checks that every skill ID in ids exists in the skills
// service. Skills are selected on the agent, so this guards the agent write
// path only — an AgentImage offers no skills to select. Returns a non-nil
// error (already written to w) when any ID is unknown.
func (s *Server) validateAgentSkills(ctx context.Context, w http.ResponseWriter, ids []string) error {
	if len(ids) == 0 || s.skills == nil {
		return nil
	}
	for _, id := range ids {
		if _, err := s.skills.Get(ctx, id); err != nil {
			if errors.Is(err, skills.ErrNotFound) {
				httpx.WriteError(w, http.StatusBadRequest, "skill not found: "+id)
				return err
			}
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return err
		}
	}
	return nil
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
	if len(in.skills) > 0 {
		if err := s.validateAgentSkills(ctx, w, in.skills); err != nil {
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

// personaRef is the template reference of a create request. Inline
// instructions carry no id yet: createAgent fills it in once the owned
// persona exists.
func (r *SimpleAgentCreateRequest) personaRef() *AgentPersonaInfo {
	if r.Persona == nil {
		return nil
	}
	return &AgentPersonaInfo{ID: r.Persona.ID}
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
		Persona:        r.personaRef(),
		EnabledTools:   &r.EnabledTools,
		Skills:         &r.Skills,
		Env:            &r.Env,
		Replicas:       r.Replicas,
		WorkingHours:   r.WorkingHours,
		Memory:         r.Memory,
		OllamaCloud:    r.OllamaCloud,
		OpenCode:       r.OpenCode,
		AlibabaCloud:   r.AlibabaCloud,
		OpenAI:         r.OpenAI,
		Anthropic:      r.Anthropic,
		Google:         r.Google,
		CustomProvider: r.CustomProvider,
	}
}

// providerProblem validates a requested spec.llm.provider against the
// llmprovider registry, so a REST caller gets a 400 instead of an agent the
// operator marks InvalidLLMProvider. A new agent must name a provider; on an
// update an empty provider means "leave unchanged".
func providerProblem(provider string, newAgent bool) string {
	valid := strings.Join(llmprovider.IDs(), ", ")
	if provider == "" {
		if newAgent {
			return "llm.provider is required: one of " + valid
		}
		return ""
	}
	if _, ok := llmprovider.Lookup(provider); !ok {
		return fmt.Sprintf("llm.provider %q is unknown: must be one of %s", provider, valid)
	}
	return ""
}

// apiKeyProviderFields ties each API-key provider in the llmprovider registry
// to its field on the write request and on the response. The custom provider
// is not listed: it carries a URL and keeps its own handling.
var apiKeyProviderFields = []struct {
	id       string
	request  func(*SimpleAgentUpdateRequest) *AgentAPIKeyInfo
	response func(*SimpleAgentResponse) **AgentAPIKeyInfo
}{
	{agentv1alpha1.AgentLLMProviderOllamaCloud,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.OllamaCloud },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.OllamaCloud }},
	{agentv1alpha1.AgentLLMProviderOpenCode,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.OpenCode },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.OpenCode }},
	{agentv1alpha1.AgentLLMProviderAlibabaCloud,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.AlibabaCloud },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.AlibabaCloud }},
	{agentv1alpha1.AgentLLMProviderOpenAI,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.OpenAI },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.OpenAI }},
	{agentv1alpha1.AgentLLMProviderAnthropic,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.Anthropic },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.Anthropic }},
	{agentv1alpha1.AgentLLMProviderGoogle,
		func(r *SimpleAgentUpdateRequest) *AgentAPIKeyInfo { return r.Google },
		func(r *SimpleAgentResponse) **AgentAPIKeyInfo { return &r.Google }},
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
	// Cloned, not aliased: the spec must not share a backing array with the
	// decoded request, matching what the removed image-inheritance code did.
	if req.EnabledTools != nil {
		spec.EnabledTools = slices.Clone(*req.EnabledTools)
	}
	if req.Skills != nil {
		spec.Skills = slices.Clone(*req.Skills)
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
	provider := ""
	if req.LLM != nil {
		provider = req.LLM.Provider
	}
	if msg := providerProblem(provider, newAgent); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return false
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
		if req.LLM.ModelLicense != "" {
			spec.LLM.ModelLicense = req.LLM.ModelLicense
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
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return false
	}
	// Working hours: a window the hub cannot evaluate is rejected here rather than
	// accepted and failed open at delivery time, where the reason would be a log
	// line nobody reads.
	if req.WorkingHours != nil {
		if req.WorkingHours.blank() {
			spec.WorkingHours = nil
		} else {
			wh := req.WorkingHours.toSpecWorkingHours()
			if msg := workingHoursProblem(wh); msg != "" {
				httpx.WriteError(w, http.StatusBadRequest, msg)
				return false
			}
			spec.WorkingHours = wh
		}
	}
	if req.Memory != nil {
		spec.Memory = &agentv1alpha1.AgentMemory{
			Enabled:  req.Memory.Enabled,
			Provider: req.Memory.Provider,
		}
	}

	// OpenAI auth method. Validated before any key is stored, so a rejected
	// request leaves no orphan secret behind.
	if req.OpenAI != nil && req.OpenAI.AuthMethod != "" {
		method := agentv1alpha1.OpenAIAuthMethod(req.OpenAI.AuthMethod)
		if method != agentv1alpha1.OpenAIAuthMethodAPIKey && method != agentv1alpha1.OpenAIAuthMethodChatGPTSignIn {
			httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("openAI.authMethod %q is unknown: must be APIKey or ChatGPTSignIn", method))
			return false
		}
		if method == agentv1alpha1.OpenAIAuthMethodChatGPTSignIn && req.OpenAI.APIKey != "" {
			httpx.WriteError(w, http.StatusBadRequest, "openAI: send an API key or choose ChatGPT sign-in, not both")
			return false
		}
		if spec.OpenAI == nil {
			spec.OpenAI = &agentv1alpha1.AgentOpenAI{}
		}
		spec.OpenAI.AuthMethod = method
	}
	if msg := signInScalingProblem(spec); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return false
	}

	// Provider API keys: a non-empty key stores a secret and points the spec
	// at it; a blank or absent key leaves the stored block untouched. The
	// secret names embed the agent name, so repeated updates rotate the value
	// in place. API keys are never returned in responses.
	for _, f := range apiKeyProviderFields {
		info := f.request(req)
		if info == nil || info.APIKey == "" {
			continue
		}
		p, _ := llmprovider.Lookup(f.id)
		secretName := name + p.HubSecretSuffix
		if err := s.putAgentSecret(ctx, secretName, info.APIKey); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "failed to create "+p.Label+" secret: "+err.Error())
			return false
		}
		p.SetCredential(spec, llmprovider.Credential{
			APIKeySecretRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  "api-key",
			},
		})
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
				httpx.WriteError(w, http.StatusBadRequest, "custom provider URL is required")
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
				httpx.WriteError(w, http.StatusInternalServerError, "failed to create custom provider secret: "+err.Error())
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
	if !decodeJSONBody(w, r, &req) {
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
				httpx.WriteError(w, http.StatusBadRequest, "groupId is required")
				return false
			}
			return s.requireGroupWrite(w, r, req.GroupID)
		}
	}
	inlinePersona := req.Persona != nil && req.Persona.Text != ""
	if inlinePersona && req.Persona.ID != "" {
		httpx.WriteError(w, http.StatusBadRequest, "persona: set either id or text, not both")
		return
	}
	if inlinePersona && s.personas == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "persona service not configured")
		return
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

	// Inline instructions become a persona owned by the new agent. It is
	// written before the CR because spec.persona.id is required, and
	// reclaimed if the CR is refused so no invisible orphan is left behind.
	var ownedPersona *personas.Persona
	if inlinePersona {
		text := req.Persona.Text
		p, err := s.personas.EnsureOwned(ctx, id, defaultOwnedPersonaName(&agent), personas.UpdateRequest{Text: &text})
		if err != nil {
			writePersonaServiceError(w, err)
			return
		}
		ownedPersona = p
		agent.Spec.Persona = agentv1alpha1.AgentPersona{ID: p.ID}
	}

	if err := s.client.Create(ctx, &agent); err != nil {
		if ownedPersona != nil {
			if derr := s.personas.DeleteOwned(ctx, ownedPersona.ID, id); derr != nil {
				slog.Error("reclaim persona of refused agent", "error", derr, "agent", id, "persona", ownedPersona.ID)
			}
		}
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
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

	httpx.WriteJSON(w, http.StatusCreated, toSimpleAgentResponse(agent, res.displayName))
}

func (s *Server) updateAgent(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var existing agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &existing); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	// Externally managed agents have exactly one author; the hub refuses every
	// spec mutation, including pause (spec.pause would be reverted by a
	// GitOps reconciler anyway).
	if isManagedCR(existing.Spec.ManagedBy) {
		writeManagedConflict(w, "agent", existing.Spec.ManagedBy)
		return
	}
	var req SimpleAgentUpdateRequest
	if !decodeJSONBody(w, r, &req) {
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
	// Skills need no image check — the skill library is the only source — and
	// an absent selection is not a change, so only a requested selection is
	// validated (a stored one may name a skill deleted since).
	var requestedSkills []string
	if req.Skills != nil {
		requestedSkills = *req.Skills
	}
	in := agentValidationInput{
		validateImage: req.ImageRef != nil || req.EnabledTools != nil,
		imageRef:      effectiveRef,
		enabledTools:  effectiveTools,
		skills:        requestedSkills,
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
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
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
	httpx.WriteJSON(w, http.StatusOK, toSimpleAgentResponse(existing, imageDisplayName))
}

func (s *Server) deleteAgent(ctx context.Context, w http.ResponseWriter, name string) {
	var existing agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &existing); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if isManagedCR(existing.Spec.ManagedBy) {
		writeManagedConflict(w, "agent", existing.Spec.ManagedBy)
		return
	}
	agent := &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.ns},
	}
	if err := s.client.Delete(ctx, agent); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "agent not found")
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
