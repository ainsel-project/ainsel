import { Fragment, useState, type CSSProperties, type ReactNode } from 'react'

export type Column<T> = {
  key: string
  header: ReactNode
  cell: (row: T) => ReactNode
  width?: string | number
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
}

export type SortState = {
  orderBy: string
  orderDir: 'asc' | 'desc'
}

type RegisterTableProps<T> = {
  rows: T[]
  columns: readonly Column<T>[]
  rowKey: (row: T) => string
  emptyLabel?: ReactNode
  rowClassName?: (row: T) => string | undefined
  withRowNumbers?: boolean
  sort?: SortState
  onSort?: (columnKey: string) => void
  /**
   * Optional expandable-row support. When provided, a leading chevron toggle
   * is rendered per row; clicking it renders the returned content in a detail
   * row beneath. Rows whose detail renders nothing (null/undefined/false/'')
   * are not expandable. When the prop is absent, output is identical to the
   * non-expandable table.
   *
   * The callback is invoked for every row on every render (to decide whether
   * the row is expandable), so it must be pure and cheap.
   */
  renderDetail?: (row: T) => ReactNode
}

const sortIndicatorStyle: CSSProperties = {
  marginLeft: 4,
  fontSize: '0.75em',
  opacity: 0.7,
}

const sortableHeaderStyle: CSSProperties = {
  cursor: 'pointer',
  userSelect: 'none',
}

export function RegisterTable<T>({
  rows,
  columns,
  rowKey,
  emptyLabel,
  rowClassName,
  withRowNumbers = true,
  sort,
  onSort,
  renderDetail,
}: RegisterTableProps<T>) {
  const [expandedKeys, setExpandedKeys] = useState<ReadonlySet<string>>(() => new Set<string>())

  if (rows.length === 0 && emptyLabel) {
    return (
      <div className="label" style={{ padding: '14px' }}>
        {emptyLabel}
      </div>
    )
  }

  const renderHeader = (col: Column<T>) => {
    const isSortable = col.sortable === true && onSort != null
    const isActive = sort?.orderBy === col.key

    if (!isSortable) {
      return col.header
    }

    let indicator = ''
    if (isActive) {
      indicator = sort?.orderDir === 'desc' ? '▼' : '▲'
    }

    return (
      <span
        style={sortableHeaderStyle}
        onClick={() => onSort(col.key)}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onSort(col.key)
          }
        }}
      >
        {col.header}
        {indicator && <span style={sortIndicatorStyle}>{indicator}</span>}
      </span>
    )
  }

  const toggleExpand = (key: string) => {
    setExpandedKeys((prev) => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  // Total columns in the grid: toggle + row number + data columns.
  const colSpan = columns.length + (withRowNumbers ? 1 : 0) + (renderDetail ? 1 : 0)

  return (
    <div className="reg-wrap">
      <table className="reg">
        <thead>
          <tr>
            {renderDetail ? <th className="rowtoggle" aria-label="Details" scope="col" /> : null}
            {withRowNumbers ? <th className="rowno">#</th> : null}
            {columns.map((col) => (
              <th
                key={col.key}
                style={{
                  width: col.width,
                  textAlign: col.align,
                }}
              >
                {renderHeader(col)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, idx) => {
            const key = rowKey(row)
            const detailId = `reg-detail-${key}`
            const detail = renderDetail ? renderDetail(row) : null
            const expandable =
              detail !== null && detail !== undefined && detail !== false && detail !== ''
            const isExpanded = expandable && expandedKeys.has(key)
            return (
              <Fragment key={key}>
                <tr className={rowClassName?.(row)}>
                  {renderDetail ? (
                    <td className="rowtoggle">
                      {expandable ? (
                        <button
                          type="button"
                          className="rowtoggle-btn"
                          aria-expanded={isExpanded}
                          aria-controls={isExpanded ? detailId : undefined}
                          aria-label={isExpanded ? 'Hide details' : 'Show details'}
                          onClick={() => toggleExpand(key)}
                        >
                          {isExpanded ? '▾' : '▸'}
                        </button>
                      ) : null}
                    </td>
                  ) : null}
                  {withRowNumbers ? (
                    <td className="rowno num">{String(idx + 1).padStart(2, '0')}</td>
                  ) : null}
                  {columns.map((col) => (
                    <td key={col.key} style={{ textAlign: col.align }}>
                      {col.cell(row)}
                    </td>
                  ))}
                </tr>
                {isExpanded ? (
                  <tr className="reg-detail">
                    <td colSpan={colSpan} id={detailId} role="region" aria-label="Row details">
                      {detail}
                    </td>
                  </tr>
                ) : null}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
