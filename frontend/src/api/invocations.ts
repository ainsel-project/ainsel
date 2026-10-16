import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { request } from './client'
import type { Paginated } from './types'

export type InvocationStatus = 'queued' | 'running' | 'success' | 'failure' | 'timeout'

// Derived run state: what a view should render for a run. The hub computes it
// from the task row, the start evidence and the invocation record, so every
// view agrees on the same value. `unknown` means nothing backs the run — a
// pruned record or a task that was never enqueued — and must not be rendered
// as running or queued.
export type RunState = 'queued' | 'running' | 'success' | 'failure' | 'timeout' | 'unknown'

// Presentation variant for an invocation status, keyed off the backend contract
// (queued / running / success / failure / timeout). Single source of truth
// shared by the Routing and Event Detail pages so their status styling cannot
// drift apart.
export type InvocationStatusVariant = 'ok' | 'err' | 'warn' | 'stale' | 'default'

export function invocationStatusVariant(s: string): InvocationStatusVariant {
  if (s === 'success') return 'ok'
  if (s === 'failure') return 'err'
  if (s === 'timeout') return 'stale'
  if (s === 'running') return 'warn'
  if (s === 'queued') return 'stale'
  return 'default'
}

/**
 * Queue/dispatch state of the agent_tasks row behind an invocation, when the
 * hub could resolve one.
 */
export type InvocationTaskState = {
  status: 'pending' | 'claimed' | 'completed' | 'failed'
  attempts: number
  error?: string
  /** First evidence of work (claim or first conversation row); absent while
   *  the task is queued. Distinct from the invocation's dispatch timestamp. */
  startedAt?: string
  /** Derived display state from the hub — prefer this over mapping `status`,
   *  which is ambiguous in per-task Job mode where "pending" covers both a
   *  queued task and a live run. */
  runState?: RunState
  /** Queued past the hub's start deadline: no worker is picking this up. */
  stuck?: boolean
}

// The run state to render for a task row. Falls back to the pre-#422 mapping
// (pending without start evidence reads as queued) when the hub predates the
// derived state; the fallback is wrong for live per-task Job runs, which is
// exactly what runState fixes.
export function taskRunState(t: InvocationTaskState): RunState {
  if (t.runState) return t.runState
  if (t.status === 'completed') return 'success'
  if (t.status === 'failed') return 'failure'
  if (t.status === 'claimed' || t.startedAt) return 'running'
  return 'queued'
}

/**
 * The single run state a view should render for an invocation: the derived
 * state when a task backs the row; a terminal invocation status when only the
 * record exists; otherwise `unknown` — a "running" record with no task behind
 * it (hub died before enqueue, or a pruned record) claims nothing.
 */
export function effectiveRunState(inv: {
  status: string
  task?: InvocationTaskState
}): RunState {
  if (inv.task) return taskRunState(inv.task)
  if (inv.status === 'success' || inv.status === 'failure' || inv.status === 'timeout') {
    return inv.status
  }
  return 'unknown'
}

export type InvocationEntry = {
  id: string
  agent: string
  agentName?: string
  trigger?: string
  triggerName?: string
  timestamp: string
  durationMs?: number
  status: InvocationStatus
  error?: string
  // Queue/dispatch state from the agent_tasks row backing this invocation,
  // when known. Lets the UI explain an empty transcript ("still queued"
  // vs "ran, produced nothing").
  task?: InvocationTaskState
}

export type ListInvocationsParams = {
  agent?: string
  status?: InvocationStatus
  event?: string
  since?: string
  pageSize?: number
}

// Backend uses `invocations` as the list key (not `items`) and adds `capacity`.
// Map to the shared Paginated<T> shape consumers expect.
type InvocationsEnvelope = {
  invocations: InvocationEntry[]
  total: number
  capacity: number
  page: number
  pageSize: number
  totalPages: number
}

export async function listInvocations(
  params: ListInvocationsParams = {},
): Promise<Paginated<InvocationEntry>> {
  const env = await request<InvocationsEnvelope>('/invocations', { query: params })
  return {
    items: env.invocations ?? [],
    total: env.total ?? 0,
    page: env.page ?? 1,
    pageSize: env.pageSize ?? 0,
    totalPages: env.totalPages ?? 0,
  }
}

export function useInvocations(
  params: ListInvocationsParams,
  opts: {
    // Straight from @tanstack/react-query: a plain interval, or a function
    // receiving the query (so callers can poll conditionally off
    // query.state.data without self-referencing the hook result).
    refetchInterval?:
      | number
      | false
      | ((query: { state: { data: Paginated<InvocationEntry> | undefined } }) => number | false | undefined)
    /** False skips the query entirely (e.g. run panels that only apply to consumer channels). */
    enabled?: boolean
  } = {},
) {
  return useQuery({
    queryKey: ['invocations', params],
    queryFn: () => listInvocations(params),
    placeholderData: keepPreviousData,
    refetchInterval: opts.refetchInterval ?? false,
    enabled: opts.enabled ?? true,
  })
}
