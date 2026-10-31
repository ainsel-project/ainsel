import { Link } from 'react-router-dom'
import type { ReactNode } from 'react'

/**
 * Renders an entity name as a link to its detail page — but only when the
 * payload carries the entity's id (older hubs omit these fields) and the id
 * resolves in the side list the page already fetched. A deleted or
 * inaccessible entity therefore renders plain text, never a link to a 404.
 *
 * `path` maps the id to the route (e.g. `/agents/${encodeURIComponent(id)}`).
 * `stopPropagation` keeps the link from activating a clickable parent row.
 */
export function EntityLink({
  id,
  known,
  label,
  path,
  stopPropagation,
}: {
  id?: string
  known?: ReadonlySet<string>
  label: ReactNode
  path: (id: string) => string
  stopPropagation?: boolean
}) {
  if (!id || !known?.has(id)) return <>{label}</>
  return (
    <Link
      to={path(id)}
      onClick={stopPropagation ? (e) => e.stopPropagation() : undefined}
    >
      {label}
    </Link>
  )
}
