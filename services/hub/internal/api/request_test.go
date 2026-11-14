package api

// Shared request helpers for the api tests. Use these instead of repeating
// httptest.NewRequest / Decode / rec.Code checks in every test:
//
//	status, body := do(t, s, http.MethodGet, "/api/v1/agents", nil)
//	agents := decode[[]types.SimpleAgentResponse](t, body)
//
// doAs is the same but carries an OIDC identity (sub == "" means anonymous),
// for the authz tests. A nil body sends no body; a string body is sent
// verbatim (for tests that assert handlers reject invalid JSON); anything
// else is marshalled as JSON.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// do issues a request against s and returns the status code and raw body.
func do(t *testing.T, s *Server, method, path string, body any) (int, json.RawMessage) {
	t.Helper()
	return doWith(t, s, method, path, body, nil)
}

// doAs issues a request carrying an OIDC identity; sub == "" produces an
// unauthenticated request.
func doAs(t *testing.T, s *Server, method, path, sub string, body any) (int, json.RawMessage) {
	t.Helper()
	var ctxFn func(context.Context) context.Context
	if sub != "" {
		ctxFn = asUser(sub, sub)
	}
	return doWith(t, s, method, path, body, ctxFn)
}

func doWith(t *testing.T, s *Server, method, path string, body any, ctxFn func(context.Context) context.Context) (int, json.RawMessage) {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
		// no body
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ctxFn != nil {
		req = req.WithContext(ctxFn(req.Context()))
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Code, json.RawMessage(rec.Body.Bytes())
}

// decode unmarshals a response body into T, failing the test on error.
func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return out
}
