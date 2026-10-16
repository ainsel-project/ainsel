// Run-state derivation on the frontend (#422). The hub sends the derived
// runState; these tests pin both the pass-through and the fallback mapping for
// responses from older hubs, so no view re-invents the mapping.
import { describe, expect, it } from 'vitest'
import { effectiveRunState, invocationStatusVariant, taskRunState } from './invocations'

describe('taskRunState', () => {
  it('trusts the derived state from the hub', () => {
    expect(
      taskRunState({ status: 'pending', attempts: 0, startedAt: 'x', runState: 'running' }),
    ).toBe('running')
  })

  it('reads a per-task Job run (pending with start evidence) as running, not queued', () => {
    expect(taskRunState({ status: 'pending', attempts: 0, startedAt: 'x' })).toBe('running')
  })

  it('reads a task with no start evidence as queued', () => {
    expect(taskRunState({ status: 'pending', attempts: 0 })).toBe('queued')
  })

  it('maps terminal task statuses', () => {
    expect(taskRunState({ status: 'completed', attempts: 1 })).toBe('success')
    expect(taskRunState({ status: 'failed', attempts: 2 })).toBe('failure')
  })
})

describe('effectiveRunState', () => {
  it('prefers the task-derived state over the invocation record', () => {
    expect(
      effectiveRunState({
        status: 'running',
        task: { status: 'pending', attempts: 0, runState: 'queued' },
      }),
    ).toBe('queued')
  })

  it('keeps a terminal invocation status when no task row backs it', () => {
    expect(effectiveRunState({ status: 'timeout' })).toBe('timeout')
  })

  it('reports unknown — not running, not queued — when nothing backs the row', () => {
    expect(effectiveRunState({ status: 'running' })).toBe('unknown')
  })
})

describe('invocationStatusVariant', () => {
  it('renders queued distinctly from running', () => {
    expect(invocationStatusVariant('queued')).toBe('stale')
    expect(invocationStatusVariant('running')).toBe('warn')
    expect(invocationStatusVariant('unknown')).toBe('default')
  })
})
