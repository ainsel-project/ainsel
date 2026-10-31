package api

import (
	"fmt"
	"net/http"
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
	writeError(w, http.StatusConflict, fmt.Sprintf("%s is managed by %s", kind, managedBy))
}
