import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  isIdle,
  useDeleteAgent,
  usePauseAgent,
  useResumeAgent,
  useUpdateAgent,
  type AgentEnvVar,
  type PauseAgentRequest,
} from '../../../api/agents'
import { ApiError } from '../../../api/client'
import { useAgentImage, useAgentImages, type EnvVar } from '../../../api/agentImages'
import { useAgentPersona } from '../../../api/personas'
import { useChannels } from '../../../api/channels'
import { useCronTriggers } from '../../../api/cronTriggers'
import { useInvocations, effectiveRunState, type RunState } from '../../../api/invocations'
import { useTriggers } from '../../../api/triggers'
import { EntityLink } from '../../../components/EntityLink'
import { agentImagePath } from '../../../components/entityPaths'
import { ManagedBadge } from '../../../components/ManagedBadge'
import { Button } from '../../../primitives/Button'
import { ConfirmModal } from '../../../primitives/ConfirmModal'
import { Dot } from '../../../primitives/Dot'
import { Field } from '../../../primitives/Field'
import { Input } from '../../../primitives/Input'
import { Notice } from '../../../primitives/Notice'
import { Panel } from '../../../primitives/Panel'
import { Tag } from '../../../primitives/Tag'
import { Textarea } from '../../../primitives/Textarea'
import { isManaged } from '../../../utils/managed'
import { SINCE_SENTINEL, startOfToday } from '../../../utils/time'
import { SignInBanner } from './SignInBanner'
import { mergeEnv } from './envRows'
import { SectionPage } from './SectionPage'
import { sectionPath, type SectionId } from './sections'
import { useWorkspaceAgent } from './workspaceAgent'

// Duration presets for the pause dialog. 'indefinite' sends an empty body:
// the pause then lasts until someone resumes explicitly.
const PAUSE_PRESETS: { value: string; label: string }[] = [
  { value: '30m', label: '30 minutes' },
  { value: '8h', label: '8 hours' },
  { value: '24h', label: '24 hours' },
  { value: '168h', label: '7 days' },
  { value: 'indefinite', label: 'Indefinite' },
]

const DEFAULT_TURN_TIMEOUT_MIN = 30

