package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// --- writeErr ---

func TestWriteErr_MapsAPIErrorToStatusAndMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, wrapAPIError(http.StatusBadRequest, "mcp server not found: github", errors.New("boom")))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the documented {\"error\": ...} JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] != "mcp server not found: github" {
		t.Errorf("expected the rendered message, got %q", body["error"])
	}
}

func TestWriteErr_FallsBackTo500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, errors.New("something unexpected"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if want := `{"error":"something unexpected"}` + "\n"; rec.Body.String() != want {
		t.Errorf("expected body %s, got %s", want, rec.Body.String())
	}
}

func TestAPIError_KeepsWrappedErrorMatchable(t *testing.T) {
	err := wrapAPIError(http.StatusBadRequest, "skill not found: s-1", skills.ErrNotFound)
	if !errors.Is(err, skills.ErrNotFound) {
		t.Error("expected errors.Is to still match the wrapped sentinel")
	}
	if err.Error() != "skill not found: s-1" {
		t.Errorf("expected Error() to return the response message, got %q", err.Error())
	}
}

// --- agentEnvFromRequest (no ResponseWriter: unit-testable directly) ---

func TestAgentEnvFromRequest(t *testing.T) {
	tests := []struct {
		name     string
		in       []AgentImageEnvVarInfo
		existing []agentv1alpha1.AgentEnvVar
		want     []agentv1alpha1.AgentEnvVar
		wantMsg  string // non-empty means a 400 apiError with this message
	}{
		{
			name: "empty request means no overrides",
			in:   nil,
			want: nil,
		},
		{
			name: "values pass through trimmed",
			in:   []AgentImageEnvVarInfo{{Name: "  HOME_DIR  ", Value: "/root"}},
			want: []agentv1alpha1.AgentEnvVar{{Name: "HOME_DIR", Value: "/root"}},
		},
		{
			name:     "secret with an empty value keeps the stored value",
			in:       []AgentImageEnvVarInfo{{Name: "TOKEN", Value: "", Secret: true}},
			existing: []agentv1alpha1.AgentEnvVar{{Name: "TOKEN", Value: "stored", Secret: true}},
			want:     []agentv1alpha1.AgentEnvVar{{Name: "TOKEN", Value: "stored", Secret: true}},
		},
		{
			name: "secret with an empty value and nothing stored stays empty",
			in:   []AgentImageEnvVarInfo{{Name: "TOKEN", Secret: true}},
			want: []agentv1alpha1.AgentEnvVar{{Name: "TOKEN", Value: "", Secret: true}},
		},
		{
			name:    "missing name is rejected",
			in:      []AgentImageEnvVarInfo{{Name: "   ", Value: "x"}},
			wantMsg: "env name is required",
		},
		{
			name:    "invalid name is rejected",
			in:      []AgentImageEnvVarInfo{{Name: "not an env name", Value: "x"}},
			wantMsg: "invalid env name not an env name",
		},
		{
			name:    "duplicate name is rejected",
			in:      []AgentImageEnvVarInfo{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}},
			wantMsg: "duplicate env name: A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agentEnvFromRequest(tc.in, tc.existing)
			if tc.wantMsg != "" {
				var ae *apiError
				if !errors.As(err, &ae) {
					t.Fatalf("expected an *apiError, got %v", err)
				}
				if ae.Status != http.StatusBadRequest {
					t.Errorf("expected 400, got %d", ae.Status)
				}
				if !strings.Contains(ae.Msg, tc.wantMsg) {
					t.Errorf("expected message containing %q, got %q", tc.wantMsg, ae.Msg)
				}
				if got != nil {
					t.Errorf("expected no entries on failure, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d entries, got %v", len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d: expected %+v, got %+v", i, tc.want[i], got[i])
				}
			}
		})
	}
}

// --- validateAgentImageRef (status triple collapses into the error) ---

