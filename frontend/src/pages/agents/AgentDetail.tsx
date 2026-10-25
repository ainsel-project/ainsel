import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import {
  isIdle,
  useAgent,
  useDeleteAgent,
  usePauseAgent,
  useResumeAgent,
  type PauseAgentRequest
} from '../../api/agents'
import { ApiError } from '../../api/client'
import { useAgentImages } from '../../api/agentImages'
import { EntityLink } from '../../components/EntityLink'
import { agentImagePath } from '../../components/entityPaths'
import { useAgentPersona } from '../../api/personas'
import { usePlugins } from '../../api/plugins'
import { recordAgentView, recentsScope } from '../../agentRecents'
import { useAuth } from '../../auth/AuthProvider'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { Dot } from '../../primitives/Dot'
import { Markdown } from '../../primitives/Markdown'
import { Panel } from '../../primitives/Panel'
import { Tabs } from '../../primitives/Tabs'
import { Tag } from '../../primitives/Tag'
import { Titleblock } from '../../layout/Titleblock'
import { AgentChannelTab } from '../channels/AgentChannelTab'
import { AgentTriggers } from './AgentTriggers'
import { AgentPersonaSection } from './AgentPersonaSection'
import {
  AgentImageSection,
  AgentToolsSection,
  AgentMCPSection,
  AgentSkillsSection,
} from './AgentImageSection'
import { AgentEnvSection } from './AgentEnvSection'
import { llmProviderLabel } from './agentFormModel'
import { PluginSlot } from '../../plugins/PluginSlot'
import { activePlugins, slotContributions } from '../../plugins/registry'

const BUILT_IN_TABS = [
  { value: 'overview', label: 'Overview' },
  { value: 'channel', label: 'Channel' },
  { value: 'triggers', label: 'Triggers' },
  { value: 'persona', label: 'Persona' },
  { value: 'runtime', label: 'Runtime' },
  { value: 'tools', label: 'Tools' },
  { value: 'skills', label: 'Skills' },
] as const

// Duration presets for the pause dialog. 'indefinite' sends an empty body:
// the pause then lasts until someone resumes explicitly.
const PAUSE_PRESETS: { value: string; label: string }[] = [
  { value: '30m', label: '30 minutes' },
  { value: '8h', label: '8 hours' },
  { value: '24h', label: '24 hours' },
  { value: '168h', label: '7 days' },
  { value: 'indefinite', label: 'Indefinite' },
]

