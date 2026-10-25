import { Link } from 'react-router-dom'
import { useRecentEvents } from '../../api/events'
import type { ActivityEntry } from '../../api/events'
import { useAgents } from '../../api/agents'
import { useConnectors } from '../../api/connectors'
import { EntityLink } from '../../components/EntityLink'
import { agentPath, connectorPath, eventPath } from '../../components/entityPaths'
import { Panel } from '../../primitives/Panel'
import { Tag } from '../../primitives/Tag'
import { formatRelative } from '../../utils/time'

function StatusTag({ status }: { status: ActivityEntry['status'] }) {
  if (status === 'matched') return <Tag variant="ok">MATCH</Tag>
  if (status === 'error') return <Tag variant="err">ERR</Tag>
  return <Tag variant="stale">SKIP</Tag>
}

export function RecentActivityStream() {
  const { data, isLoading, error } = useRecentEvents(5)
  // Side lists decide which references are linkable: an entity that was
  // deleted (or is not readable by this caller) renders plain text, not a 404.
  const { data: agentData } = useAgents({ pageSize: 200 })
  const { data: connectorData } = useConnectors({ pageSize: 200 })
  const agentIds = new Set((agentData?.items ?? []).map((a) => a.id))
  const connectorIds = new Set((connectorData?.items ?? []).map((c) => c.id))

  const errorCount = (data ?? []).filter((e) => e.status === 'error').length

  return (
    <Panel
      title="Recent Activity · live"
      right={errorCount > 0 ? <Tag variant="err">{errorCount} error</Tag> : null}
      className="cropped"
    >
      {isLoading ? <div className="label" style={{ padding: 4 }}>Loading…</div> : null}
      {error ? (
        <div className="label" style={{ padding: 4, color: 'var(--signal)' }}>
          Failed to load activity.
        </div>
      ) : null}
      {!isLoading && !error && data ? (
        data.length === 0 ? (
          <div className="label" style={{ padding: 4 }}>
            No recent events.
          </div>
        ) : (
          data.map((e) => (
            <div key={e.id} className="stream-row">
              {/* Stretched link: the whole row opens the event detail. Nested
                  entity links sit above it (z-index) and stop propagation, so
                  clicking a connector or agent still goes to its own page. */}
              <Link
                className="stretched-link"
                to={eventPath(e.id)}
                aria-label={`Open event ${e.id}`}
              />
              <span className="ts num">{formatRelative(e.timestamp)}</span>
              <span className="desc">
                {e.connector ? (
                  <b>
                    <EntityLink
                      id={e.connector}
                      known={connectorIds}
                      label={e.connector}
                      path={connectorPath}
                      stopPropagation
                    />
                  </b>
                ) : null}
                {e.matches && e.matches.length > 0 ? (
                  <>
                    {' '}
                    →{' '}
                    <b>
                      <EntityLink
                        id={e.matches[0].agentId}
                        known={agentIds}
                        label={e.matches[0].agent}
                        path={agentPath}
                        stopPropagation
                      />
                    </b>
                  </>
                ) : null}
              </span>
              <StatusTag status={e.status} />
            </div>
          ))
        )
      ) : null}
    </Panel>
  )
}