func TestValidateAgentImageRef(t *testing.T) {
	img := testAgentImageWithDisplayName("img-1", "Image One", "git", "bash")
	s := testServer(t, img)
	ctx := context.Background()

	if name, err := s.validateAgentImageRef(ctx, "img-1", []string{"git"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} else if name != "Image One" {
		t.Errorf("expected display name 'Image One', got %q", name)
	}

	tests := []struct {
		name       string
		refName    string
		enabled    []string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "empty name",
			refName:    "  ",
			wantStatus: http.StatusBadRequest,
			wantMsg:    "imageRef.name is required",
		},
		{
			name:       "unknown image",
			refName:    "nope",
			wantStatus: http.StatusBadRequest,
			wantMsg:    `imageRef.name "nope" does not match any AgentImage`,
		},
		{
			name:       "tool not declared by the image",
			refName:    "img-1",
			enabled:    []string{"git", "docker"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    "enabledTools not in image: docker",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, err := s.validateAgentImageRef(ctx, tc.refName, tc.enabled)
			var ae *apiError
			if !errors.As(err, &ae) {
				t.Fatalf("expected an *apiError, got %v", err)
			}
			if ae.Status != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, ae.Status)
			}
			if ae.Msg != tc.wantMsg {
				t.Errorf("expected message %q, got %q", tc.wantMsg, ae.Msg)
			}
			if name != "" {
				t.Errorf("expected no display name on failure, got %q", name)
			}
		})
	}
}

// --- resolveMCPRequest ---

func TestResolveMCPRequest_NilRequestMeansUnchanged(t *testing.T) {
	s := testServer(t)
	got, err := s.resolveMCPRequest(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected a nil selection for a nil request (inherit the image), got %+v", got)
	}
}

func TestResolveMCPRequest_ExplicitEmptySelection(t *testing.T) {
	s := testServer(t)
	// An explicit empty selection is not the same thing as no selection: it
	// means "no MCP connections". No name is looked up for it, so a service
	// with no store behind it is enough to exercise the distinction.
	s.mcp = mcpservers.NewService(nil)

	got, err := s.resolveMCPRequest(context.Background(), &AgentMCPRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil selection for an explicit empty selection")
	}
	if len(got.Servers) != 0 {
		t.Errorf("expected no servers, got %v", got.Servers)
	}
}

func TestResolveMCPRequest_ServiceNotConfigured(t *testing.T) {
	s := testServer(t) // s.mcp is nil
	_, err := s.resolveMCPRequest(context.Background(), &AgentMCPRequest{Servers: []string{"github"}})
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected an *apiError, got %v", err)
	}
	if ae.Status != http.StatusServiceUnavailable || ae.Msg != "mcp service not configured" {
		t.Errorf("expected 503 'mcp service not configured', got %d %q", ae.Status, ae.Msg)
	}
}

// --- validateAgentSkills ---

