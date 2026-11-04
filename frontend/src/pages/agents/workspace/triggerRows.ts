import type { CronTriggerSummary } from '../../../api/cronTriggers'
import type { InvocationEntry } from '../../../api/invocations'
import type { TriggerFilter, TriggerSummary } from '../../../api/triggers'

export type TriggerRow = {
  id: string
  name: string
  type: 'Subscription' | 'Schedule'
  source: string
  filter: string
  fired24h: number
  failed24h: number
  enabled: boolean
  /** Why the trigger is invalid, when a reference is broken. */
  invalidReason?: string
}

const DAY_MS = 24 * 3_600_000

function invalidReason(reasons: (string | false)[]): string | undefined {
  const broken = reasons.filter((r): r is string => r !== false)
  return broken.length ? broken.join('; ') : undefined
}

function formatFilter(f: TriggerFilter): string {
  const value = f.values?.length ? f.values.join(', ') : f.value
  return `${f.field} ${f.op} ${value}`
}

/**
 * One row per subscription and schedule. Subscriptions have no enabled flag in
 * the API, so they always read as enabled. Run counts come from invocations
 * in the last 24h, matched by trigger name (falling back to the trigger id);
 * timeouts count as failures alongside failures.
 */
export function toTriggerRows(
  triggers: TriggerSummary[],
  crons: CronTriggerSummary[],
  invocations: InvocationEntry[],
  connectorNames: ReadonlyMap<string, string> = new Map(),
): TriggerRow[] {
  const cutoff = Date.now() - DAY_MS
  const recent = invocations.filter((inv) => Date.parse(inv.timestamp) >= cutoff)

  const counts = (id: string) => {
    let fired24h = 0
    let failed24h = 0
    for (const inv of recent) {
      if ((inv.triggerName ?? inv.trigger) !== id) continue
      fired24h++
      if (inv.status === 'failure' || inv.status === 'timeout') failed24h++
    }
    return { fired24h, failed24h }
  }

  const subscriptions: TriggerRow[] = triggers.map((t) => ({
    id: t.id,
    name: t.name,
    type: 'Subscription',
    source: t.connectorRef ? connectorNames.get(t.connectorRef) ?? t.connectorRef : '—',
    filter: t.filters?.length ? t.filters.map(formatFilter).join(' · ') : '—',
    ...counts(t.id),
    enabled: true,
    invalidReason: invalidReason([
      t.status?.agentValid === false && 'agent reference is invalid',
      t.status?.connectorValid === false && 'connector reference is invalid',
    ]),
  }))

  const schedules: TriggerRow[] = crons.map((c) => ({
    id: c.id,
    name: c.name,
    type: 'Schedule',
    source: 'Schedule',
    filter: c.schedule,
    ...counts(c.id),
    enabled: c.enabled,
    invalidReason: invalidReason([
      c.status?.agentValid === false && 'agent reference is invalid',
      c.status?.scheduleValid === false && 'schedule is invalid',
    ]),
  }))

  return [...subscriptions, ...schedules]
}
