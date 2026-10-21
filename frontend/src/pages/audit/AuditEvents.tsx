import { useState } from 'react'
import { useAuthEvents, AUTH_EVENT_TYPES, type ListAuthEventsParams } from '../../api/auditEvents'
import { useCurrentUser } from '../../hooks/useCurrentUser'
import { Button } from '../../primitives/Button'
import { Input } from '../../primitives/Input'
import { Panel } from '../../primitives/Panel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Titleblock } from '../../layout/Titleblock'
import type { AuthEvent } from '../../api/auditEvents'

const PAGE_SIZE = 50

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/**
 * Admin-only audit trail of persisted auth events (issue #116): logins,
 * password changes, and user-management actions. The endpoint is admin-gated
 * server-side (403 for everyone else, including no-auth mode); non-admins see
 * an honest notice instead of a table they cannot read.
 */
export function AuditEvents() {
  const { data: currentUser } = useCurrentUser()
  const isAdmin = currentUser?.isAdmin === true

  // Committed filters drive the query; draft inputs only become committed on
  // Apply, so typing does not fire a request per keystroke.
  const [filters, setFilters] = useState<ListAuthEventsParams>({})
  const [page, setPage] = useState(1)
  const [draftUser, setDraftUser] = useState('')
  const [draftType, setDraftType] = useState('')
  const [draftFrom, setDraftFrom] = useState('')
  const [draftTo, setDraftTo] = useState('')

  const query: ListAuthEventsParams = { ...filters, page, pageSize: PAGE_SIZE }
  const { data, isLoading, error } = useAuthEvents(query)

  const apply = () => {
    const toRfc3339 = (v: string) => (v ? new Date(v).toISOString() : undefined)
    setFilters({
      user: draftUser.trim() || undefined,
      type: draftType || undefined,
      from: toRfc3339(draftFrom),
      to: toRfc3339(draftTo),
    })
    setPage(1)
  }

  const reset = () => {
    setDraftUser('')
    setDraftType('')
    setDraftFrom('')
    setDraftTo('')
    setFilters({})
    setPage(1)
  }

  const columns: readonly Column<AuthEvent>[] = [
    {
      key: 'occurredAt',
      header: 'Time',
      width: 180,
      cell: (e) => <span className="num">{formatTime(e.occurredAt)}</span>,
    },
    {
      key: 'eventType',
      header: 'Event',
      width: 190,
      cell: (e) => <b>{e.eventType}</b>,
    },
    { key: 'actorSub', header: 'Actor', cell: (e) => <span className="num">{e.actorSub}</span> },
    { key: 'target', header: 'Target', cell: (e) => <span className="num">{e.target}</span> },
    {
      key: 'sourceIp',
      header: 'Source IP',
      width: 140,
      cell: (e) => <span className="num">{e.sourceIp || '—'}</span>,
    },
    {
      key: 'outcome',
      header: 'Outcome',
      width: 90,
      align: 'center',
      cell: (e) => (
        <span style={{ color: e.outcome === 'success' ? undefined : 'var(--signal)' }}>
          {e.outcome}
        </span>
      ),
    },
  ]

  const renderBody = () => {
    if (!isAdmin) {
      return (
        <div className="label" style={{ padding: 14 }}>
          The audit trail is restricted to administrators.
        </div>
      )
    }
    if (isLoading) {
      return (
        <div className="label" style={{ padding: 14 }}>
          Loading…
        </div>
      )
    }
    if (error) {
      return (
        <div className="label" style={{ padding: 14, color: 'var(--signal)' }}>
          Failed to load audit events.
        </div>
      )
    }
    if (!data) return null
    return (
      <>
        <RegisterTable
          rows={data.items}
          columns={columns}
          rowKey={(e) => String(e.id)}
          emptyLabel="No audit events match."
        />
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: '10px 14px',
          }}
        >
          <span className="label">
            {data.total} event{data.total === 1 ? '' : 's'} · page {data.page} of{' '}
            {Math.max(data.totalPages, 1)}
          </span>
          <span style={{ display: 'inline-flex', gap: 8 }}>
            <Button
              variant="ghost"
              size="sm"
              disabled={page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              Previous
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={page >= data.totalPages}
              onClick={() => setPage((p) => p + 1)}
            >
              Next
            </Button>
          </span>
        </div>
      </>
    )
  }

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Audit</b>
          </>
        }
        title={
          <>
            Auth <em>Events</em>
          </>
        }
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 16 }}>
        <Panel title="Filters">
          <form
            onSubmit={(e) => {
              e.preventDefault()
              apply()
            }}
            style={{
              padding: 16,
              display: 'flex',
              flexWrap: 'wrap',
              gap: 12,
              alignItems: 'flex-end',
            }}
          >
            <label style={{ display: 'grid', gap: 4 }}>
              <span className="label">User (actor or target)</span>
              <Input
                value={draftUser}
                onChange={(e) => setDraftUser(e.target.value)}
                placeholder="local:alice"
                style={{ minWidth: 180 }}
              />
            </label>
            <label style={{ display: 'grid', gap: 4 }}>
              <span className="label">Type</span>
              <select
                value={draftType}
                onChange={(e) => setDraftType(e.target.value)}
                style={{ minWidth: 180 }}
              >
                <option value="">All types</option>
                {AUTH_EVENT_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </label>
            <label style={{ display: 'grid', gap: 4 }}>
              <span className="label">From</span>
              <Input
                type="datetime-local"
                value={draftFrom}
                onChange={(e) => setDraftFrom(e.target.value)}
              />
            </label>
            <label style={{ display: 'grid', gap: 4 }}>
              <span className="label">To</span>
              <Input
                type="datetime-local"
                value={draftTo}
                onChange={(e) => setDraftTo(e.target.value)}
              />
            </label>
            <span style={{ display: 'inline-flex', gap: 8 }}>
              <Button variant="primary" type="submit">
                Apply
              </Button>
              <Button variant="ghost" onClick={reset}>
                Reset
              </Button>
            </span>
          </form>
        </Panel>
        <Panel className="cropped">{renderBody()}</Panel>
      </div>
    </>
  )
}
