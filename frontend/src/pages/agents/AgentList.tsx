import { useNavigate } from 'react-router-dom'
import { isIdle, useAgents } from '../../api/agents'
import type { AgentSummary } from '../../api/agents'
import { useGroups } from '../../api/groups'
import { useListParams } from '../../hooks/useListParams'
import { Input } from '../../primitives/Input'
import { Select } from '../../primitives/Select'
import { Button } from '../../primitives/Button'
import { Dot } from '../../primitives/Dot'
import { ListPanel } from '../../primitives/ListPanel'
import { Pager } from '../../primitives/Pager'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Tag } from '../../primitives/Tag'
import { ManagedBadge } from '../../components/ManagedBadge'
import { Titleblock } from '../../layout/Titleblock'
import { llmProviderLabel } from './agentFormModel'

// URL keys the roster filters on, beyond page, page size and search.
const EXTRA_KEYS = ['groupId'] as const

export function AgentList() {
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
  const groupParam = filters.groupId ?? ''

  const { data: groups = [] } = useGroups()
  const groupOptions = groups.map((g) => ({ value: g.id, label: g.name }))

  const { data, isLoading, error, refetch } = useAgents({
    page,
    pageSize,
    search: search || undefined,
    groupId: groupParam || undefined,
  })

  const columns: readonly Column<AgentSummary>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (a) => (
        <>
          <b
            style={{ cursor: 'pointer', color: 'var(--accent)' }}
            onClick={() => navigate(`/agents/${encodeURIComponent(a.id)}`)}
          >
            {a.name}
          </b>{' '}
          <ManagedBadge managedBy={a.managedBy} />
        </>
      ),
    },
    {
      key: 'model',
      header: 'Model',
      width: 200,
      cell: (a) => (
        <>
          <span className="num">{a.llm?.model ?? '—'}</span>{' '}
          {a.llm?.vision === true && (
            <Tag title="Image input enabled — screenshots and image attachments are sent to the model (if the endpoint supports it)">
              vision
            </Tag>
          )}
        </>
      ),
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
        // A ChatGPT sign-in agent without a working sign-in runs nothing, and
        // idle would hide why: its queue waits for someone to sign in.
        if (a.status?.signInRequired) {
          return (
            <>
              <Dot state="warn" aria-label="Sign-in required" /> Sign-in required
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
      <div className="page">
        <div className="toolbar" style={{ marginBottom: 12 }}>
          <div className="field">
            <span className="label">Search</span>
            <Input
              placeholder="Search agents…"
              value={searchDraft}
              onChange={(e) => setSearch(e.target.value)}
              aria-label="Search agents"
            />
          </div>
          <div className="field">
            <span className="label">Group</span>
            <Select
              aria-label="Filter by group"
              value={groupParam}
              onChange={(v) => setFilters({ groupId: v })}
              options={groupOptions}
              emptyLabel="All groups"
            />
          </div>
        </div>
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load agents."
          empty="No agents yet."
        >
          {data ? (
            <>
              <RegisterTable rows={data.items} columns={columns} rowKey={(a) => a.id} />
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