/** Overview section: the agent's identity, today's runs, its key facts and lifecycle actions. */
export function OverviewSection() {
  const agent = useWorkspaceAgent()
  const { id } = agent
  const navigate = useNavigate()
  const goTo = (section: SectionId) => navigate(sectionPath(id, section))
  const managed = isManaged(agent.managedBy)
  const paused = agent.paused === true

  const [confirmOpen, setConfirmOpen] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [pauseOpen, setPauseOpen] = useState(false)
  const [pauseDuration, setPauseDuration] = useState('8h')
  const [pauseError, setPauseError] = useState<string | null>(null)
  const [draft, setDraft] = useState<{ name: string; description: string } | null>(null)
  const [editError, setEditError] = useState<string | null>(null)

  const remove = useDeleteAgent()
  const pause = usePauseAgent()
  const resume = useResumeAgent()
  const update = useUpdateAgent()

  // Side list decides whether the image reference is linkable: an image that
  // was deleted renders as plain text, not a link to a 404.
  const { data: imageData } = useAgentImages({ pageSize: 200 })
  const imageIds = useMemo(() => new Set((imageData?.items ?? []).map((i) => i.id)), [imageData])
  const imageQuery = useAgentImage(agent.imageRef?.name)
  const image = imageQuery.data
  const imageLabel = agent.imageRef?.displayName ?? agent.imageRef?.name

  // Fixed at mount: a per-render value would change the query key every render.
  const [since] = useState(startOfToday)
  const { data: invocations } = useInvocations({ agent: id, since, pageSize: 200 })
  const { data: triggerData } = useTriggers({ pageSize: 200 })
  const { data: cronData } = useCronTriggers({ pageSize: 200 })
  const { data: channelData } = useChannels({ pageSize: 500 })
  const { data: personaView } = useAgentPersona(id)

  const items = useMemo(() => invocations?.items ?? [], [invocations])
  const runsToday = invocations?.total ?? items.length
  // Count via the derived run state, not the raw invocation status: the
  // numbers must agree with the Activity rows each cell links to (see
  // docs/observability.md "Queued, running, and unknown"). `unknown` items
  // count into neither Running nor Queued. Every cell maps 1:1 to an
  // Activity `outcome` value, so the linked view shows exactly that state.
  const runStates = useMemo(() => items.map((i) => effectiveRunState(i)), [items])
  const countState = (s: RunState) => runStates.filter((r) => r === s).length
  const failuresToday = countState('failure')
  const timeoutsToday = countState('timeout')
  const runningNow = countState('running')
  const queuedNow = countState('queued')
  // `since=today` keeps the destination on the same day the numbers count;
  // the counts themselves are directional (invocations vs. events), not a
  // guarantee the linked totals match.
  const activityLink = (outcome?: RunState) =>
    `/activity?agent=${encodeURIComponent(id)}&since=${SINCE_SENTINEL}` +
    (outcome ? `&outcome=${outcome}` : '')

  const triggerCount =
    (triggerData?.items ?? []).filter((t) => t.agentRef === id).length +
    (cronData?.items ?? []).filter((t) => t.agentRef === id).length

  const inboxChannel = (channelData?.items ?? []).find(
    (c) => c.kind === 'agent' && c.entityRef === id,
  )

  // The image query is disabled without an image reference; only a query that
  // will run counts as loading.
  const imageLoading = agent.imageRef?.name !== undefined && imageQuery.isPending
  const timeout = timeoutFact(agent.env ?? [], image?.env ?? [], imageLoading)

  // The operator's run account says whether work is in flight. An agent with
  // nothing queued or running is idle by design: every task runs as its own Job.
  const idle = isIdle(agent.status)
  const statusLabel = idle ? 'Idle' : agent.status?.ready ? 'Ready' : 'Pending'
  const statusDot = idle ? 'stale' : agent.status?.ready ? 'ok' : 'warn'

  const onConfirmDelete = async () => {
    setDeleteError(null)
    try {
      await remove.mutateAsync(id)
      setConfirmOpen(false)
      navigate('/agents', { replace: true })
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete agent.')
    }
  }

  const onConfirmPause = async () => {
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
    try {
      await resume.mutateAsync(id)
    } catch {
      // The refetch leaves the agent paused; the button stays available.
    }
  }

  const onSaveEdit = async () => {
    if (!draft) return
    setEditError(null)
    try {
      await update.mutateAsync({
        id,
        body: { name: draft.name.trim(), description: draft.description },
      })
      setDraft(null)
    } catch (err) {
      setEditError(err instanceof ApiError ? err.message : 'Failed to save agent.')
    }
  }

  return (
    <>
      <SectionPage
        title={
          <>
            {agent.name}
            {!managed ? (
              <Button
                variant="ghost"
                size="sm"
                aria-label="Edit name and description"
                onClick={() => setDraft({ name: agent.name, description: agent.description ?? '' })}
                style={{ marginLeft: 8 }}
              >
                <PencilIcon />
              </Button>
            ) : null}
            {managed ? (
              <span style={{ marginLeft: 10 }}>
                <ManagedBadge managedBy={agent.managedBy} />
              </span>
            ) : null}
            {paused ? (
              <span style={{ marginLeft: 10 }}>
                <Tag variant="warn">
                  Paused
                  {agent.pausedUntil
                    ? ` · resumes ${new Date(agent.pausedUntil).toLocaleString()}`
                    : ' · until resumed'}
                </Tag>
              </span>
            ) : null}
            {/* Outside a working-hours window the agent looks idle and is not: its
                work is queued and goes when the window opens. Say which it is, or the
                schedule reads as a broken agent. Pause is the louder state, so it is
                reported on its own line above. */}
            {!paused && agent.workingHours && !agent.hoursOpen ? (
              <span style={{ marginLeft: 10 }}>
                <Tag variant="stale">
                  Outside hours
                  {agent.hoursNextOpenAt
                    ? ` · opens ${new Date(agent.hoursNextOpenAt).toLocaleString()}`
                    : ''}
                </Tag>
              </span>
            ) : null}
          </>
        }
        actions={
          managed ? null : (
            <>
              {paused ? (
                <Button variant="primary" onClick={onResume} disabled={resume.isPending}>
                  {resume.isPending ? 'Resuming…' : 'Resume'}
                </Button>
              ) : (
                <Button onClick={() => setPauseOpen(true)}>Pause</Button>
              )}
              <Button onClick={() => goTo('chat')}>Chat</Button>
              <Button variant="danger" onClick={() => setConfirmOpen(true)}>
                Delete
              </Button>
            </>
          )
        }
      >
        <div style={{ display: 'grid', gap: 24 }}>
          <SignInBanner agent={agent} />
          {draft ? (
            <Panel title="Name and description" className="cropped">
              <div style={{ display: 'grid', gap: 12 }}>
                <Field label="Name" htmlFor="overview-name">
                  <Input
                    id="overview-name"
                    value={draft.name}
                    onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                  />
                </Field>
                <Field label="Description" htmlFor="overview-description">
                  <Textarea
                    id="overview-description"
                    rows={3}
                    value={draft.description}
                    onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                  />
                </Field>
                {editError ? <Notice variant="err">{editError}</Notice> : null}
                <div style={{ display: 'flex', gap: 8 }}>
                  <Button
                    variant="primary"
                    onClick={onSaveEdit}
                    disabled={update.isPending || draft.name.trim() === ''}
                  >
                    {update.isPending ? 'Saving…' : 'Save'}
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => {
                      setEditError(null)
                      setDraft(null)
                    }}
                  >
                    Cancel
                  </Button>
                </div>
              </div>
            </Panel>
          ) : (
            <div>
              <div
                data-testid="overview-meta"
                style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}
              >
                <Dot state={statusDot} />
                <span>{statusLabel}</span>
                <span style={{ color: 'var(--ink-3)' }}>·</span>
                <span>{agent.llm?.model ?? '—'}</span>
                <span style={{ color: 'var(--ink-3)' }}>·</span>
                {agent.imageRef ? (
                  <EntityLink
                    id={agent.imageRef.id}
                    known={imageIds}
                    label={imageLabel}
                    path={agentImagePath}
                  />
                ) : (
                  <span>—</span>
                )}
              </div>
              {agent.description ? (
                <p style={{ marginTop: 8, color: 'var(--ink-2)' }}>{agent.description}</p>
              ) : null}
            </div>
          )}

          <div className="info-grid cols-5">
            <div>
              <div className="k">Runs today</div>
              <div className="v">
                <Link to={activityLink()}>{runsToday}</Link>
              </div>
            </div>
            <div>
              <div className="k">Failures today</div>
              <div className="v">
                <Link to={activityLink('failure')}>{failuresToday}</Link>
              </div>
            </div>
            <div>
              <div className="k">Timeouts today</div>
              <div className="v">
                <Link to={activityLink('timeout')}>{timeoutsToday}</Link>
              </div>
            </div>
            <div>
              <div className="k">Running now</div>
              <div className="v">
                <Link to={activityLink('running')}>{runningNow}</Link>
              </div>
            </div>
            <div>
              <div className="k">Queued now</div>
              <div className="v">
                <Link to={activityLink('queued')}>{queuedNow}</Link>
              </div>
            </div>
          </div>

          <Panel title="Key facts" className="cropped">
            <dl className="info-grid cols-7" style={{ margin: 0 }}>
              <div>
                <dt className="k">Persona</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {personaView?.persona ? (
                    <Link to={sectionPath(id, 'persona')}>{personaView.persona.name}</Link>
                  ) : personaView?.ref ? (
                    'Missing'
                  ) : (
                    'None'
                  )}
                </dd>
              </div>
              <div>
                <dt className="k">Image</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {agent.imageRef ? (
                    <EntityLink
                      id={agent.imageRef.id}
                      known={imageIds}
                      label={imageLabel}
                      path={agentImagePath}
                    />
                  ) : (
                    '—'
                  )}
                </dd>
              </div>
              <div>
                <dt className="k">Timeout</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {timeout}
                </dd>
              </div>
              <div>
                <dt className="k">Triggers</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {triggerCount}
                </dd>
              </div>
              <div>
                <dt className="k">Channel</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {inboxChannel ? (
                    <Link to={sectionPath(id, 'channel')}>{inboxChannel.name || agent.name}</Link>
                  ) : (
                    '—'
                  )}
                </dd>
              </div>
              <div>
                <dt className="k">Model</dt>
                <dd className="v" style={{ margin: 0 }}>
                  {agent.llm?.model ? (
                    <Link to={sectionPath(id, 'runtime')}>
                      <span className="num">{agent.llm.model}</span>
                    </Link>
                  ) : (
                    '—'
                  )}
                </dd>
              </div>
              <div>
                <dt className="k">Image Input</dt>
                <dd className="v" style={{ margin: 0 }}>
                  <Link to={sectionPath(id, 'runtime')}>
                    {agent.llm?.vision === true ? 'Images are sent' : 'Text only'}
                  </Link>
                </dd>
              </div>
            </dl>
          </Panel>
        </div>
      </SectionPage>

      <ConfirmModal
        open={pauseOpen}
        title="Pause agent?"
        body={
          <>
            <p style={{ marginTop: 0 }}>
              <b>{agent.name}</b> stops claiming queued tasks. Events keep arriving and route as
              usual — the tasks wait and drain in order when the pause ends. Running tasks finish
              normally; chat still works.
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
            <b>{agent.name}</b> will be permanently removed. Its inbox channel — subscriptions and
            schedules — goes with it.
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

const TIMEOUT_VAR = 'TURN_TIMEOUT_MS'

/** A blank or non-numeric TURN_TIMEOUT_MS is absent, so it falls through. */
function isUsableTimeout(e: { name: string; value: string }): boolean {
  return e.name !== TIMEOUT_VAR || (e.value.trim() !== '' && Number.isFinite(Number(e.value)))
}

/**
 * The turn timeout as "N min · source", from the merged env (the agent's own
 * entry wins over the image's). Falls back to the 30 minute default, or "…"
 * while the image that may set it is still loading.
 */
function timeoutFact(agentEnv: AgentEnvVar[], imageEnv: EnvVar[], imageLoading: boolean): string {
  const row = mergeEnv(agentEnv.filter(isUsableTimeout), imageEnv.filter(isUsableTimeout)).find(
    (r) => r.name === TIMEOUT_VAR,
  )
  if (!row) return imageLoading ? '…' : `${DEFAULT_TURN_TIMEOUT_MIN} min · default`
  const fromImage = row.kind === 'image'
  const ms = Number(fromImage ? row.imageValue : row.agentValue)
  return `${ms / 60_000} min · ${fromImage ? 'image' : 'agent'}`
}

function PencilIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M11.5 2.5l2 2L5 13l-2.5.5L3 11z"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  )
}
