import { Link } from 'react-router-dom'
import type { ReactNode } from 'react'

/**
 * Renders an entity name as a link to its detail page — but only when the
 * payload carries the entity's id (older hubs omit these fields) and the id
 * resolves in the side list the page already fetched. A deleted or
 * inaccessible entity therefore renders plain text, never a link to a 404.
 *
 * `path` maps the id to the route (e.g. `/agents/${encodeURIComponent(id)}`).
 * `className` styles the anchor (list rows use `.rowlink`; anchors carry no
 * colour of their own in this design system, so an unstyled one is UA blue).
 * `stopPropagation` keeps the link from activating a clickable parent row.
 *
 * Omit `known` when every id passed in is by definition resolvable — a list
 * page rendering the entities themselves. There the guard would only ever
 * drop the link.
 */
export function EntityLink({
  id,
  known,
  label,
  path,
  className,
  stopPropagation,
}: {
  id?: string
  known?: ReadonlySet<string>
  label: ReactNode
  path: (id: string) => string
  className?: string
  stopPropagation?: boolean
}) {
  if (!id) return <>{label}</>
  if (known && !known.has(id)) return <>{label}</>
  return (
    <Link
      to={path(id)}
      className={className}
      onClick={stopPropagation ? (e) => e.stopPropagation() : undefined}
    >
      {label}
    </Link>
  )
}
