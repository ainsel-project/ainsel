package api

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

// managedByUser is the reserved spec.managedBy value meaning
// console-managed — the same rule the DB-backed registries use
// (skills.ManagedByUser, mcpservers.ManagedByUser, personas.ManagedByUser).
// An empty value is equivalent: no +kubebuilder:default is set on the CRDs, so
// existing CRs keep their meaning and stay editable.
const managedByUser = "user"

// isManagedCR reports whether a CRD spec.managedBy value marks the resource
// externally managed. Any non-empty value other than "user" names an external
// author (helm, gitops, plugin, ...) whose resource the hub API must refuse to
// mutate — the same rule as IsManaged in the skills/mcpservers/personas
// packages, applied to the CRD-backed kinds (Agents, AgentImages,
// WebhookConnectors).
//
// This guards the hub REST API only: direct kubectl writes are not blocked
// (an admission webhook would be; it is out of scope for issue #146).
func isManagedCR(managedBy string) bool {
	return managedBy != "" && managedBy != managedByUser
}

// writeManagedConflict answers a mutation of an externally managed resource
// with 409 Conflict naming the manager, matching the MCP-server/skill
// plugin-managed contract.
func writeManagedConflict(w http.ResponseWriter, kind, managedBy string) {
	httpx.WriteError(w, http.StatusConflict, fmt.Sprintf("%s is managed by %s", kind, managedBy))
}

// managedByPattern is the CRD validation pattern for spec.managedBy
// (^[a-z0-9][a-z0-9.-]{0,62}$). A stamp the hub writes into a CR must satisfy
// it, or the API server turns a plugin's create into a 500-worthy rejection.
var managedByPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

// callerIsManager reports whether the caller is the named manager of a
// resource whose managedBy value is managedBy — the one exception to the
// managed-resource 409, shared by every guard site in this package. The rule
// itself lives in plugincreds.Caller.IsManagerOf, next to the identity it is
// derived from, so the API layer and the DB-backed registries cannot drift.
func callerIsManager(caller *plugincreds.Caller, managedBy string) bool {
	return caller.IsManagerOf(managedBy)
}

// requestCaller is the caller on the request context, or nil for a console
// request — resolveCreateManagedBy and callerIsManager both treat a nil caller
// as "not a plugin", which is exactly the #146 behavior.
func requestCaller(r *http.Request) *plugincreds.Caller {
	caller, _ := plugincreds.CallerFrom(r.Context())
	return caller
}

// resolveCreateManagedBy decides the managedBy value a create request writes.
//
// The rule has exactly three branches:
//
//   - a plugin-kind caller that asks to be named gets its own stamp, and only
//     its own stamp — the submitted value must equal the canonical
//     "plugin-<caller plugin id>" composed here, so the body can never name a
//     different plugin or a free-form manager; a mismatch is a 400 rather
//     than a silently ignored field, because a plugin that believes it stamped
//     a row it did not would ship the two-writers bug this field exists to
//     prevent;
//   - a plugin-kind caller that sends nothing gets "user", same as today —
//     stamping is something a plugin may do, not something forced on it;
//   - anyone else (console user, agent- or user-kind credential) keeps the
//     #146 behavior: a submitted value is ignored and the row is "user".
func resolveCreateManagedBy(caller *plugincreds.Caller, requested string) (string, error) {
	if requested == "" {
		return managedByUser, nil
	}
	if !caller.IsPlugin() {
		return managedByUser, nil
	}
	stamp := plugincreds.ManagerStamp(caller.PluginID)
	if requested != stamp {
		return "", fmt.Errorf("managedBy must be %q for this caller", stamp)
	}
	if !managedByPattern.MatchString(stamp) {
		// Only reachable via an over-long plugin id; say so instead of letting
		// CRD validation reject the write.
		return "", fmt.Errorf("managedBy %q is not a valid manager name (must match %s)", stamp, managedByPattern.String())
	}
	return stamp, nil
}

// writeCreateManagedBy renders a resolveCreateManagedBy failure as the 400 it
// is and reports whether the handler must stop.
func writeCreateManagedBy(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	httpx.WriteError(w, http.StatusBadRequest, err.Error())
	return true
}

// crManagedBy maps a resolved create stamp onto the value written into a CRD's
// spec.managedBy. "user" stays empty: the CRDs deliberately declare no default
// (so every pre-existing CR keeps meaning "console-managed"), and a console
// create must not start writing a field that #146 left absent. Only a real
// stamp is written.
func crManagedBy(managedBy string) string {
	if managedBy == managedByUser {
		return ""
	}
	return managedBy
}

// isManagedCRForRequest reports whether a CR must refuse this request: it is
// externally managed and the caller is not the named manager.
func isManagedCRForRequest(r *http.Request, managedBy string) bool {
	return isManagedCR(managedBy) && !callerIsManager(requestCaller(r), managedBy)
}
