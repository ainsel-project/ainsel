import { useMemo, type ReactNode } from 'react'
import { Link, useParams } from 'react-router-dom'
import { birthChannelPath } from '../../../api/channels'
import { useConversations } from '../../../api/conversations'
import { useEvent } from '../../../api/events'
import {
  useInvocations,
  invocationStatusVariant,
  effectiveRunState,
  taskRunState,
  type InvocationEntry,
} from '../../../api/invocations'
import { EventJourney } from '../../../components/journey/EventJourney'
import { ConnectorLabel } from '../../../components/ConnectorLabel'
import { EntityLink } from '../../../components/EntityLink'
import { agentPath, connectorPath } from '../../../components/entityPaths'
import { useAgents } from '../../../api/agents'
import { useConnectors } from '../../../api/connectors'
import { Titleblock } from '../../../layout/Titleblock'
import { Panel } from '../../../primitives/Panel'
import { ScrollToTop } from '../../../primitives/ScrollToTop'
import { SectionStatus } from '../../../primitives/SectionStatus'
import { Tag } from '../../../primitives/Tag'
import { formatISO } from '../../../utils/time'
import { ConversationTranscript } from './ConversationTranscript'
import { PluginSlot } from '../../../plugins/PluginSlot'

function StatusTag({ status }: { status: 'matched' | 'unmatched' | 'error' }) {
  if (status === 'matched') return <Tag variant="ok">MATCH</Tag>
  if (status === 'error') return <Tag variant="err">ERR</Tag>
  return <Tag variant="stale">SKIP</Tag>
}

function formatDuration(ms: number | undefined): string {
  return ms !== undefined ? `${(ms / 1000).toFixed(1)}s` : '—'
}

// Poll interval for the event detail page while any invocation is still
// running. Conversation messages are reported incrementally per LLM turn and
// invocation records are completed by the agent's ack — without polling, a
// page opened before the run finished would never show them.
const RUNNING_POLL_MS = 5000

function Kpi({
  label,
  children,
  size = 18,
}: {
  label: string
  children: ReactNode
  size?: number
}) {
  return (
    <div className="kpi" style={{ cursor: 'default' }}>
      <div className="hd">
        <span className="label">{label}</span>
      </div>
      <div className="figure" style={{ fontSize: size }}>
        {children}
      </div>
    </div>
  )
}

// Explains why a transcript is empty, using the derived run state of the
// backing agent_tasks row (#195, #422). A queued task means the agent has not
// even started — very different from "the agent ran and produced nothing".
// In per-task Job mode the row stays "pending" while the pod works, so the
// derived state (claim or conversation evidence), not task.status, decides.
function transcriptEmptyState(invocation: InvocationEntry): string {
  const t = invocation.task
  if (!t) return 'No conversation recorded for this invocation.'
  const state = taskRunState(t)
  if (state === 'queued') {
    return t.attempts > 0
      ? `Waiting in queue — attempt ${t.attempts} failed, retrying before the agent starts.`
      : 'Waiting in queue — the agent has not started this task yet.'
  }
  if (state === 'running') {
    return `Processing — the agent is working on this task (attempt ${t.attempts + 1}).`
  }
  return 'No conversation recorded for this invocation.'
}

