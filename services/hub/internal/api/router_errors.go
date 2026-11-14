package api

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
)

// maxRouterErrorBody bounds how many bytes of a candidate router error body are
// buffered while deciding whether to replace it. The mux's own messages are a
// few dozen bytes, so anything longer is a handler's body and is passed through.
const maxRouterErrorBody = 512

// jsonRouterErrors wraps the route mux so the 404 and 405 responses that
// `http.ServeMux` generates itself keep the published error contract: every
// non-WebSocket error body is `{"error": "..."}` (docs/api-reference.md,
// Error Format), and plugin clients render that field as the failure message.
//
// Without it, a route the router rejects — a wrong method on a registered
// path, or a path no pattern matches — answers with the mux's own
// `text/plain` "404 page not found" / "Method Not Allowed" body, which no
// client of this API parses.
//
// Only the mux's own answers are rewritten: the status must be 404 or 405, the
// Content-Type must still be the one the mux writes, and the body must still
// start with the mux's fixed message. A handler that writes its own 404/405 —
// JSON, plain text or empty — is passed through untouched, and the `Allow`
// header the mux sets on a 405 is preserved.
//
// The wrapper must not get in the way of responses that are not errors:
// nothing is buffered once a non-error status has been written, and Hijack,
// Flush and Unwrap are forwarded, or the WebSocket upgrade on /api/v1/ws and
// the streaming paths would break behind it.
func jsonRouterErrors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &routerErrorWriter{ResponseWriter: w}
		// Deferred, not sequential: a handler that panics after choosing a
		// router-shaped 404/405 must still get that status on the wire, or
		// net/http's recovery turns it into a 500. finish() commits nothing when
		// nothing was written, so the http.ErrAbortHandler case is unaffected.
		defer rw.finish()
		h.ServeHTTP(rw, r)
	})
}

// routerErrorMessages are the exact bodies http.ServeMux writes for the two
// statuses it generates, keyed by status. A response is only treated as
// router-generated when its body still starts with the entry for its status.
var routerErrorMessages = map[int]string{
	http.StatusNotFound:         "404 page not found",
	http.StatusMethodNotAllowed: "Method Not Allowed",
}

// routerErrorWriter buffers a candidate router error until the handler returns,
// then either replaces it with JSON or replays it verbatim. Everything else
// passes straight through.
type routerErrorWriter struct {
	http.ResponseWriter

	// status is the status passed to WriteHeader; 0 means the handler never
	// called it, so the first Write records the implicit 200.
	status int
	// wroteHeader is true once a status has been committed downstream.
	wroteHeader bool
	// buf holds the body while it is still a candidate for replacement.
	buf bytes.Buffer
	// passthrough is set as soon as the response is committed: no buffering.
	passthrough bool
}

func (w *routerErrorWriter) WriteHeader(status int) {
	if w.wroteHeader || w.passthrough {
		return
	}
	w.status = status
	if _, ok := routerErrorMessages[status]; !ok {
		w.commit(status)
	}
	// A candidate error: hold the header until the body tells us whose it is.
}

func (w *routerErrorWriter) Write(b []byte) (int, error) {
	if w.passthrough {
		return w.ResponseWriter.Write(b)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if _, ok := routerErrorMessages[w.status]; !ok {
		w.commit(w.status)
		return w.ResponseWriter.Write(b)
	}
	if w.buf.Len()+len(b) > maxRouterErrorBody {
		w.commit(w.status)
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

// finish is called once the handler returns. It commits whatever is left: a
// JSON replacement for a router-generated error, or the buffered response
// as-is.
func (w *routerErrorWriter) finish() {
	if w.passthrough || w.wroteHeader {
		return
	}
	if w.status == 0 {
		// Nothing was written at all — leave the net/http default alone.
		return
	}
	if prefix, ok := routerErrorMessages[w.status]; ok &&
		bytes.HasPrefix(w.buf.Bytes(), []byte(prefix)) {
		w.wroteHeader = true
		w.passthrough = true
		writeError(w.ResponseWriter, w.status, routerErrorMessage(w.status))
		w.buf.Reset()
		return
	}
	w.commit(w.status)
}

// commit writes the buffered status and body downstream and switches to
// unbuffered pass-through for the rest of the response.
func (w *routerErrorWriter) commit(status int) {
	if w.wroteHeader || w.passthrough {
		return
	}
	w.wroteHeader = true
	w.passthrough = true
	w.ResponseWriter.WriteHeader(status)
	if w.buf.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		w.buf.Reset()
	}
}

// Hijack forwards to the wrapped writer so the WebSocket upgrade on
// /api/v1/ws still works behind the wrapper. A buffered candidate body is
// released first, so a handler that writes and then hijacks cannot lose bytes.
func (w *routerErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.release()
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("api: response writer does not support hijacking")
	}
	return hj.Hijack()
}

// Flush forwards to the wrapped writer, releasing any buffered bytes first, so
// streaming handlers keep working behind the wrapper.
func (w *routerErrorWriter) Flush() {
	w.release()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// release puts a still-buffered response on the wire, for the callers (Hijack,
// Flush) that need everything written so far to be real. A handler that has
// written nothing yet — a WebSocket upgrade is the case that matters — must
// still own its response, so nothing is committed for it.
func (w *routerErrorWriter) release() {
	if w.wroteHeader || w.passthrough {
		return
	}
	if w.status == 0 && w.buf.Len() == 0 {
		w.passthrough = true
		return
	}
	w.commit(w.status)
}

// Unwrap exposes the wrapped writer to http.ResponseController, which reaches
// Hijack, deadlines and the rest through it.
func (w *routerErrorWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// routerErrorMessage is the JSON error text for a router-generated status. The
// 405 text is the one handlers used to write by hand, so clients that match on
// it keep matching.
func routerErrorMessage(status int) string {
	switch status {
	case http.StatusMethodNotAllowed:
		return "method not allowed"
	default:
		return "not found"
	}
}
