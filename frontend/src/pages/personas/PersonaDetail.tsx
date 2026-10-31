import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useAgents } from '../../api/agents'
import { ApiError } from '../../api/client'
import { useDeletePersona, usePersona } from '../../api/personas'
import { usePlugins } from '../../api/plugins'
import { EntityLink } from '../../components/EntityLink'
import { ManagedBadge } from '../../components/ManagedBadge'
import { agentPath } from '../../components/entityPaths'
import { isManaged } from '../../utils/managed'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { Markdown } from '../../primitives/Markdown'
import { Panel } from '../../primitives/Panel'
import { Tabs } from '../../primitives/Tabs'
import { Titleblock } from '../../layout/Titleblock'
import { formatISO } from '../../utils/time'
import { PersonaVersionHistory } from './PersonaVersionHistory'
import { PluginSlot } from '../../plugins/PluginSlot'
import { activePlugins, slotContributions } from '../../plugins/registry'

const BUILT_IN_TABS = [
  { value: 'overview', label: 'Overview' },
  { value: 'history', label: 'History' },
] as const

export function PersonaDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [tab, setTab] = useState<string>('overview')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [referrers, setReferrers] = useState<{ agentName: string; agentId?: string }[]>([])
  const { data, isLoading, error } = usePersona(id)
  const { data: agents } = useAgents({ pageSize: 100 })
  const del = useDeletePersona()
  const { data: pluginsList } = usePlugins()

  const referringAgents = (agents?.items ?? []).filter((a) => a.persona?.id === id)
  // Side list decides which delete-409 referrers are linkable: an agent that
  // was deleted since the 409 renders plain text, not a 404.
  const agentIds = useMemo(() => new Set((agents?.items ?? []).map((a) => a.id)), [agents])

  // Build tabs list with plugin contributions
  const plugins = useMemo(() => activePlugins(pluginsList ?? []), [pluginsList])
  const pluginContributions = useMemo(() => slotContributions(plugins, 'persona.panel'), [plugins])

  const tabs = useMemo(() => {
    const builtIn = [...BUILT_IN_TABS]
    const pluginTabs = pluginContributions.map((c) => ({
      value: `plugin:${c.plugin.id}`,
      label: c.plugin.displayName,
    }))
    return [...builtIn, ...pluginTabs]
  }, [pluginContributions])

  const onDelete = async () => {
    if (!id) return
    try {
      await del.mutateAsync(id)
      setConfirmOpen(false)
      navigate('/personas', { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        const body = err.body as
          { referrers?: { agentName: string; agentId?: string }[] } | undefined
        setReferrers(body?.referrers ?? [])
      } else {
        throw err
      }
    }
  }

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Library / <Link to="/personas">Personas</Link> / <b>{data?.name ?? id ?? '—'}</b>
          </>
        }
        title={
          <>
            {data?.name ?? <em>Persona</em>} <ManagedBadge managedBy={data?.managedBy} />
          </>
        }
        actions={
          isManaged(data?.managedBy) ? null : (
            <>
              <Button onClick={() => id && navigate(`/personas/${encodeURIComponent(id)}/edit`)}>
                Edit
              </Button>
              <Button variant="danger" onClick={() => setConfirmOpen(true)}>
                Delete
              </Button>
            </>
          )
        }
      />
      <div style={{ padding: '28px 32px' }}>
        <Tabs value={tab} onChange={setTab} tabs={tabs} aria-label="Persona sections" />
        <div style={{ marginTop: 24 }}>
          {isLoading ? <p className="label">Loading…</p> : null}
          {error ? (
            <p className="label" style={{ color: 'var(--signal)' }}>
              Failed to load persona.
            </p>
          ) : null}

          {data && tab.startsWith('plugin:') ? (
            <PluginSlot
              slot="persona.panel"
              props={{ personaId: data.id }}
              pluginId={tab.substring('plugin:'.length)}
            />
          ) : null}

          {data && tab === 'overview' ? (
            <div style={{ display: 'grid', gap: 24 }}>
              <Panel title="Identity" className="cropped">
                <div className="info-grid">
                  <div>
                    <div className="k">ID</div>
                    <div className="v num">{data.id}</div>
                  </div>
                  <div>
                    <div className="k">Name</div>
                    <div className="v">{data.name}</div>
                  </div>
                  <div>
                    <div className="k">Description</div>
                    <div className="v">{data.description || '—'}</div>
                  </div>
                  <div>
                    <div className="k">Version</div>
                    <div className="v">v{data.currentVersion}</div>
                  </div>
                  <div>
                    <div className="k">Created</div>
                    <div className="v num">{formatISO(data.createdAt)}</div>
                  </div>
                  <div>
                    <div className="k">Updated</div>
                    <div className="v num">{formatISO(data.updatedAt)}</div>
                  </div>
                </div>
              </Panel>

              <Panel title="Used by" className="cropped">
                {referringAgents.length === 0 ? (
                  <p className="label">No agents reference this persona yet.</p>
                ) : (
                  <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
                    {referringAgents.map((a) => (
                      <li key={a.id} style={{ padding: '4px 0' }}>
                        <Link to={`/agents/${encodeURIComponent(a.id)}`}>{a.name}</Link>
                      </li>
                    ))}
                  </ul>
                )}
              </Panel>

              <Panel title="Persona text" className="cropped">
                <Markdown source={data.text} />
              </Panel>
            </div>
          ) : null}

          {data && tab === 'history' ? (
            <PersonaVersionHistory
              personaId={data.id}
              currentVersion={data.currentVersion}
              readOnly={isManaged(data.managedBy)}
            />
          ) : null}
        </div>
      </div>

      <ConfirmModal
        open={confirmOpen}
        title={`Delete persona "${data?.name ?? ''}"?`}
        destructive
        confirmLabel={
          referrers.length > 0 ? 'Cannot delete' : del.isPending ? 'Deleting…' : 'Delete'
        }
        body={
          referrers.length > 0 ? (
            <div>
              <p>
                This persona is referenced by {referrers.length} agent
                {referrers.length === 1 ? '' : 's'}. Unreference
                {referrers.length === 1 ? ' it' : ' them'} before deleting:
              </p>
              <ul>
                {referrers.map((r) => (
                  <li key={r.agentId ?? r.agentName}>
                    <EntityLink
                      id={r.agentId}
                      known={agentIds}
                      label={r.agentName}
                      path={agentPath}
                    />
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <p>This will permanently delete the persona and its version history.</p>
          )
        }
        onConfirm={() => {
          if (referrers.length === 0) onDelete()
        }}
        onCancel={() => {
          setConfirmOpen(false)
          setReferrers([])
        }}
      />
    </>
  )
}
