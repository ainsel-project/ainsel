package config

import (
	"errors"
	"os"
	"strconv"
)

// Config holds configuration for the chat MCP sidecar.
// All values come from environment variables, with sensible defaults
// for in-cluster operation.
type Config struct {
	// Port is the HTTP listen port for the MCP server.
	Port int

	// HubURL is the hub backend REST API base URL.
	HubURL string

	// AgentName identifies which agent this sidecar belongs to.
	// The operator sets this env var on the agent pod.
	AgentName string

	// HubInternalToken is the shared secret for bypassing OIDC auth on
	// hub internal endpoints.
	HubInternalToken string

	// TaskDir is the directory a task pod's containers share, set by the operator
	// on per-task Jobs only. When it is non-empty the sidecar ends together with
	// the agent runtime that wrote its pid there, instead of holding the task pod
	// (and the Job's slot in the agent's concurrency ceiling) open after the task
	// resolved. Requires the pod to share its PID namespace.
	TaskDir string
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:             envInt("PORT", 8081),
		HubURL:           envStr("HUB_URL", ""),
		AgentName:        envStr("AGENT_NAME", ""),
		HubInternalToken: envStr("HUB_INTERNAL_VALIDATE_SECRET", ""),
		TaskDir:          envStr("AINSEL_TASK_DIR", ""),
	}
}

// Validate returns an error if required configuration is missing.
func (c Config) Validate() error {
	if c.HubURL == "" {
		return errors.New("HUB_URL is required")
	}
	if c.AgentName == "" {
		return errors.New("AGENT_NAME is required")
	}
	return nil
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}