function InvocationDetail({ invocation }: { invocation: InvocationEntry }) {
  const { data: agentData } = useAgents({ pageSize: 200 })
  const agentIds = useMemo(
    () => new Set((agentData?.items ?? []).map((a) => a.id)),
    [agentData],
  )
  const conversations = useConversations(
    { invocation: invocation.id },
    // Keep polling the transcript while the invocation runs so the
    // incrementally reported messages appear without a manual refresh.
    { refetchInterval: invocation.status === 'running' ? RUNNING_POLL_MS : false },
  )
  const messages = useMemo(() => conversations.data?.messages ?? [], [conversations.data])
  const total = conversations.data?.total
  const tokens = useMemo(
    () =>
      messages.reduce(
        (sum, m) =>
          m.role === 'assistant' ? sum + (m.inputTokens ?? 0) + (m.outputTokens ?? 0) : sum,
        0,
      ),
    [messages],
  )

  const runState = effectiveRunState(invocation)
  // Compose both suffixes: a retried task that has waited past the start
  // deadline carries the more actionable signal, so never hide "stuck"
  // behind "attempt N" (or vice versa).
  const queuedDetail =
    runState === 'queued' && invocation.task
      ? `${invocation.task.attempts > 0 ? ` · attempt ${invocation.task.attempts}` : ''}${
          invocation.task.stuck ? ' · stuck' : ''
        }`
      : ''

  return (
    <Panel
      title={`Invocation ${invocation.id} · ${invocation.agentName ?? invocation.agent}`}
      className="cropped"
    >
      <div className="kpi-row" style={{ gridTemplateColumns: 'repeat(5, 1fr)' }}>
        <Kpi label="Agent">
          <EntityLink
            id={invocation.agentId}
            known={agentIds}
            label={invocation.agentName ?? invocation.agent}
            path={agentPath}
          />
        </Kpi>
        <Kpi label="Trigger">{invocation.triggerName ?? invocation.trigger ?? '—'}</Kpi>
        <Kpi label="Status">
          {/* The derived run state: a task with no worker yet reads queued
              (never running), a live per-task Job run reads running (never
              queued), and a row with nothing behind it reads unknown. */}
          <Tag variant={invocationStatusVariant(runState)}>{runState}{queuedDetail}</Tag>
        </Kpi>
        <Kpi label="Duration" size={28}>
          {formatDuration(invocation.durationMs)}
        </Kpi>
        <Kpi label="Tokens" size={28}>
          {tokens > 0 ? tokens : '—'}
        </Kpi>
      </div>
      <div style={{ marginTop: 16 }}>
        {conversations.isLoading ? (
          <SectionStatus state="loading" />
        ) : conversations.error ? (
          <SectionStatus state="error" />
        ) : (
          <ConversationTranscript
            messages={messages}
            total={total}
            emptyState={transcriptEmptyState(invocation)}
          />
        )}
      </div>

      <div style={{ marginTop: 16 }}>
        <PluginSlot
          slot="invocation.panel"
          props={{ invocationId: invocation.id, agentName: invocation.agent }}
        />
      </div>
    </Panel>
  )
}

