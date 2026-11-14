package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
)

// agentBodyMaxBytes caps how much of a request body the hub reads. Agent specs
// are small; the cap is a DoS guard, matching the bound the other handlers put
// on their bodies (see handlers_llm_check.go).
const agentBodyMaxBytes = 1 << 20

// skillsShapeMessage replaces the decoder's Go-type string on the wire. An
// operator who sends the pre-`530070a` wrapper gets an instruction, not
// "cannot unmarshal object into Go struct field … of type []string".
//
// It names the docs rather than a version: the version this landed under is
// still "Unreleased", and a version baked into a shipped string is wrong the
// moment it releases.
const skillsShapeMessage = "skills must be a list of skill ids; the {items: [...]} wrapper was removed - see docs/upgrade-guide.md"

// shapeGuards maps a request field to the message for a value that is not a
// list. Keyed by field name so the next shape change (enabledTools is the
// obvious candidate) is one entry here, not a new code path.
var shapeGuards = map[string]string{
	"skills": skillsShapeMessage,
}

// decodeJSONBody reads a bounded request body into dst, answering a wrong-shape
// guarded field with its pointer message before the decoder gives up on it.
//
// The plain `json.NewDecoder(r.Body).Decode` it replaces reports a wrong-shaped
// `skills` as a Go type name, which is useless to an HTTP client and is exactly
// what made the skills wrapper change so hard for downstream plugins to
// diagnose (issue #263). Genuine syntax errors keep the `invalid JSON:` prefix.
// Returns true when dst was filled in.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, agentBodyMaxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	if msg, ok := wrongShapeField(body); ok {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// wrongShapeField finds a guarded field whose value is neither a list nor null.
// json.RawMessage is what makes this precise: it captures the value whatever its
// JSON type, so no decoding of the request itself has to succeed first. A body
// that is not a JSON object at all is left to the real decoder, which has the
// better message for that.
func wrongShapeField(body []byte) (string, bool) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false
	}
	for field, msg := range shapeGuards {
		raw, ok := probe[field]
		if !ok || valueIsListOrNull(raw) {
			continue
		}
		return msg, true
	}
	return "", false
}

// valueIsListOrNull reports whether a raw JSON value starts as an array or is
// null. null is not a shape problem: it decodes to a nil slice, which every
// request type here already reads as "absent / no change".
func valueIsListOrNull(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[', 'n':
			return true
		default:
			return false
		}
	}
	return false
}
