package mcpservers

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ManagedBy values. "user" rows are console-owned, "helm" rows arrive with the
// chart's seed list, and "plugin" rows are written by the plugin registry
// syncer for a plugin's agent MCP endpoint.
const (
	ManagedByUser   = "user"
	ManagedByHelm   = "helm"
	ManagedByPlugin = "plugin"
)

// PluginNamePrefix is the name prefix every plugin-owned registry row carries,
// so the row's owner is readable from its name alone. The plugin id is the
// name without it.
const PluginNamePrefix = "plugin-"

// ErrManaged is returned by Delete when the row is owned by helm.
var ErrManaged = errors.New("mcp server is managed and cannot be deleted")

// ErrPluginManaged is the sentinel wrapped by every PluginManagedError, so a
// caller can test for plugin ownership without knowing the plugin id.
var ErrPluginManaged = errors.New("mcp server is managed by a plugin")

// PluginManagedError reports a row owned by a plugin. Its message is the exact
// wording the API answers with, so the handler passes it through verbatim.
type PluginManagedError struct {
	PluginID string
}

func (e PluginManagedError) Error() string {
	return fmt.Sprintf("managed by plugin %s", e.PluginID)
}

func (e PluginManagedError) Unwrap() error { return ErrPluginManaged }

// PluginIDFromName recovers the plugin id from a plugin-owned row name. It
// returns the name unchanged for a name without the prefix, which cannot
// happen for a row the syncer wrote.
func PluginIDFromName(name string) string {
	return strings.TrimPrefix(name, PluginNamePrefix)
}

// pluginManaged is the error a plugin-owned row answers user mutations with.
func pluginManaged(name string) error {
	return PluginManagedError{PluginID: PluginIDFromName(name)}
}

// Service wraps Store with business logic.
type Service struct {
	store *Store
}

func NewService(s *Store) *Service {
	return &Service{store: s}
}

func (s *Service) Create(ctx context.Context, m *MCPServer) error {
	return s.store.Create(ctx, m)
}

func (s *Service) Get(ctx context.Context, name string) (*MCPServer, error) {
	return s.store.Get(ctx, name)
}

func (s *Service) List(ctx context.Context) ([]*MCPServer, error) {
	return s.store.List(ctx)
}

// Update mutates a row. The stored row's owner decides: a plugin-owned row is
// refused, so a user cannot repoint an agent endpoint the hub itself serves.
// The check reads the stored row rather than the caller's copy, because that
// copy is whatever the caller chose to send.
func (s *Service) Update(ctx context.Context, m *MCPServer) error {
	row, err := s.store.Get(ctx, m.Name)
	if err != nil {
		return err
	}
	if row.ManagedBy == ManagedByPlugin {
		return pluginManaged(row.Name)
	}
	return s.store.Update(ctx, m)
}

// Delete removes a row. Returns ErrManaged when the row is owned by helm and
// PluginManagedError when it is owned by a plugin.
func (s *Service) Delete(ctx context.Context, name string) error {
	row, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	if row.ManagedBy == ManagedByPlugin {
		return pluginManaged(row.Name)
	}
	if row.ManagedBy != ManagedByUser {
		return ErrManaged
	}
	return s.store.Delete(ctx, name)
}

// DeleteManaged removes a row with no ownership check. It exists for the
// plugin registry syncer, which reconciles plugin-owned rows and is the only
// code allowed to drop one; every other caller goes through Delete.
func (s *Service) DeleteManaged(ctx context.Context, name string) error {
	return s.store.Delete(ctx, name)
}

// Upsert is used by the seed loader and the plugin registry syncer.
func (s *Service) Upsert(ctx context.Context, m *MCPServer) error {
	return s.store.Upsert(ctx, m)
}
