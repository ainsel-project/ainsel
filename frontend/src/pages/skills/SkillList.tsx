import { useNavigate } from 'react-router-dom'
import { useSkills, type SkillSummary } from '../../api/skills'
import { useListParams } from '../../hooks/useListParams'
import { Button } from '../../primitives/Button'
import { Input } from '../../primitives/Input'
import { ListPanel } from '../../primitives/ListPanel'
import { Pager } from '../../primitives/Pager'
import { RegisterTable, type Column, type SortState } from '../../primitives/RegisterTable'
import { Tag } from '../../primitives/Tag'
import { EntityLink } from '../../components/EntityLink'
import { skillPath } from '../../components/entityPaths'
import { ManagedBadge } from '../../components/ManagedBadge'
import { Titleblock } from '../../layout/Titleblock'
import { formatRelative } from '../../utils/time'

// Maps column keys to the API orderBy values.
const SORTABLE_COLUMNS: Record<string, string> = {
  name: 'name',
  id: 'id',
  usedBy: 'usedBy',
  updated: 'updatedAt',
}

// Reverse lookup: API orderBy value → column key.
const API_TO_COLUMN_KEY: Record<string, string> = Object.fromEntries(
  Object.entries(SORTABLE_COLUMNS).map(([k, v]) => [v, k]),
)

// Default direction when a column is first clicked.
const DEFAULT_DIRECTIONS: Record<string, 'asc' | 'desc'> = {
  name: 'asc',
  id: 'asc',
  usedBy: 'desc',
  updated: 'desc',
}

// URL keys the library filters on, beyond page, page size and search.
const EXTRA_KEYS = ['tags', 'orderBy', 'orderDir'] as const

export function SkillList() {
  const navigate = useNavigate()
  const {
    page,
    pageSize,
    pageSizes,
    search,
    searchDraft,
    setSearch,
    setPage,
    setPageSize,
    filters,
    setFilters,
  } = useListParams({ extraKeys: EXTRA_KEYS })
  const tagsParam = filters.tags ?? ''
  const orderByParam = filters.orderBy ?? ''
  const orderDirParam = filters.orderDir ?? ''

  const activeTags = tagsParam ? tagsParam.split(',').filter(Boolean) : []

  const sortState: SortState | undefined =
    orderByParam && API_TO_COLUMN_KEY[orderByParam]
      ? {
          orderBy: API_TO_COLUMN_KEY[orderByParam],
          orderDir: orderDirParam === 'desc' ? 'desc' : 'asc',
        }
      : undefined

  const { data, isLoading, error, refetch } = useSkills({
    page,
    pageSize,
    search: search || undefined,
    tags: tagsParam || undefined,
    orderBy: sortState ? SORTABLE_COLUMNS[sortState.orderBy] : undefined,
    orderDir: sortState?.orderDir,
  })

  // Collect all unique tags from current results for filter chips
  const availableTags = data ? [...new Set(data.items.flatMap((s) => s.tags ?? []))].sort() : []

  const toggleTag = (tag: string) => {
    const next = activeTags.includes(tag)
      ? activeTags.filter((t) => t !== tag)
      : [...activeTags, tag]
    setFilters({ tags: next.join(',') })
  }

  const handleSort = (columnKey: string) => {
    const apiColumn = SORTABLE_COLUMNS[columnKey]
    if (!apiColumn) return
    // Same column: flip the direction. New column: its default direction.
    let orderDir: string
    if (orderByParam === apiColumn) {
      orderDir = orderDirParam === 'desc' ? 'asc' : 'desc'
    } else {
      orderDir = DEFAULT_DIRECTIONS[columnKey] || 'asc'
    }
    setFilters({ orderBy: apiColumn, orderDir })
  }

  const columns: readonly Column<SkillSummary>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      cell: (s) => (
        <>
          <EntityLink id={s.id} label={s.name} path={skillPath} className="rowlink" />{' '}
          <ManagedBadge managedBy={s.managedBy} />
        </>
      ),
    },
    {
      key: 'id',
      header: 'ID',
      width: 180,
      sortable: true,
      cell: (s) => <span className="num">{s.id}</span>,
    },
    {
      key: 'description',
      header: 'Description',
      cell: (s) => <span style={{ color: 'var(--ink-3)' }}>{s.description || '—'}</span>,
    },
    {
      key: 'tags',
      header: 'Tags',
      width: 200,
      cell: (s) =>
        s.tags && s.tags.length > 0 ? (
          <span style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
            {s.tags.map((t) => (
              <Tag key={t}>{t}</Tag>
            ))}
          </span>
        ) : (
          <span style={{ color: 'var(--ink-3)' }}>—</span>
        ),
    },
    {
      key: 'usedBy',
      header: 'Used by',
      width: 90,
      align: 'right',
      sortable: true,
      cell: (s) =>
        s.usedBy ? (
          <span className="num">{s.usedBy}</span>
        ) : (
          <span className="num" style={{ color: 'var(--ink-3)' }}>
            —
          </span>
        ),
    },
    {
      key: 'updated',
      header: 'Updated',
      width: 130,
      align: 'right',
      sortable: true,
      cell: (s) => <span className="num">{formatRelative(s.updatedAt)}</span>,
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Library / <b>Skills</b>
          </>
        }
        title={
          <>
            Skill <em>Library</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/skills/new')}>
            ＋ New Skill
          </Button>
        }
      />
      <div className="page">
        <div
          style={{
            display: 'flex',
            gap: 12,
            marginBottom: 16,
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          <Input
            placeholder="Search skills…"
            value={searchDraft}
            onChange={(e) => setSearch(e.target.value)}
            style={{ maxWidth: 320 }}
            aria-label="Search skills"
          />
          {availableTags.length > 0 && (
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
              {availableTags.map((t) => (
                <Tag
                  key={t}
                  variant={activeTags.includes(t) ? 'ok' : 'default'}
                  solid={activeTags.includes(t)}
                  className="clickable"
                >
                  <span
                    style={{ cursor: 'pointer' }}
                    onClick={() => toggleTag(t)}
                    role="button"
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        toggleTag(t)
                      }
                    }}
                  >
                    {t}
                  </span>
                </Tag>
              ))}
            </div>
          )}
        </div>
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load skills."
          empty={
            search || tagsParam
              ? 'No skills match the current filters.'
              : 'No skills yet. Create one to define reusable agent capabilities.'
          }
        >
          {data ? (
            <>
              <RegisterTable
                rows={data.items}
                columns={columns}
                rowKey={(s) => s.id}
                rowClassName={() => 'clickable'}
                sort={sortState}
                onSort={handleSort}
              />
              <Pager
                page={page}
                pageSize={pageSize}
                total={data.total}
                pageSizeOptions={pageSizes}
                onPageChange={setPage}
                onPageSizeChange={setPageSize}
              />
            </>
          ) : null}
        </ListPanel>
      </div>
    </>
  )
}