export function AgentDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [pauseOpen, setPauseOpen] = useState(false)
  const [pauseDuration, setPauseDuration] = useState('8h')
  const [pauseError, setPauseError] = useState<string | null>(null)
  const { data, isLoading, error } = useAgent(id)
  // Side list decides whether the image reference is linkable: an image that
  // was deleted renders the plain switch-to-runtime control, not a 404.
  const { data: imageData } = useAgentImages({ pageSize: 200 })
  const imageIds = useMemo(
    () => new Set((imageData?.items ?? []).map((i) => i.id)),
    [imageData],
  )
  const remove = useDeleteAgent()
  const pause = usePauseAgent()
  const resume = useResumeAgent()
  const { user } = useAuth()
  const { data: pluginsList } = usePlugins()

  // Build tabs list with plugin contributions
  const plugins = useMemo(() => activePlugins(pluginsList ?? []), [pluginsList])
  const pluginContributions = useMemo(
    () => slotContributions(plugins, 'agent.panel'),
    [plugins],
  )

  const tabs = useMemo(() => {
    const builtIn = [...BUILT_IN_TABS]
    const pluginTabs = pluginContributions.map((c) => ({
      value: `plugin:${c.plugin.id}`,
      label: c.plugin.displayName,
    }))
    return [...builtIn, ...pluginTabs]
  }, [pluginContributions])

  const tab =
    tabParam && tabs.some((t) => t.value === tabParam) ? tabParam : 'overview'

  const onTabChange = (next: string) => {
    setSearchParams(next === 'overview' ? {} : { tab: next }, { replace: true })
  }

  // Feed the nav's recent-agents list. Keyed on the loaded agent's id, not the
  // raw route param, so an unresolvable URL cannot be remembered as "viewed".
  // (The nav also filters these against what the hub returns you, so the
  // worst case for a stale id is an unused entry that the cap ages out.)
  useEffect(() => {
    if (data?.id) recordAgentView(recentsScope(user), data.id)
  }, [data?.id, user])

  const onConfirmDelete = async () => {
    if (!id) return
    setDeleteError(null)
    try {
      await remove.mutateAsync(id)
      setConfirmOpen(false)
      navigate('/agents', { replace: true })
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : 'Failed to delete agent.'
      setDeleteError(msg)
    }
  }

  // The operator's run account says whether work is in flight. An agent with
  // nothing queued or running is idle by design — the Deployment is only the
  // pod template; every task runs as its own Job.
  const idle = isIdle(data?.status)

  const onConfirmPause = async () => {
    if (!id) return
    setPauseError(null)
    const body: PauseAgentRequest =
      pauseDuration === 'indefinite' ? {} : { duration: pauseDuration }
    try {
      await pause.mutateAsync({ id, body })
      setPauseOpen(false)
    } catch (err) {
      setPauseError(err instanceof ApiError ? err.message : 'Failed to pause agent.')
    }
  }

  const onResume = async () => {
    if (!id) return
    try {
      await resume.mutateAsync(id)
    } catch {
      // The refetch leaves the agent paused; the button stays available.
    }
  }

  // Paused is not broken: tasks route on and wait in the agent's queue,
  // draining in order when the pause ends. Show it as its own state so the
  // Ready dot does not read as a fault.
  const paused = data?.paused === true

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Fleet / <Link to="/agents">Agents</Link> /{' '}
            <b>{data?.name ?? id ?? '—'}</b>
          </>
        }
        title={
          <>
            {data?.name ?? <em>Agent</em>}
            {paused ? (
              <span style={{ marginLeft: 10 }}>
                <Tag variant="warn">
                  Paused
                  {data?.pausedUntil
                    ? ` · resumes ${new Date(data.pausedUntil).toLocaleString()}`
                    : ' · until resumed'}
                </Tag>
              </span>
            ) : null}
          </>
        }
        actions={
          <>
            {paused ? (
              <Button variant="primary" onClick={onResume} disabled={resume.isPending}>
                {resume.isPending ? 'Resuming…' : 'Resume'}
              </Button>
            ) : (
              <Button onClick={() => setPauseOpen(true)}>Pause</Button>
            )}
            <Button
              onClick={() => id && navigate(`/agents/${encodeURIComponent(id)}/edit`)}
            >
              Edit
            </Button>
            <Button variant="danger" onClick={() => setConfirmOpen(true)}>
              Delete
            </Button>
          </>
        }
      />
      <div style={{ padding: '28px 32px' }}>
        <Tabs value={tab} onChange={onTabChange} tabs={tabs} aria-label="Agent sections" />
        <div style={{ marginTop: 24 }}>
          {isLoading ? <p className="label">Loading…</p> : null}
          {error ? (
            <p className="label" style={{ color: 'var(--signal)' }}>
              Failed to load agent.
            </p>
          ) : null}
          {data && tab.startsWith('plugin:') ? (
            <PluginSlot
              slot="agent.panel"
              props={{ agentName: data.name }}
              pluginId={tab.substring('plugin:'.length)}
            />
          ) : null}
          {data && tab === 'overview' ? (
            <div style={{ display: 'grid', gap: 24 }}>
              <Panel title="General" className="cropped">
                <div className="info-grid" style={{ gridTemplateColumns: 'repeat(5, 1fr)' }}>
                  <div>
                    <div className="k">Name</div>
                    <div className="v">{data.name}</div>
                  </div>
                  <div>
                    <div className="k">Model</div>
                    <div className="v">{data.llm?.model ?? '—'}</div>
                  </div>
                  <div>
                    <div className="k">Provider</div>
                    <div className="v">{llmProviderLabel(data.llm?.provider)}</div>
                  </div>
                  <div>
                    <div className="k">Image input</div>
                    <div className="v">{data.llm?.vision ? 'Images on' : 'Text only'}</div>
                  </div>
                  <div>
                    <div className="k">Image</div>
                    <div className="v">
                      {data.imageRef ? (
                        data.imageRef.id && imageIds.has(data.imageRef.id) ? (
                          <EntityLink
                            id={data.imageRef.id}
                            known={imageIds}
                            label={data.imageRef.displayName ?? data.imageRef.name}
                            path={agentImagePath}
                          />
                        ) : (
                          <Button variant="ghost" onClick={() => onTabChange('runtime')}>
                            {data.imageRef.displayName ?? data.imageRef.name}
                          </Button>
                        )
                      ) : (
                        '—'
                      )}
                    </div>
                  </div>
                </div>
                {data.description ? (
                  <p style={{ marginTop: 8, color: 'var(--ink-2)' }}>{data.description}</p>
                ) : null}
              </Panel>

              <Panel title="Runtime Status" className="cropped">
                <div className="info-grid">
                  <div>
                    <div className="k">Ready</div>
                    <div className="v">
                      <Dot state={idle ? 'stale' : data.status?.ready ? 'ok' : 'warn'} />{' '}
                      {idle ? 'Idle' : data.status?.ready ? 'Ready' : 'Pending'}
                    </div>
                  </div>
                  <div>
                    <div className="k">Tasks running</div>
                    <div className="v">
                      {data.status?.desired ?? 0} of up to {data.replicas ?? '—'}
                    </div>
                  </div>
                </div>
              </Panel>

              {data.enabledTools && data.enabledTools.length > 0 ? (
                <Panel title={`Enabled Tools · ${data.enabledTools.length}`} className="cropped">
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                    {data.enabledTools.map((t) => (
                      <Tag key={t}>{t}</Tag>
                    ))}
                  </div>
                </Panel>
              ) : null}

              <PersonaPanel
                agentId={data.id}
                onConfigure={() => onTabChange('persona')}
              />
            </div>
          ) : null}

          {data && tab === 'persona' ? <AgentPersonaSection agent={data} /> : null}

          {data && tab === 'runtime' ? (
            <>
              <AgentImageSection agent={data} />
              <AgentEnvSection agent={data} />
            </>
          ) : null}

          {data && tab === 'tools' ? (
            <>
              <AgentToolsSection agent={data} />
              <AgentMCPSection agent={data} />
            </>
          ) : null}

          {data && tab === 'skills' ? <AgentSkillsSection agent={data} /> : null}

          {data && tab === 'channel' ? (
            <AgentChannelTab agentId={data.id} agentName={data.name} />
          ) : null}

          {data && tab === 'triggers' ? (
            <AgentTriggers agentId={data.id} agentName={data.name} />
          ) : null}
        </div>
      </div>

      <ConfirmModal
        open={pauseOpen}
        title="Pause agent?"
        body={
          <>
            <p style={{ marginTop: 0 }}>
              <b>{data?.name ?? id}</b> stops claiming queued tasks. Events keep
              arriving and route as usual — the tasks wait and drain in order
              when the pause ends. Running tasks finish normally; chat still
              works.
            </p>
            <div role="radiogroup" aria-label="Pause duration" style={{ display: 'grid', gap: 6 }}>
              {PAUSE_PRESETS.map((p) => (
                <label key={p.value} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                  <input
                    type="radio"
                    name="pause-duration"
                    value={p.value}
                    checked={pauseDuration === p.value}
                    onChange={() => setPauseDuration(p.value)}
                  />
                  {p.label}
                </label>
              ))}
            </div>
          </>
        }
        confirmLabel={pause.isPending ? 'Pausing…' : 'Pause'}
        error={pauseError}
        onConfirm={onConfirmPause}
        onCancel={() => {
          setPauseError(null)
          setPauseOpen(false)
        }}
      />

      <ConfirmModal
        open={confirmOpen}
        title="Delete agent?"
        body={
          <>
            <b>{data?.name ?? id}</b> will be permanently removed. Its inbox
            channel — subscriptions and schedules — goes with it.
          </>
        }
        confirmLabel={remove.isPending ? 'Deleting…' : 'Delete'}
        destructive
        error={deleteError}
        onConfirm={onConfirmDelete}
        onCancel={() => {
          setDeleteError(null)
          setConfirmOpen(false)
        }}
      />
    </>
  )
}

