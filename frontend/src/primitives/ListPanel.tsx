import type { ReactNode } from 'react'
import { Panel } from './Panel'
import { SectionStatus } from './SectionStatus'

/** The part of a list query a panel reads: react-query's list hooks all match it. */
export type ListQuery<T = unknown> = {
  data?: T
  isLoading?: boolean
  error?: unknown
  refetch?: () => void
}

type ListPanelProps<T = unknown> = {
  query: ListQuery<T>
  /** Error line, e.g. `Failed to load connectors.` */
  errorTitle?: string
  /** The standard empty state, shown instead of the children when nothing is listed. */
  empty?: ReactNode
  /** Optional call to action rendered under the empty message. */
  emptyAction?: ReactNode
  /**
   * Overrides how emptiness is read from `query.data` — for a query whose data
   * is neither a row array nor `{ items }`.
   */
  isEmpty?: boolean
  title?: string
  right?: ReactNode
  className?: string
  children: ReactNode
}

// rowCount reads the row count out of the two shapes the list endpoints return:
// a bare array, or a page of `{ items, total }`.
function rowCount(data: unknown): number | undefined {
  if (Array.isArray(data)) return data.length
  if (data && typeof data === 'object' && 'items' in data) {
    const items = (data as { items: unknown }).items
    return Array.isArray(items) ? items.length : undefined
  }
  return undefined
}

/**
 * ListPanel is the body of a list page: one Panel that says what it cannot show
 * — loading, failed (with a Retry), or nothing to list — and otherwise renders
 * its children. Pages used to hand-roll all four as `<div className="label">`
 * copies, which drifted apart in wording and left the failures unclickable.
 *
 * The children are only rendered when there is something to render them over, so
 * an empty list shows the empty state alone rather than a header row and a pager
 * reading `00–00 of 0`.
 */
export function ListPanel<T = unknown>({
  query,
  errorTitle = 'Failed to load',
  empty,
  emptyAction,
  isEmpty,
  title,
  right,
  className,
  children,
}: ListPanelProps<T>) {
  const rows = rowCount(query.data)
  const isEmptyList = isEmpty ?? (rows !== undefined && rows === 0)

  let body: ReactNode
  if (query.isLoading) {
    body = <SectionStatus state="loading" />
  } else if (query.error) {
    body = <SectionStatus state="error" title={errorTitle} onRetry={query.refetch} />
  } else if (empty !== undefined && isEmptyList) {
    body = (
      <div className="label" style={{ padding: 14 }}>
        {empty}
        {emptyAction ? <div style={{ marginTop: 10 }}>{emptyAction}</div> : null}
      </div>
    )
  } else {
    body = children
  }

  return (
    <Panel title={title} right={right} className={className}>
      {body}
    </Panel>
  )
}