func TestValidateAgentSkills_UnknownID(t *testing.T) {
	ms := newMockSkillService()
	ms.skills["skill-1"] = &skills.Skill{ID: "skill-1", Name: "Skill One"}
	s := testServer(t)
	s.skills = ms
	ctx := context.Background()

	if err := s.validateAgentSkills(ctx, []string{"skill-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := s.validateAgentSkills(ctx, []string{"skill-1", "missing"})
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected an *apiError, got %v", err)
	}
	if ae.Status != http.StatusBadRequest || ae.Msg != "skill not found: missing" {
		t.Errorf("expected 400 'skill not found: missing', got %d %q", ae.Status, ae.Msg)
	}
	if !errors.Is(err, skills.ErrNotFound) {
		t.Error("expected the returned error to still match skills.ErrNotFound")
	}
}

// --- applyAgentRequest (request-shaped struct, returns an error) ---

func TestApplyAgentRequest(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	spec := &agentv1alpha1.AgentSpec{}
	req := &SimpleAgentUpdateRequest{
		Name:         ptr("Assembled"),
		ImageRef:     &AgentImageRefInfo{Name: "img-1"},
		LLM:          &AgentLLMInfo{Provider: "ollama-cloud", Model: "glm-5.1:cloud"},
		EnabledTools: &[]string{"git"},
	}

	err := s.applyAgentRequest(ctx, agentApplyInput{spec: spec, req: req, res: &agentValidationResult{}, name: "a-1", newAgent: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.DisplayName != "Assembled" || spec.ImageRef.Name != "img-1" || spec.LLM.Provider != "ollama-cloud" {
		t.Errorf("spec not assembled as expected: %+v", spec)
	}
	if len(spec.EnabledTools) != 1 || spec.EnabledTools[0] != "git" {
		t.Errorf("expected enabledTools [git], got %v", spec.EnabledTools)
	}
}

func TestApplyAgentRequest_MissingProviderOnCreate(t *testing.T) {
	s := testServer(t)
	spec := &agentv1alpha1.AgentSpec{}
	req := &SimpleAgentUpdateRequest{Name: ptr("No Provider")}

	err := s.applyAgentRequest(context.Background(), agentApplyInput{
		spec: spec, req: req, res: &agentValidationResult{}, name: "a-1", newAgent: true,
	})
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected an *apiError, got %v", err)
	}
	if ae.Status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", ae.Status)
	}
	if !strings.Contains(ae.Msg, "llm.provider is required") {
		t.Errorf("expected a provider-required message, got %q", ae.Msg)
	}
}

// --- the double-write contract, end to end ---

// countingResponseWriter records how often a handler wrote a status, which is
// what "validators return errors instead of responding" has to guarantee: a
// failed request is answered exactly once.
type countingResponseWriter struct {
	header http.Header
	status int
	writes int
	body   bytes.Buffer
}

func (c *countingResponseWriter) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *countingResponseWriter) WriteHeader(status int) {
	c.status = status
	c.writes++
}

func (c *countingResponseWriter) Write(p []byte) (int, error) { return c.body.Write(p) }

func TestCreateAgent_ValidationErrorWritesExactlyOnce(t *testing.T) {
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)

	body, _ := json.Marshal(SimpleAgentCreateRequest{
		Name:     "Bad Image",
		ImageRef: AgentImageRefInfo{Name: "nope"},
		LLM:      AgentLLMInfo{Provider: "ollama-cloud", Model: "glm-5.1:cloud"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := &countingResponseWriter{}
	s.mux.ServeHTTP(rec, req)

	if rec.writes != 1 {
		t.Errorf("expected exactly one WriteHeader, got %d (body %q)", rec.writes, rec.body.String())
	}
	if rec.status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.status)
	}
	// docs/api-reference.md: every REST error body is {"error": "..."}. Image-ref
	// failures used to go out as text/plain via http.Error; they are JSON now.
	if ct := rec.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected a JSON error body per the API reference, got %q", ct)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, rec.body.String())
	}
	if !strings.Contains(out["error"], "does not match any AgentImage") {
		t.Errorf("expected the image-ref message, got %q", out["error"])
	}
}

func TestUpdateAgent_ValidationErrorWritesExactlyOnce(t *testing.T) {
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)

	body, _ := json.Marshal(SimpleAgentCreateRequest{
		Name:     "Update Target",
		ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Provider: "ollama-cloud", Model: "glm-5.1:cloud"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	ubody, _ := json.Marshal(SimpleAgentUpdateRequest{
		Env: &[]AgentImageEnvVarInfo{{Name: "duplicate"}, {Name: "duplicate"}},
	})
	ureq := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.ID, bytes.NewReader(ubody))
	ureq.Header.Set("Content-Type", "application/json")
	urec := &countingResponseWriter{}
	s.mux.ServeHTTP(urec, ureq)

	if urec.writes != 1 {
		t.Errorf("expected exactly one WriteHeader, got %d (body %q)", urec.writes, urec.body.String())
	}
	if urec.status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", urec.status)
	}
	if !strings.Contains(urec.body.String(), "duplicate env name: duplicate") {
		t.Errorf("expected the duplicate-env message, got %q", urec.body.String())
	}
}
