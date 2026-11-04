import { describe, expect, it } from 'vitest'
import type { CronTriggerSummary } from '../../../api/cronTriggers'
import type { InvocationEntry } from '../../../api/invocations'
import type { TriggerSummary } from '../../../api/triggers'
import { toTriggerRows } from './triggerRows'

const hoursAgo = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString()

function run(
  id: string,
  status: InvocationEntry['status'],
  timestamp: string,
  extra: Partial<InvocationEntry> = {},
): InvocationEntry {
  return {
    id: `inv-${Math.random()}`,
    agent: 'a1',
    timestamp,
    status,
    triggerName: id,
    ...extra,
  }
}

describe('toTriggerRows', () => {
  it('counts runs inside the last 24h and ignores older ones', () => {
    const triggers: TriggerSummary[] = [{ id: 't-1', name: 'on-pr-open', agentRef: 'a1' }]
    const invocations = [
      run('t-1', 'success', hoursAgo(1)),
      run('t-1', 'failure', hoursAgo(2)),
      run('t-1', 'success', hoursAgo(23)),
      run('t-1', 'failure', hoursAgo(30)),
    ]

    const [row] = toTriggerRows(triggers, [], invocations)

    expect(row).toMatchObject({ id: 't-1', type: 'Subscription', fired24h: 3, failed24h: 1 })
  })

  it('counts timeouts as failed and falls back to trigger when triggerName is absent', () => {
    const triggers: TriggerSummary[] = [{ id: 't-2', name: 'on-issue', agentRef: 'a1' }]
    const invocations = [
      run('t-2', 'timeout', hoursAgo(1)),
      { ...run('x', 'failure', hoursAgo(1)), triggerName: undefined, trigger: 't-2' },
    ]

    const [row] = toTriggerRows(triggers, [], invocations)

    expect(row).toMatchObject({ fired24h: 2, failed24h: 2 })
  })

  it('resolves subscription source to the connector name and filter from its filters', () => {
    const triggers: TriggerSummary[] = [
      {
        id: 't-3',
        name: 'on-pr-open',
        agentRef: 'a1',
        connectorRef: 'c-1',
        filters: [{ field: 'event', op: 'eq', value: 'pull_request' }],
      },
    ]

    const [row] = toTriggerRows(triggers, [], [], new Map([['c-1', 'forgejo-main']]))

    expect(row).toMatchObject({
      type: 'Subscription',
      source: 'forgejo-main',
      filter: 'event eq pull_request',
      enabled: true,
    })
  })

  it('explains which reference makes a trigger invalid', () => {
    const triggers: TriggerSummary[] = [
      {
        id: 't-4',
        name: 'broken',
        agentRef: 'a1',
        status: { agentValid: true, connectorValid: false },
      },
    ]
    const crons: CronTriggerSummary[] = [
      {
        id: 'cr-2',
        name: 'stale',
        agentRef: 'a1',
        schedule: '0 * * * *',
        prompt: 'x',
        enabled: true,
        status: { agentValid: false, scheduleValid: true, ready: false },
      },
    ]

    const rows = toTriggerRows(triggers, crons, [])

    expect(rows.find((r) => r.id === 't-4')?.invalidReason).toBe('connector reference is invalid')
    expect(rows.find((r) => r.id === 'cr-2')?.invalidReason).toBe('agent reference is invalid')
  })

  it('shows schedules with source Schedule, the cron expression as filter, and their enabled flag', () => {
    const crons: CronTriggerSummary[] = [
      {
        id: 'cr-1',
        name: 'nightly',
        agentRef: 'a1',
        schedule: '0 2 * * *',
        prompt: 'review',
        enabled: false,
      },
    ]

    const rows = toTriggerRows([], crons, [])

    expect(rows).toEqual([
      expect.objectContaining({
        id: 'cr-1',
        type: 'Schedule',
        source: 'Schedule',
        filter: '0 2 * * *',
        enabled: false,
        fired24h: 0,
        failed24h: 0,
      }),
    ])
  })
})
