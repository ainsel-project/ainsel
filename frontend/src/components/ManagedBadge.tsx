import { Tag } from '../primitives/Tag'
import { isManaged, managedByLabel } from '../utils/managed'

/**
 * Badge marking a resource as authored by an external system (issue #146).
 * Renders nothing for console-managed resources, so call sites can use it
 * unconditionally.
 */
export function ManagedBadge({ managedBy }: { managedBy?: string }) {
  if (managedBy == null || !isManaged(managedBy)) return null
  return (
    <span title={`${managedByLabel(managedBy)} — read-only in the console`}>
      <Tag variant="warn">{managedByLabel(managedBy)}</Tag>
    </span>
  )
}
