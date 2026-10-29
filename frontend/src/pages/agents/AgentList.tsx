import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  isIdle,
  useAgents
} from '../../api/agents'
import type { AgentSummary } from '../../api/agents'
import { useGroups } from '../../api/groups'
import { Input } from '../../primitives/Input'
import { Select } from '../../primitives/Select'
import { Button } from '../../primitives/Button'
import { Dot } from '../../primitives/Dot'
import { Pager } from '../../primitives/Pager'
import { Panel } from '../../primitives/Panel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Titleblock } from '../../layout/Titleblock'
import { llmProviderLabel } from './agentFormModel'

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const

export function AgentList() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const page = Math.max(1, Number(params.get('page')) || 1)
  const pageSizeParam = Number(params.get('pageSize'))
  const pageSize = (PAGE_SIZE_OPTIONS as readonly number[]).includes(pageSizeParam)
    ? pageSizeParam
    : 20
  const searchParam = params.get('search') ?? ''
  const groupParam = params.get('groupId') ?? ''

  const [searchInput, setSearchInput] = useState(searchParam)

  // Sync local input when the URL changes externally (e.g. back/forward).
  useEffect(() => {
    setSearchInput(searchParam)
  }, [searchParam])

  // Debounce search input → URL param. Filters drop `page` so the view
  // returns to page 1 of the filtered set instead of stranding the caller
  // on a page that may no longer exist.
  useEffect(() => {
    const timer = setTimeout(() => {
      if (searchInput !== searchParam) {
        setParams((prev) => {
          const next = new URLSearchParams(prev)
          if (searchInput) {
            next.set('search', searchInput)
          } else {
            next.delete('search')
          }
          next.delete('page')
          return next
        }, { replace: true })
      }
    }, 300)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchInput])

  const setGroup = (v: string) => {
    setParams((prev) => {
      const next = new URLSearchParams(prev)
      if (v) {
        next.set('groupId', v)
      } else {
        next.delete('groupId')
      }
      next.delete('page')
      return next
    }, { replace: true })
  }

  const { data: groups = [] } = useGroups()
  const groupOptions = groups.map((g) => ({ value: g.id, label: g.name }))

  const { data, isLoading, error } = useAgents({
    page,
    pageSize,
    search: searchParam || undefined,
    groupId: groupParam || undefined,
  })

  const setPage = (p: number) => {
    const next = new URLSearchParams(params)
    next.set('page', String(p))
    setParams(next, { replace: true })
  }
  const setPageSize = (n: number) => {
    const next = new URLSearchParams(params)
    next.set('pageSize', String(n))
    next.set('page', '1')
    setParams(next, { replace: true })
  }

  const columns: readonly Column<AgentSummary>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (a) => (
        <b
          style={{ cursor: 'pointer', color: 'var(--accent)' }}
          onClick={() => navigate(`/agents/${encodeURIComponent(a.id)}`)}
        >
          {a.name}
        </b>
      ),
    },
    {
      key: 'model',
      header: 'Model',
      width: 200,
      cell: (a) => <span className="num">{a.llm?.model ?? '—'}</span>,
    },
    {
      key: 'provider',
      header: 'Provider',
      width: 160,
      cell: (a) => llmProviderLabel(a.llm?.provider),
    },
    {
      key: 'image',
      header: 'Image',
      width: 200,
      cell: (a) => a.imageRef?.displayName ?? a.imageRef?.name ?? '—',
    },
    {
      key: 'group',
      header: 'Group',
      width: 160,
      // Prefer the group's display name; fall back to the bare id only when
      // the name is unavailable (deleted group, authz disabled).
      cell: (a) => a.groupName || a.groupId || '—',
    },
    {
      key: 'status',
      header: 'Status',
      width: 130,
      cell: (a) => {
        // Paused outranks everything: the agent is healthy, it is just
        // withholding claims — its queue depth is the backlog, not a fault.
        if (a.paused) {
          return (
            <>
              <Dot state="stale" aria-label="Paused" /> Paused
            </>
          )
        }
        // Idle is its own state: with work arriving as one Job per task, an
        // agent with nothing queued or running has no container and nothing is
        // wrong.
        if (isIdle(a.status)) {
          return (
            <>
              <Dot state="stale" aria-label="Idle" /> Idle
            </>
          )
        }
        const ok = a.status?.ready === true
        return (
          <>
            <Dot state={ok ? 'ok' : 'warn'} /> {ok ? 'Ready' : 'Pending'}
          </>
        )
      },
    },
    {
      key: 'running',
      header: 'Running',
      width: 90,
      align: 'right',
      cell: (a) => <span className="num">{a.status?.desired ?? '—'}</span>,
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Fleet / <b>Agents</b>
          </>
        }
        title={
          <>
            Agent <em>Roster</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/agents/new')}>
            + New Agent
          </Button>
        }
      />
      <div style={{ padding: '28px 32px' }}>
        <div className="toolbar" style={{ marginBottom: 12 }}>
          <div className="field">
            <span className="label">Search</span>
            <Input
              placeholder="Search agents…"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              aria-label="Search agents"
            />
          </div>
          <div className="field">
            <span className="label">Group</span>
            <Select
              aria-label="Filter by group"
              value={groupParam}
              onChange={setGroup}
              options={groupOptions}
              emptyLabel="All groups"
            />
          </div>
        </div>
        <Panel className="cropped">
          {isLoading ? (
            <div className="label" style={{ padding: 14 }}>
              Loading…
            </div>
          ) : error ? (
            <div className="label" style={{ padding: 14, color: 'var(--signal)' }}>
              Failed to load agents.
            </div>
          ) : data ? (
            <>
              <RegisterTable
                rows={data.items}
                columns={columns}
                rowKey={(a) => a.id}
                emptyLabel="No agents yet."
              />
              <Pager
                page={page}
                pageSize={pageSize}
                total={data.total}
                pageSizeOptions={PAGE_SIZE_OPTIONS}
                onPageChange={setPage}
                onPageSizeChange={setPageSize}
              />
            </>
          ) : null}
        </Panel>
      </div>
    </>
  )
}
