// Run-state derivation on the frontend (#422). The hub sends the derived
// runState; these tests pin both the pass-through and the fallback mapping for
// responses from older hubs, so no view re-invents the mapping.
import { describe, expect, it } from 'vitest'
import {
  type InvocationTaskState,
  effectiveRunState,
  invocationStatusVariant,
  runStateLabel,
  taskRunState,
  taskTimedOut,
} from './invocations'

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
    // Never picked up before the hub's pickup deadline: a timeout, not a failure.
    expect(taskRunState({ status: 'expired', attempts: 0 })).toBe('timeout')
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

// #392: a task whose first attempt died on the runner's turn timeout and whose
// retry then completed is a success — with the provenance visible. The state stays
// `success`; the marker is the derived `timedOut` flag.
describe('taskTimedOut', () => {
  it('marks a success that retained a turn-timeout nack', () => {
    const task: InvocationTaskState = {
      status: 'completed' as const,
      attempts: 2,
      lastError: 'Turn timed out after 21600000ms',
      runState: 'success' as const,
      timedOut: true,
    }
    expect(taskTimedOut(task)).toBe(true)
    // The state itself is unchanged: success, not timeout.
    expect(effectiveRunState({ status: 'success', task })).toBe('success')
    expect(taskRunState(task)).toBe('success')
  })

  it('reads the state from the hub, so it can never badge a non-success', () => {
    const failed: InvocationTaskState = {
      status: 'failed',
      attempts: 2,
      runState: 'failure',
      timedOut: true,
    }
    const running: InvocationTaskState = {
      status: 'pending',
      attempts: 1,
      runState: 'running',
      timedOut: true,
    }
    expect(taskTimedOut(failed)).toBe(false)
    expect(taskTimedOut(running)).toBe(false)
  })

  it('reads an activity match (taskStatus, no status field)', () => {
    expect(taskTimedOut({ runState: 'success', taskStatus: 'completed', timedOut: true })).toBe(
      true,
    )
    expect(taskTimedOut({ taskStatus: 'completed', timedOut: true })).toBe(true)
  })

  it('is false without the flag and without a run state at all', () => {
    const plain: InvocationTaskState = { status: 'completed', attempts: 1, runState: 'success' }
    expect(taskTimedOut(plain)).toBe(false)
    expect(taskTimedOut(undefined)).toBe(false)
  })
})

describe('runStateLabel', () => {
  it('names the marker instead of forking the state vocabulary', () => {
    expect(runStateLabel('success', true)).toBe('success · timed out')
    expect(runStateLabel('success', false)).toBe('success')
  })
})

describe('invocationStatusVariant', () => {
  it('renders queued distinctly from running', () => {
    expect(invocationStatusVariant('queued')).toBe('stale')
    expect(invocationStatusVariant('running')).toBe('warn')
    expect(invocationStatusVariant('unknown')).toBe('default')
  })
})
