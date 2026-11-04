import type { ReactNode } from 'react'
import type { AgentEnvVar } from '../../../api/agents'
import type { EnvVar } from '../../../api/agentImages'
import { Button } from '../../../primitives/Button'
import { Input } from '../../../primitives/Input'
import { RegisterTable, type Column } from '../../../primitives/RegisterTable'
import { Tag } from '../../../primitives/Tag'
import { envNameErrors, tableRows, type EnvRow, type TableRow } from './envRows'

const MASK = '••••••••'

type EnvTableProps = {
  agentEnv: AgentEnvVar[]
  imageEnv: EnvVar[]
  imageName: string
  readOnly: boolean
  onChange: (next: AgentEnvVar[]) => void
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
 * Secret values are never rendered from the image; a secret agent value is
 * masked by the hub, so its field is empty and "unchanged" on submit.
 */
export function EnvTable({ agentEnv, imageEnv, imageName, readOnly, onChange }: EnvTableProps) {
  const errors = envNameErrors(agentEnv)
  const rows = tableRows(agentEnv, imageEnv)

  const patchAgent = (index: number, patch: Partial<AgentEnvVar>) =>
    onChange(agentEnv.map((e, i) => (i === index ? { ...e, ...patch } : e)))
  const removeAgent = (index: number) => onChange(agentEnv.filter((_, i) => i !== index))
  const overrideImage = (row: EnvRow) =>
    onChange([
      ...agentEnv,
      { name: row.name, value: row.secret ? '' : (row.imageValue ?? ''), secret: row.secret },
    ])

  const columns: Column<TableRow>[] = [
    {
      key: 'name',
      header: 'Name',
      width: '28%',
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
        const shown = row.secret ? MASK : row.imageValue
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
                <Badge title={`Overrides the image value ${shown} (${imageName})`}>
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
      width: '34%',
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
        return (
          <div>
            <Input
              aria-label="Variable value"
              placeholder={row.secret ? 'unchanged' : 'value'}
              type={row.secret ? 'password' : 'text'}
              value={row.agentValue ?? ''}
              disabled={readOnly}
              onChange={(e) => patchAgent(idx, { value: e.target.value })}
            />
            {row.kind === 'override' ? (
              <div
                style={{
                  marginTop: 4,
                  fontSize: 12,
                  color: 'var(--ink-3)',
                  textDecoration: 'line-through',
                }}
              >
                {`${row.secret ? MASK : row.imageValue} from image`}
              </div>
            ) : null}
          </div>
        )
      },
    },
    {
      key: 'secret',
      header: 'Secret',
      width: 80,
      cell: (row) => (
        <input
          type="checkbox"
          aria-label="Secret"
          checked={row.secret}
          disabled={row.kind === 'image' || readOnly}
          onChange={(e) => patchAgent(row.agentIndex ?? -1, { secret: e.target.checked })}
        />
      ),
    },
    {
      key: 'source',
      header: 'Source',
      width: 90,
      cell: (row) => <Tag>{row.kind === 'image' ? 'image' : 'agent'}</Tag>,
    },
    {
      key: 'actions',
      header: '',
      width: 120,
      align: 'right',
      cell: (row) => {
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
      },
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
            onClick={() => onChange([...agentEnv, { name: '', value: '', secret: false }])}
          >
            + Add variable
          </Button>
        </div>
      )}
    </div>
  )
}