function PersonaPanel({
  agentId,
  onConfigure,
}: {
  agentId: string
  onConfigure: () => void
}) {
  // Agent-scoped on purpose: a persona an agent owns has no library permission
  // record, so reading it through /personas/{id} would be denied for
  // non-admins. This endpoint is gated by the agent instead, and reports
  // whether the persona is private to it or a shared template.
  const { data: view, isLoading } = useAgentPersona(agentId)

  if (isLoading) {
    return (
      <Panel title="Persona" className="cropped">
        <p className="label">Loading persona…</p>
      </Panel>
    )
  }

  const persona = view?.persona

  if (!persona) {
    return (
      <Panel title="Persona" className="cropped">
        <p style={{ marginTop: 0, color: 'var(--ink-2)' }}>
          {view?.ref
            ? `The linked persona (${view.ref}) no longer exists.`
            : 'No persona yet — this agent runs without one.'}
        </p>
        <p style={{ marginTop: 12 }}>
          <Button variant="ghost" onClick={onConfigure}>
            Configure persona →
          </Button>
        </p>
      </Panel>
    )
  }

  // 600 chars keeps the preview tight; full text is one click away.
  const preview =
    persona.text.length > 600 ? persona.text.slice(0, 600) + '…' : persona.text

  return (
    <Panel
      title={`Persona · ${persona.name}${view?.owned ? ' · own' : ' · shared template'}`}
      className="cropped"
    >
      {persona.description ? (
        <p style={{ marginTop: 0, marginBottom: 12, color: 'var(--ink-2)' }}>
          {persona.description}
        </p>
      ) : null}
      <Markdown source={preview} />
      <p style={{ marginTop: 12 }}>
        <Button variant="ghost" onClick={onConfigure}>
          Configure persona →
        </Button>
      </p>
    </Panel>
  )
}
