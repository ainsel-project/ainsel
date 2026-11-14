import type { ReactNode } from 'react'
import type { AgentEnvVar } from '../../../api/agents'
import type { EnvVar } from '../../../api/agentImages'
import { Button } from '../../../primitives/Button'
import { Input } from '../../../primitives/Input'
import { RegisterTable, type Column } from '../../../primitives/RegisterTable'
import { Tag } from '../../../primitives/Tag'
import {
  envNameErrors,
  newDraftId,
  tableRows,
  type DraftEnvVar,
  type EnvRow,
  type TableRow,
} from './envRows'

const MASK = '••••••••'

type EnvTableProps = {
  /** The editor's draft: the agent's own entries, as edited. */
  agentEnv: DraftEnvVar[]
  /** The entries as last saved; the table keeps rows in this order. */
  savedEnv?: DraftEnvVar[]
  imageEnv?: EnvVar[]
  imageName?: string
  readOnly?: boolean
  onChange?: (next: DraftEnvVar[]) => void
  /**
   * 'merged' (default): the agent's entries merged with its image's, read-only
   * image rows included — the agent overrides editor. 'plain': just the
   * editable list, no Source column and no image rows — the image page's own
   * environment editor and the agent page's read-only image card.
   */
  variant?: 'merged' | 'plain'
}

/** Small inline marker (lock, warning) with a tooltip. */
function Badge({ title, children }: { title: string; children: ReactNode }) {
  return (
    <span
      role="img"
      aria-label={title}
      title={title}
      style={{ display: 'inline-flex', alignItems: 'center', color: 'var(--ink-3)', lineHeight: 1 }}
    >
      {children}
    </span>
  )
}

const LockIcon = () => (
  <svg
    width="11"
    height="11"
    viewBox="0 0 16 16"
    aria-hidden="true"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
  >
    <rect x="3" y="7" width="10" height="7" rx="1.5" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  </svg>
)

/**
 * One table for the variables an agent sees: the image's entries (read-only,
 * with an Override action) and the agent's own entries (overrides and
 * agent-only, editable). Controlled: the parent owns the draft list.
 *
 * Only the image entry's own secret flag masks the image value; an agent
 * entry's secret flag masks the agent's value. Image secrets are never rendered.
 *
 * The plain variant drops the image side entirely: one editable list of
 * name/value/secret rows with a Remove action, used wherever an env list is
 * edited on its own (the image page) or shown read-only (the agent's image
 * card). No fixed column widths: the table keeps its natural size and the
 * `.reg-wrap` scroll takes over when the panel is narrower.
 */
