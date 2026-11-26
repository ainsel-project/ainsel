import { createContext, useContext } from 'react'

export interface PluginUser {
  sub: string
  username: string
  isAdmin: boolean
  groups: {
    id: string
    name: string
    role: string
  }[]
}

export interface HttpClient {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  get<T = any>(
    path: string,
    init?: { query?: Record<string, string> },
  ): Promise<T>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  post<T = any>(path: string, body?: unknown): Promise<T>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  put<T = any>(path: string, body?: unknown): Promise<T>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  patch<T = any>(path: string, body?: unknown): Promise<T>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete<T = any>(path: string): Promise<T>
}

/**
 * The client for the plugin's own api surface: everything HttpClient does, plus
 * a WebSocket the console opens for you.
 */
export interface PluginApiClient extends HttpClient {
  /**
   * Opens a WebSocket to `path` on the plugin's api surface, authenticated as
   * the signed-in user. Browsers cannot set an Authorization header on a
   * WebSocket, so the console offers the user's token to the hub itself; the
   * plugin never sees the token (the hub strips it) and neither does your code.
   * `protocols` are your own subprotocols; "ainsel.bearer.*" is reserved.
   *
   * Optional: a console older than this method does not provide it, so check
   * `typeof api.openWebSocket === 'function'` and degrade.
   */
  openWebSocket?(path: string, protocols?: string[]): WebSocket
}

export interface PluginContextValue {
  pluginId: string
  user: PluginUser
  hub: HttpClient
  api: PluginApiClient
  navigate(to: string): void
  theme: 'light' | 'dark'
}

export const PluginContext = createContext<PluginContextValue | null>(null)

export function usePluginContext(): PluginContextValue {
  const context = useContext(PluginContext)
  if (!context) {
    throw new Error(
      'usePluginContext must be used inside a plugin page or slot',
    )
  }
  return context
}
