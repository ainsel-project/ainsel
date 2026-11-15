package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"id": "abc"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	// json.Encoder terminates the document with a newline; the api handlers
	// have always produced that body, so it is part of the shape tests assert.
	if got, want := rec.Body.String(), "{\"id\":\"abc\"}\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "persona not found")

	// The {"error": ...} envelope is parsed by the hub plugin clients, so the
	// body must stay byte-for-byte this.
	if got, want := rec.Body.String(), "{\"error\":\"persona not found\"}\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer", "Bearer tok123", "tok123"},
		{"case-insensitive scheme", "bearer tok123", "tok123"},
		{"surrounding spaces", "Bearer   tok123 ", "tok123"},
		{"empty header", "", ""},
		{"wrong scheme", "Basic tok123", ""},
		{"prefix only", "Bearer ", ""},
		{"empty token", "Bearer  ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BearerToken(tc.header); got != tc.want {
				t.Errorf("BearerToken(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
