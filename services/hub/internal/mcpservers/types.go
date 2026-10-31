// Package mcpservers manages the hub's registry of external MCP endpoints.
// An MCPServer entry records the endpoint URL and how to authenticate to it;
// Store persists user-managed entries in Postgres while Service layers
// validation and lifecycle rules (e.g. rejecting edits to helm-managed
// entries) on top.
package mcpservers

import "time"

// MCPServer is a registry entry for an external MCP endpoint.
type MCPServer struct {
	Name         string
	DisplayName  string
	Description  string
	URL          string
	TokenFromEnv string // name of the env var on the agent pod whose value is sent as the bearer token
	ManagedBy    string // "user" | "helm"
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