export function EventView() {
  const { id } = useParams<{ id: string }>()
  const { data, isLoading, error } = useEvent(id ?? '')
  // Side lists decide which references are linkable: an id whose entity was
  // deleted (or is not readable by this caller) renders plain text, not a 404.
  const { data: agentData } = useAgents({ pageSize: 200 })
  const { data: connectorData } = useConnectors({ pageSize: 200 })
  const agentIds = useMemo(
    () => new Set((agentData?.items ?? []).map((a) => a.id)),
    [agentData],
  )
  const connectorIds = useMemo(
    () => new Set((connectorData?.items ?? []).map((c) => c.id)),
    [connectorData],
  )
  // Any invocation still running means the event detail is a live view:
  // poll so status flips, new invocations, and the transcript show up.
  const invocations = useInvocations(
    { event: id, pageSize: 50 },
    {
      refetchInterval: (query) =>
        (query.state.data?.items ?? []).some((inv) => inv.status === 'running')
          ? RUNNING_POLL_MS
          : false,
    },
  )

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Operations / <Link to="/observability">Observability</Link> /{' '}
            <Link to="/observability/events">Events</Link> / <b>Detail</b>
          </>
        }
        title={
          <>
            Event <em>Detail</em>
          </>
        }
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 24 }}>
        {isLoading ? (
          <Panel className="cropped">
            <SectionStatus state="loading" />
          </Panel>
        ) : error ? (
          <Panel className="cropped">
            <SectionStatus state="error" />
          </Panel>
        ) : data ? (
          <>
            <div className="kpi-row" style={{ gridTemplateColumns: 'repeat(4, 1fr)' }}>
              <div className="kpi">
                <div className="hd">
                  <span className="label">When</span>
                </div>
                <div className="figure" style={{ fontSize: 18 }}>
                  {formatISO(data.timestamp)}
                </div>
              </div>
              <div className="kpi">
                <div className="hd">
                  <span className="label">Connector</span>
                </div>
                <div className="figure" style={{ fontSize: 18 }}>
                  {data.connector ? (
                    <EntityLink
                      id={data.connector}
                      known={connectorIds}
                      label={<ConnectorLabel id={data.connector} />}
                      path={connectorPath}
                    />
                  ) : (
                    '—'
                  )}
                </div>
              </div>
              <div className="kpi">
                <div className="hd">
                  <span className="label">Status</span>
                </div>
                <div className="figure">
                  <StatusTag status={data.status} />
                </div>
              </div>
              <div className="kpi">
                <div className="hd">
                  <span className="label">Event ID</span>
                </div>
                <div className="figure" style={{ fontSize: 14 }}>
                  {data.id}
                </div>
              </div>
            </div>

            {data.matches && data.matches.length > 0 ? (
              <Panel title="Matches" className="cropped">
                <div className="info-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
                  <div>
                    <div className="k">Trigger</div>
                    <div className="v">Agent</div>
                  </div>
                </div>
                {data.matches.map((m, i) => (
                  <div
                    key={i}
                    className="info-grid"
                    style={{
                      gridTemplateColumns: '1fr 1fr',
                      borderTop: '1px solid var(--border)',
                      paddingTop: 8,
                      marginTop: 8,
                    }}
                  >
                    <div>
                      <div className="v">{m.trigger}</div>
                    </div>
                    <div>
                      <div className="v">
                        <EntityLink
                          id={m.agentId}
                          known={agentIds}
                          label={m.agent}
                          path={agentPath}
                        />
                      </div>
                    </div>
                  </div>
                ))}
              </Panel>
            ) : null}

            <Panel
              title="Channel journey"
              className="cropped"
              right={(() => {
                const href = birthChannelPath(data)
                return href ? (
                  <Link to={href} className="label">
                    Trace channel →
                  </Link>
                ) : undefined
              })()}
            >
              <EventJourney
                connector={data.connector}
                channelId={data.channelId}
                timestamp={data.timestamp}
                matches={data.matches ?? []}
              />
            </Panel>

            {invocations.isLoading ? (
              <Panel className="cropped">
                <SectionStatus state="loading" />
              </Panel>
            ) : invocations.error ? (
              <Panel className="cropped">
                <SectionStatus state="error" />
              </Panel>
            ) : (invocations.data?.items.length ?? 0) === 0 ? (
              <Panel title="Invocations" className="cropped">
                <div className="label" style={{ padding: '4px 0' }}>
                  No invocations recorded for this event.
                </div>
              </Panel>
            ) : (
              <>
                <div className="scroll-cap">
                  <div style={{ display: 'grid', gap: 24 }}>
                    {invocations.data?.items.map((inv) => (
                      <InvocationDetail key={inv.id} invocation={inv} />
                    ))}
                  </div>
                </div>
                {(invocations.data?.total ?? 0) > (invocations.data?.items.length ?? 0) ? (
                  <div className="label" style={{ padding: '6px 2px', color: 'var(--ink-3)' }}>
                    Showing {invocations.data?.items.length} of {invocations.data?.total}{' '}
                    invocations.
                  </div>
                ) : null}
              </>
            )}

            {data.payload != null ? (
              <Panel title="Payload" className="cropped">
                <div className="scroll-cap">
                  <pre className="code">
                    <code>{JSON.stringify(data.payload, null, 2)}</code>
                  </pre>
                </div>
              </Panel>
            ) : null}
          </>
        ) : null}
      </div>
      <ScrollToTop />
    </>
  )
}
