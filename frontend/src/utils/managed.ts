/**
 * managedBy provenance (issue #146).
 *
 * Every resource (agents, agent images, connectors, personas, skills, MCP
 * servers) carries an optional `managedBy` naming the external system that
 * authors it — e.g. "gitops", "helm", "plugin". Any value other than empty or
 * "user" means the resource has exactly one author outside the console: the
 * console must render it read-only so it is never edited from two places.
 * The hub enforces the same rule server-side (mutations answer 409).
 */
export const MANAGED_BY_USER = 'user'

/** True when a managedBy value marks the resource externally managed. */
export function isManaged(managedBy?: string): boolean {
  return managedBy != null && managedBy !== '' && managedBy !== MANAGED_BY_USER
}

/** Human label for an externally managed resource, e.g. 'managed by gitops'. */
export function managedByLabel(managedBy: string): string {
  return `managed by ${managedBy}`
}
