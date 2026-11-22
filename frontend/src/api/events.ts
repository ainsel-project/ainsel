import { useQuery } from '@tanstack/react-query'
import { request } from './client'
import type { InvocationStatus, RunState } from './invocations'

export type { RunState }

/** The event's match result — did it route to an agent, and did any task fail?
 *  The other axis on the activity page is `runState` (see RunState), which
 *  answers where the run is right now. */
export type ActivityMatchResult = 'matched' | 'unmatched' | 'error'

export type ActivityMatch = {
  trigger: string
  agent: string
  /** The agent's API id (its resource name) — the route key for
   *  /agents/:id. Absent on hubs that predate the field: render plain text. */
  agentId?: string
  /** Raw `invocations.status` for the run behind this match — the column
   *  mirrored, so `unknown` is possible for a record the hub recorded but never
   *  enqueued. Render `runState` instead: it is derived from the task and tells
   *  queued apart from running. The hub still emits the retired `runStatus`
   *  spelling with the same value; the console reads this one. */
  invocationStatus?: InvocationStatus | 'unknown'
  durationMs?: number
  error?: string
  /** Raw agent_tasks status when a task row exists. */
  taskStatus?: 'pending' | 'claimed' | 'completed' | 'failed' | 'expired'
  /** Derived display state from the hub — the value to render. */
  runState?: RunState
  /** First evidence of work (claim or first conversation row). */
  startedAt?: string
  /** Queued past the hub's start deadline. */
  stuck?: boolean
  /** The run succeeded only after an attempt hit the runner's turn timeout —
   *  a modifier on `success`, never a state of its own (#392). */
  timedOut?: boolean
}

export type ActivityEntry = {
  id: string
  timestamp: string
  connector?: string
  /** Channel the event was born in. Absent on history recorded before the
   *  channel registry existed and on direct (cron/chat) births. */
  channelId?: string
  /** Did the event match a subscription? A different axis from a match's
   *  runState. The hub also still emits the retired `status` spelling with the
   *  same value; the console reads this one — hub and console ship together. */
  match: ActivityMatchResult
  matches?: ActivityMatch[]
  payload?: unknown
}

/** The run states the activity list can be filtered by: every RunState except
 *  `unknown`, which is the absence of a state rather than one. */
export type RunStateFilter = Exclude<RunState, 'unknown'>

export type ListEventsParams = {
  limit?: number
  offset?: number
  /** Did the event match a subscription? */
  match?: ActivityMatchResult
  connector?: string
  agent?: string
  /** Derived run state of any of the event's tasks, filtered in SQL on the
   *  same started predicate the rows render, so the whole history is walked
   *  and the filter cannot disagree with the table. */
  runState?: RunStateFilter
  since?: string
}

// Backend wraps the list in `{ events, total }`; total is the number of all
// events matching the filters (not just the returned page), which the
// Activity page uses to drive its pager.
type EventsEnvelope = {
  events: ActivityEntry[]
  total: number
}

export type EventsPage = {
  events: ActivityEntry[]
  total: number
}

export async function listEventsPage(params: ListEventsParams = {}): Promise<EventsPage> {
  const env = await request<EventsEnvelope>('/events', { query: params })
  return { events: env.events ?? [], total: env.total ?? 0 }
}

export async function listEvents(params: ListEventsParams = {}): Promise<ActivityEntry[]> {
  const env = await listEventsPage(params)
  return env.events
}

export async function getEvent(id: string): Promise<ActivityEntry> {
  return request<ActivityEntry>(`/events/${encodeURIComponent(id)}`)
}

export function useEvent(id: string) {
  return useQuery({
    queryKey: ['events', 'detail', id],
    queryFn: () => getEvent(id),
    enabled: id !== '',
  })
}

export function useRecentEvents(limit: number) {
  return useQuery({
    queryKey: ['events', 'recent', limit],
    queryFn: () => listEvents({ limit }),
  })
}

export function useEventsPage(params: ListEventsParams) {
  return useQuery({
    queryKey: [
      'events',
      'page',
      params.limit ?? null,
      params.offset ?? null,
      params.match ?? null,
      params.connector ?? null,
      params.agent ?? null,
      params.runState ?? null,
      params.since ?? null,
    ],
    queryFn: () => listEventsPage(params),
  })
}
