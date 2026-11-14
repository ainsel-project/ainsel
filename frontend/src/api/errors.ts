import { useQuery } from '@tanstack/react-query'
import { expectArray, request } from './client'

export type ErrorSeverity = 'error' | 'warning'
export type ErrorSource = 'router' | 'connector' | 'api' | 'agent' | 'hub' | 'gateway'

export type PlatformError = {
  id: string
  timestamp: string
  severity: ErrorSeverity
  source: ErrorSource
  message: string
  details?: Record<string, unknown>
}

export type ListErrorsParams = {
  limit?: number
  severity?: ErrorSeverity
  source?: ErrorSource
  since?: string
}

// Backend wraps the list in `{ errors, total }`; unwrap so consumers can
// keep treating the result as an array.
type ErrorsEnvelope = {
  errors: PlatformError[]
  total: number
}

export async function listErrors(params: ListErrorsParams = {}): Promise<PlatformError[]> {
  const env = await request<ErrorsEnvelope>('/errors', { query: params })
  // A paginated envelope or an HTML error page must surface as an ApiError
  // (issue #283) instead of a silently empty list or a render-time TypeError.
  return expectArray<PlatformError>(env?.errors, 'GET /errors')
}

export function useErrors(params: ListErrorsParams = {}) {
  return useQuery({
    queryKey: ['errors', params],
    queryFn: () => listErrors(params),
  })
}
