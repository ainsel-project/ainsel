package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DominikPinsel/ainsel/services/hub/internal/authz"
	ainselv1alpha1 "github.com/DominikPinsel/ainsel/shared/api/api/v1alpha1"

	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// runBackfill waits for cache sync, collects every groupable resource — from
// the Kubernetes API (agents, connectors, agent images) and Postgres
// (triggers, cron triggers, personas, skills, MCP servers) — and ensures each
// has a resource_groups entry in the "legacy" group.
//
// It is a repair path, not a migration: resources created through the hub API
// get their group at creation time, and the access-control migration covered
// rows that existed then. What remains are resources created out-of-band
// (kubectl apply of an Agent CR) and pre-migration leftovers — which is why
// this still runs at startup rather than being a one-time SQL migration: the
// Kubernetes-backed kinds cannot be enumerated from SQL.
//
// The write is one INSERT … ON CONFLICT DO NOTHING over all refs, so a boot
// costs two round-trips to Postgres regardless of resource count. Assignments
// already made (possibly re-grouped by an admin) are never touched.
// Failures are logged but non-fatal.
func runBackfill(ctx context.Context, c *container) error {
	if !c.mgr.GetCache().WaitForCacheSync(ctx) {
		return fmt.Errorf("cache sync failed, skipping group backfill")
	}
	refs, err := collectResourceRefs(ctx, c.apiClient, c.namespace, c.pool)
	if err != nil {
		return fmt.Errorf("collect resource refs for backfill: %w", err)
	}

	created, err := c.authzStore.EnsureResourceGroupsDefault(ctx, refs, "legacy")
	if err != nil {
		return fmt.Errorf("ensure resource groups: %w", err)
	}

	slog.Info("group backfill complete", "checked", len(refs), "created", created)
	return nil
}

// collectResourceRefs gathers all resource references from Kubernetes and the
// database for group backfill.
func collectResourceRefs(ctx context.Context, c client.Client, ns string, pool *pgxpool.Pool) ([]authz.ResourceRef, error) {
	var refs []authz.ResourceRef

	var agents ainselv1alpha1.AgentList
	if err := c.List(ctx, &agents, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	for _, a := range agents.Items {
		refs = append(refs, authz.ResourceRef{Type: "agent", Name: a.Name})
	}

	// DB-based resources: triggers
	triggerRows, err := pool.Query(ctx, `SELECT id FROM triggers`)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	defer triggerRows.Close()
	for triggerRows.Next() {
		var id string
		if err := triggerRows.Scan(&id); err != nil {
			return nil, err
		}
		refs = append(refs, authz.ResourceRef{Type: "trigger", Name: id})
	}
	if err := triggerRows.Err(); err != nil {
		return nil, err
	}

	// DB-based resources: cron triggers
	cronTriggerRows, err := pool.Query(ctx, `SELECT id FROM cron_triggers`)
	if err != nil {
		return nil, fmt.Errorf("list cron triggers: %w", err)
	}
	defer cronTriggerRows.Close()
	for cronTriggerRows.Next() {
		var id string
		if err := cronTriggerRows.Scan(&id); err != nil {
			return nil, err
		}
		refs = append(refs, authz.ResourceRef{Type: "cron-trigger", Name: id})
	}
	if err := cronTriggerRows.Err(); err != nil {
		return nil, err
	}

	var wConnectors ainselv1alpha1.WebhookConnectorList
	if err := c.List(ctx, &wConnectors, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list webhook connectors: %w", err)
	}
	for _, wc := range wConnectors.Items {
		refs = append(refs, authz.ResourceRef{Type: "connector", Name: wc.Name})
	}

	var images ainselv1alpha1.AgentImageList
	if err := c.List(ctx, &images, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list agent images: %w", err)
	}
	for _, img := range images.Items {
		refs = append(refs, authz.ResourceRef{Type: "agent-image", Name: img.Name})
	}

	// DB-based resources: personas
	rows, err := pool.Query(ctx, `SELECT id FROM personas`)
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		refs = append(refs, authz.ResourceRef{Type: "persona", Name: id})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// DB-based resources: skills
	skillRows, err := pool.Query(ctx, `SELECT id FROM skills`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer skillRows.Close()
	for skillRows.Next() {
		var id string
		if err := skillRows.Scan(&id); err != nil {
			return nil, err
		}
		refs = append(refs, authz.ResourceRef{Type: "skill", Name: id})
	}
	if err := skillRows.Err(); err != nil {
		return nil, err
	}

	// MCP servers
	mcpRows, err := pool.Query(ctx, `SELECT name FROM mcp_servers`)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer mcpRows.Close()
	for mcpRows.Next() {
		var name string
		if err := mcpRows.Scan(&name); err != nil {
			return nil, err
		}
		refs = append(refs, authz.ResourceRef{Type: "mcp-server", Name: name})
	}

	return refs, mcpRows.Err()
}
