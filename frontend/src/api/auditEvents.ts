import { useQuery } from '@tanstack/react-query'
import { request } from './client'
import type { Paginated } from './types'

/** One persisted auth event (issue #116). Mirrors the hub's auth_events row:
 *  no secrets — the write path never sees a password or token. */
export type AuthEvent = {
  id: number
  occurredAt: string
  actorSub: string
  eventType: string
  target: string
  sourceIp: string
  outcome: 'success' | 'failure' | string
}

/** The six event types the hub writes. Kept as data so the filter select and
 *  any display labels stay in one place; the backend accepts any string. */
export const AUTH_EVENT_TYPES = [
  'auth_login',
  'auth_login_failed',
  'auth_password_changed',
  'user_created',
  'user_password_reset',
  'user_deleted',
] as const

export type ListAuthEventsParams = {
  /** Matches actor OR target: "everything involving this user". */
  user?: string
  /** Exact event type; omit for all types. */
  type?: string
  /** RFC3339, inclusive. */
  from?: string
  /** RFC3339, exclusive. */
  to?: string
  page?: number
  pageSize?: number
}

export function listAuthEvents(params: ListAuthEventsParams = {}) {
  return request<Paginated<AuthEvent>>('/audit/auth-events', { query: params })
}

export function useAuthEvents(params: ListAuthEventsParams = {}) {
  return useQuery({
    queryKey: ['audit', 'auth-events', params],
    queryFn: () => listAuthEvents(params),
  })
}
