package api

import (
	"errors"
	"net/http"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
)

// apiError is an error that carries the HTTP status and response message the
// handler boundary should map it to.
//
// Validators, converters and other helpers that used to take an
// http.ResponseWriter return it instead: they decide, they do not respond.
// That keeps the "if this returned an error the response is already written,
// do not write again" contract out of the package — a returned error now
// always means "nothing has been written yet" — and it makes those functions
// testable without an httptest.ResponseRecorder.
//
// Msg carries the full rendered message (e.g. "mcp server not found: github"),
// because writeError's body is {"error": msg} and the console and the
// connector plugins parse that string.
type apiError struct {
	// Status is the HTTP status code to respond with.
	Status int
	// Msg is the message for the response's "error" field.
	Msg string
	// Err is the underlying error, if any. nil for pure validation failures.
	Err error
}

func (e *apiError) Error() string { return e.Msg }

func (e *apiError) Unwrap() error { return e.Err }

// newAPIError builds an apiError for a failure with no underlying error, the
// shape a validation rejection takes.
func newAPIError(status int, msg string) *apiError {
	return &apiError{Status: status, Msg: msg}
}

// wrapAPIError builds an apiError around an underlying error, for failures
// whose status only the call site knows. Wrapping keeps errors.Is useful on
// the returned error (e.g. errors.Is(err, mcpservers.ErrNotFound)).
func wrapAPIError(status int, msg string, err error) *apiError {
	return &apiError{Status: status, Msg: msg, Err: err}
}

// writeErr maps an error returned by a validator or converter onto the HTTP
// response. It belongs at the handler boundary: the handler is the only place
// that writes, and it writes exactly once per request.
//
// An *apiError responds with its own status and message. Anything else is an
// unexpected internal failure and responds 500 with err.Error(), which is what
// every handler did inline before, so nothing new leaks.
//
// There is deliberately no global sentinel-to-status table here: the same
// sentinel carries different statuses depending on the call site — a
// mcpservers.ErrNotFound named in a request body is a 400 (a bad request
// field), while authz.ErrNotFound for an addressed resource is a 404 (see
// writeAuthzError). The call site that knows the status wraps it into an
// apiError; writeErr only reads it back.
func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		httpx.WriteError(w, ae.Status, ae.Msg)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, err.Error())
}