export function EnvTable({
  agentEnv,
  savedEnv,
  imageEnv,
  imageName = '',
  readOnly = false,
  onChange = () => {},
  variant = 'merged',
}: EnvTableProps) {
  const plain = variant === 'plain'
  const imageEntries = imageEnv ?? []
  const errors = envNameErrors(agentEnv)
  const rows: TableRow[] = plain
    ? agentEnv.map((e, i) => ({
        name: e.name,
        kind: 'agent' as const,
        agentValue: e.value,
        secret: !!e.secret,
        agentIndex: i,
      }))
    : tableRows(agentEnv, savedEnv ?? agentEnv, imageEntries)
  const imageSecret = (name: string) => imageEntries.find((e) => e.name === name)?.secret === true
  const shownImageValue = (row: EnvRow) => (imageSecret(row.name) ? MASK : row.imageValue)

  const patchAgent = (index: number, patch: Partial<AgentEnvVar>) =>
    onChange(agentEnv.map((e, i) => (i === index ? { ...e, ...patch } : e)))
  const removeAgent = (index: number) => onChange(agentEnv.filter((_, i) => i !== index))
  const overrideImage = (row: EnvRow) =>
    onChange([
      ...agentEnv,
      {
        id: newDraftId(),
        name: row.name,
        value: row.secret ? '' : (row.imageValue ?? ''),
        secret: row.secret,
      },
    ])

  const actionsCell = (row: TableRow) => {
    if (readOnly) return null
    if (row.kind === 'image') {
      return (
        <Button type="button" size="sm" onClick={() => overrideImage(row)}>
          Override
        </Button>
      )
    }
    if (row.kind === 'override') {
      return (
        <Button type="button" size="sm" onClick={() => removeAgent(row.agentIndex ?? -1)}>
          Reset to image
        </Button>
      )
    }
    return (
      <Button
        type="button"
        size="sm"
        aria-label="Remove variable"
        onClick={() => removeAgent(row.agentIndex ?? -1)}
      >
        Remove
      </Button>
    )
  }

  const columns: Column<TableRow>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (row) => {
        if (row.kind === 'image') {
          const title = `From image ${imageName}${row.secret ? ' · secret' : ''}`
          return (
            <span
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 6,
                color: 'var(--ink-3)',
              }}
            >
              <code>{row.name}</code>
              <Badge title={title}>
                <LockIcon />
              </Badge>
            </span>
          )
        }
        const idx = row.agentIndex ?? -1
        return (
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <Input
                placeholder="NAME"
                aria-label="Variable name"
                value={row.name}
                disabled={readOnly}
                onChange={(e) => patchAgent(idx, { name: e.target.value })}
              />
              {row.kind === 'override' ? (
                <Badge title={`Overrides the image value ${shownImageValue(row)} (${imageName})`}>
                  <span style={{ fontWeight: 700, color: 'var(--signal)' }}>!</span>
                </Badge>
              ) : null}
            </div>
            {errors[idx] ? <div className="field-error">{errors[idx]}</div> : null}
          </div>
        )
      },
    },
    {
      key: 'value',
      header: 'Value',
      cell: (row) => {
        if (row.kind === 'image') {
          return (
            <Input
              aria-label="Image value"
              disabled
              value={row.secret ? '' : (row.imageValue ?? '')}
              placeholder={row.secret ? MASK : undefined}
              type={row.secret ? 'password' : 'text'}
              style={{ color: 'var(--ink-3)' }}
            />
          )
        }
        const idx = row.agentIndex ?? -1
        const secret = agentEnv[idx]?.secret === true
        return (
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Input
                aria-label="Variable value"
                placeholder={secret ? (plain ? MASK : 'unchanged') : 'value'}
                type={secret ? 'password' : 'text'}
                value={row.agentValue ?? ''}
                disabled={readOnly}
                onChange={(e) => patchAgent(idx, { value: e.target.value })}
              />
              <label
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 4,
                  fontSize: 12,
                  whiteSpace: 'nowrap',
                  cursor: 'pointer',
                }}
              >
                <input
                  type="checkbox"
                  checked={secret}
                  disabled={readOnly}
                  onChange={(e) => patchAgent(idx, { secret: e.target.checked })}
                />
                Secret
              </label>
            </div>
            {row.kind === 'override' ? (
              <div
                style={{
                  marginTop: 4,
                  fontSize: 12,
                  color: 'var(--ink-3)',
                  textDecoration: 'line-through',
                }}
              >
                {`${shownImageValue(row)} from image`}
              </div>
            ) : null}
          </div>
        )
      },
    },
    ...(plain
      ? []
      : [
          {
            key: 'source',
            header: 'Source',
            cell: (row: TableRow) => <Tag>{row.kind === 'image' ? 'image' : 'agent'}</Tag>,
          } as Column<TableRow>,
        ]),
    {
      key: 'actions',
      header: '',
      align: 'right',
      cell: (row) => <span style={{ whiteSpace: 'nowrap' }}>{actionsCell(row)}</span>,
    },
  ]

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <RegisterTable
        rows={rows}
        columns={columns}
        rowKey={(row) => (row.kind === 'image' ? `image:${row.name}` : `agent:${row.agentIndex}`)}
        withRowNumbers={false}
        emptyLabel="No variables."
      />
      {readOnly ? null : (
        <div>
          <Button
            type="button"
            size="sm"
            onClick={() =>
              onChange([...agentEnv, { id: newDraftId(), name: '', value: '', secret: false }])
            }
          >
            + Add variable
          </Button>
        </div>
      )}
    </div>
  )
}
