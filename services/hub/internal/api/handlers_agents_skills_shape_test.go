package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wrapper→list change to Agent.spec.skills (530070a) left clients sending
// `skills: {items: […]}` to a decoder that answers with a Go type name. These
// tests pin the replacement: a wrong shape gets the pointer message, and every
// shape the typed request already understood keeps working (issue #263).

// createAgentBody returns a minimal valid create body with the given raw
// `skills` value spliced in ("" omits the field entirely).
func createAgentBody(t *testing.T, skills string) []byte {
	t.Helper()
	fields := []string{
		`"name":"Shape Agent"`,
		`"imageRef":{"name":"img-1"}`,
		`"llm":{"model":"glm-5.1:cloud","provider":"ollama-cloud"}`,
	}
	if skills != "" {
		fields = append(fields, `"skills":`+skills)
	}
	return []byte("{" + strings.Join(fields, ",") + "}")
}

func postAgents(t *testing.T, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

// putAgentShape creates one agent, then PUTs the given body to it.
func putAgentShape(t *testing.T, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		bytes.NewReader(createAgentBody(t, ""))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed agent: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode seed agent: %v", err)
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.ID,
		bytes.NewReader(body)))
	return rec
}

// assertShapeRejected pins both halves of the acceptance rule: a 400 that names
// the fix, and no Go decoder detail leaking to the client.
func assertShapeRejected(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("expected %q in body, got %s", want, rec.Body.String())
	}
	for _, leak := range []string{"Go struct field", "cannot unmarshal", "[]string"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("response leaked decoder detail %q: %s", leak, rec.Body.String())
		}
	}
}

func TestAgentSkillsShape_Create(t *testing.T) {
	tests := []struct {
		name   string
		skills string
		// wantStatus is 0 when the request should be rejected with the pointer.
		wantStatus int
	}{
		{name: "wrapper with ids", skills: `{"items":["sk-1"]}`},
		{name: "empty wrapper", skills: `{}`},
		{name: "wrapper with null items", skills: `{"items":null}`},
		{name: "string", skills: `"sk-1"`},
		{name: "number", skills: `42`},
		{name: "list", skills: `["sk-1"]`, wantStatus: http.StatusCreated},
		{name: "empty list", skills: `[]`, wantStatus: http.StatusCreated},
		{name: "null", skills: `null`, wantStatus: http.StatusCreated},
		{name: "absent", skills: "", wantStatus: http.StatusCreated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAgents(t, createAgentBody(t, tc.skills))
			if tc.wantStatus != 0 {
				if rec.Code != tc.wantStatus {
					t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
				}
				return
			}
			assertShapeRejected(t, rec, "list of skill ids")
		})
	}
}

func TestAgentSkillsShape_Update(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "wrapper", body: `{"skills":{"items":["sk-1"]}}`},
		{name: "string", body: `{"skills":"sk-1"}`},
		{name: "list", body: `{"skills":[]}`, wantStatus: http.StatusOK},
		{name: "null leaves it alone", body: `{"skills":null}`, wantStatus: http.StatusOK},
		{name: "absent", body: `{}`, wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := putAgentShape(t, []byte(tc.body))
			if tc.wantStatus != 0 {
				if rec.Code != tc.wantStatus {
					t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
				}
				return
			}
			assertShapeRejected(t, rec, "list of skill ids")
		})
	}
}

// A genuine syntax error keeps the message it always had — the shape probe must
// not swallow it.
func TestAgentSkillsShape_MalformedJSONStillReports(t *testing.T) {
	rec := postAgents(t, []byte(`{"name":"x",`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid JSON:") {
		t.Fatalf("expected the invalid JSON message, got %s", rec.Body.String())
	}
}

// The bounded read must reject an oversized body rather than buffer it, and must
// not panic on the way.
func TestAgentSkillsShape_OversizedBodyRejected(t *testing.T) {
	huge := `"` + strings.Repeat("x", agentBodyMaxBytes+1024) + `"`
	rec := postAgents(t, createAgentBody(t, huge))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// The probe looks at shape only, so a body that is not an object at all is left
// to the real decoder, which has the better message for it.
func TestWrongShapeField_NonObjectBody(t *testing.T) {
	for _, body := range []string{`[1,2,3]`, `"skills"`, `12`, `not json`} {
		if msg, ok := wrongShapeField([]byte(body)); ok {
			t.Fatalf("body %s reported a shape problem (%s)", body, msg)
		}
	}
}
