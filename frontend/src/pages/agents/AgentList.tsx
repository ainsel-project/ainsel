import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  isIdle,
  useAgents
} from '../../api/agents'
import type { AgentSummary } from '../../api/agents'
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

  const { data, isLoading, error } = useAgents({ page, pageSize })

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
