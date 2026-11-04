import { useMemo, useState } from 'react'
import {
  useCronTriggers,
  useDeleteCronTrigger,
  useUpdateCronTrigger,
} from '../../../api/cronTriggers'
import { useConnectors } from '../../../api/connectors'
import { useInvocations } from '../../../api/invocations'
import { useDeleteTrigger, useTriggers } from '../../../api/triggers'
import { Button } from '../../../primitives/Button'
import { ConfirmModal } from '../../../primitives/ConfirmModal'
import { Panel } from '../../../primitives/Panel'
import { RegisterTable, type Column } from '../../../primitives/RegisterTable'
import { Tag } from '../../../primitives/Tag'
import { ScheduleForm } from '../ScheduleForm'
import { TriggerForm } from '../TriggerForm'
import { SectionPage } from './SectionPage'
import { DAY_MS, toTriggerRows, type TriggerRow } from './triggerRows'
import { useWorkspaceAgent } from './workspaceAgent'

/**
 * The agent's subscriptions and schedules as one list. Hub issue #63: the
 * triggers endpoints ignore ?agent=, so both lists are filtered by agentRef
 * client-side.
 */
export function TriggersSection() {
  const agent = useWorkspaceAgent()
  const [since] = useState(() => new Date(Date.now() - DAY_MS).toISOString())
  const triggersQuery = useTriggers({ pageSize: 200 })
  const cronQuery = useCronTriggers({ pageSize: 200 })
  const { data: connectorData } = useConnectors({ pageSize: 200 })
  const { data: invocationData, error: invocationError } = useInvocations({
    agent: agent.id,
    since,
    pageSize: 200,
  })
  const countsUnavailable = invocationError != null
  const deleteSubscription = useDeleteTrigger()
  const deleteSchedule = useDeleteCronTrigger()
  const updateSchedule = useUpdateCronTrigger()

  const [menuOpen, setMenuOpen] = useState(false)
  const [creating, setCreating] = useState<TriggerRow['type'] | undefined>(undefined)
  const [editing, setEditing] = useState<TriggerRow | undefined>(undefined)
  const [deleteTarget, setDeleteTarget] = useState<TriggerRow | undefined>(undefined)

  const subscriptions = useMemo(
    () => (triggersQuery.data?.items ?? []).filter((t) => t.agentRef === agent.id),
    [triggersQuery.data, agent.id],
  )
  const schedules = useMemo(
    () => (cronQuery.data?.items ?? []).filter((c) => c.agentRef === agent.id),
    [cronQuery.data, agent.id],
  )
  const connectorNames = useMemo(
    () => new Map((connectorData?.items ?? []).map((c) => [c.id, c.name])),
    [connectorData],
  )
  const rows = useMemo(
    () => toTriggerRows(subscriptions, schedules, invocationData?.items ?? [], connectorNames),
    [subscriptions, schedules, invocationData, connectorNames],
  )

  const openNew = (type: TriggerRow['type']) => {
    setMenuOpen(false)
    setEditing(undefined)
    setCreating(type)
  }

  const onConfirmDelete = async () => {
    if (!deleteTarget) return
    if (deleteTarget.type === 'Subscription') {
      await deleteSubscription.mutateAsync(deleteTarget.id)
    } else {
      await deleteSchedule.mutateAsync(deleteTarget.id)
    }
    setDeleteTarget(undefined)
  }

  const deleting = deleteSubscription.isPending || deleteSchedule.isPending

  const columns: readonly Column<TriggerRow>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (r) => (
        <>
          <b>{r.name}</b>
          {r.invalidReason ? (
            <>
              {' '}
              <span title={r.invalidReason}>
                <Tag variant="warn">Invalid</Tag>
              </span>
            </>
          ) : null}
        </>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      width: 120,
      cell: (r) => <Tag>{r.type}</Tag>,
    },
    { key: 'source', header: 'Source', width: 170, cell: (r) => r.source },
    {
      key: 'filter',
      header: 'Filter',
      cell: (r) => (
        <code style={{ fontFamily: 'var(--mono)', fontSize: 12, color: 'var(--ink-2)' }}>
          {r.filter}
        </code>
      ),
    },
    {
      key: 'fired',
      header: 'Fired 24h',
      width: 90,
      align: 'right',
      cell: (r) => (countsUnavailable ? '—' : r.fired24h),
    },
    {
      key: 'failed',
      header: 'Failed 24h',
      width: 90,
      align: 'right',
      cell: (r) =>
        countsUnavailable ? (
          '—'
        ) : (
          <span style={{ color: r.failed24h > 0 ? 'var(--signal)' : undefined }}>
            {r.failed24h}
          </span>
        ),
    },
    {
      key: 'actions',
      header: '',
      width: 220,
      align: 'right',
      cell: (r) => (
        <span style={{ display: 'inline-flex', gap: 6 }}>
          <Button
            size="sm"
            onClick={() => {
              setCreating(undefined)
              setEditing(r)
            }}
          >
            Edit
          </Button>
          {r.type === 'Schedule' ? (
            <Button
              size="sm"
              disabled={updateSchedule.isPending}
              onClick={() => updateSchedule.mutate({ id: r.id, body: { enabled: !r.enabled } })}
            >
              {r.enabled ? 'Pause' : 'Enable'}
            </Button>
          ) : null}
          <Button size="sm" variant="danger" onClick={() => setDeleteTarget(r)}>
            Delete
          </Button>
        </span>
      ),
    },
  ]

  const loading = triggersQuery.isLoading || cronQuery.isLoading
  const failed = triggersQuery.error || cronQuery.error

  const editingSubscription =
    editing?.type === 'Subscription' ? subscriptions.find((t) => t.id === editing.id) : undefined
  const editingSchedule =
    editing?.type === 'Schedule' ? schedules.find((c) => c.id === editing.id) : undefined

  const newMenu = (
    <span style={{ position: 'relative', display: 'inline-flex' }}>
      <Button
        variant="primary"
        size="sm"
        aria-expanded={menuOpen}
        onClick={() => setMenuOpen((open) => !open)}
      >
        New
      </Button>
      {menuOpen ? (
        <span
          style={{
            position: 'absolute',
            right: 0,
            top: '100%',
            marginTop: 4,
            zIndex: 10,
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
            padding: 6,
            minWidth: 150,
            background: 'var(--paper-2)',
            border: '1px solid var(--rule-soft)',
          }}
        >
          <Button size="sm" onClick={() => openNew('Subscription')}>
            Subscription
          </Button>
          <Button size="sm" onClick={() => openNew('Schedule')}>
            Schedule
          </Button>
        </span>
      ) : null}
    </span>
  )

  return (
    <SectionPage title="Triggers" actions={newMenu}>
      <Panel title="Subscriptions & schedules" className="cropped">
        {loading ? (
          <p className="label">Loading triggers…</p>
        ) : failed ? (
          <p className="label" style={{ color: 'var(--signal)' }}>
            Failed to load triggers.
          </p>
        ) : (
          <>
            {countsUnavailable ? (
              <p className="label" style={{ color: 'var(--signal)' }}>
                Run counts unavailable.
              </p>
            ) : null}
            <RegisterTable
              rows={rows}
              columns={columns}
              rowKey={(r) => `${r.type}:${r.id}`}
              rowClassName={(r) => (r.invalidReason ? 'row-err' : undefined)}
              emptyLabel="Nothing flows into this agent yet — add a subscription or a schedule."
            />
          </>
        )}
      </Panel>

      {creating === 'Subscription' ? (
        <TriggerForm
          agentId={agent.id}
          onClose={() => setCreating(undefined)}
          onSaved={() => setCreating(undefined)}
        />
      ) : null}
      {creating === 'Schedule' ? (
        <ScheduleForm
          agentId={agent.id}
          onClose={() => setCreating(undefined)}
          onSaved={() => setCreating(undefined)}
        />
      ) : null}
      {editingSubscription ? (
        <TriggerForm
          agentId={agent.id}
          trigger={editingSubscription}
          onClose={() => setEditing(undefined)}
          onSaved={() => setEditing(undefined)}
        />
      ) : null}
      {editingSchedule ? (
        <ScheduleForm
          agentId={agent.id}
          trigger={editingSchedule}
          onClose={() => setEditing(undefined)}
          onSaved={() => setEditing(undefined)}
        />
      ) : null}

      <ConfirmModal
        open={deleteTarget !== undefined}
        title={deleteTarget?.type === 'Schedule' ? 'Delete schedule?' : 'Delete subscription?'}
        body={
          <>
            <b>{deleteTarget?.name}</b> will be permanently removed.
          </>
        }
        confirmLabel={deleting ? 'Deleting…' : 'Delete'}
        destructive
        onConfirm={onConfirmDelete}
        onCancel={() => setDeleteTarget(undefined)}
      />
    </SectionPage>
  )
}
