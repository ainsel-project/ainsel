import { useAgents } from '../../api/agents'
import { useConnectors } from '../../api/connectors'
import { useObservabilitySummary } from '../../api/observability'
import { useTriggers } from '../../api/triggers'
import { Titleblock } from '../../layout/Titleblock'
import { ConnectorRegister } from './ConnectorRegister'
import { KpiRow } from './KpiRow'
import { RecentActivityStream } from './RecentActivityStream'
import { ThroughputChart } from './ThroughputChart'
import './Dashboard.css'

export function Dashboard() {
  const agents = useAgents({ pageSize: 1 })
  const connectors = useConnectors({ pageSize: 1 })
  const triggers = useTriggers({ pageSize: 1 })
  // The summary endpoint is range-less by design for MCP consumers: without a
  // `range` it answers with everything the hub still retains (up to 7d of
  // task logs), not the last hour. The "Errors · 1h" plate claims a window, so
  // it has to request one (issue #217).
  const summary = useObservabilitySummary('1h')

  const errorCount = summary.data?.routingErrors ?? 0

  // No plate carries a sparkline (issue #275). Agents, connectors and triggers
  // are resource counts — the hub keeps no per-bucket history for them — and
  // the errors plate's figure is the summary's routingErrors, which on a
  // Prometheus-pinned hub is a task-log count, not the `routing_errors`
  // timeseries. A plate may show less, but never a trend that was not measured.
  const plates = [
    {
      key: 'agents',
      label: 'Agents',
      code: 'AG·01',
      to: '/agents',
      count: agents.data?.total,
      sub: 'Active AI agents',
    },
    {
      key: 'connectors',
      label: 'Connectors',
      code: 'CN·02',
      to: '/connectors',
      count: connectors.data?.total,
      sub: 'Platform connectors',
    },
    {
      key: 'triggers',
      label: 'Triggers',
      code: 'TR·03',
      // Trigger configuration lives per agent (/agents/:id/triggers); the
      // agents list is the nearest real destination. There is no /triggers
      // route — the catch-all used to bounce this click back to /dashboard.
      to: '/agents',
      count: triggers.data?.total,
      sub: 'Routing rules',
    },
    {
      key: 'errors',
      label: 'Errors · 1h',
      code: 'ER·04',
      to: '/error-log',
      count: summary.data?.routingErrors,
      sub: errorCount > 0 ? 'Recent errors — investigate' : 'No recent errors',
      alert: errorCount > 0,
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Operations / <b>Dashboard</b>
          </>
        }
        title={
          <>
            Operations <em>Console</em>
          </>
        }
      />
      <div className="page">
        <KpiRow plates={plates} />
        <div className="dashboard-grid">
          <ConnectorRegister />
          <div className="dashboard-grid-right">
            <ThroughputChart />
            <RecentActivityStream />
          </div>
        </div>
      </div>
    </>
  )
}
