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

export interface PluginContextValue {
  pluginId: string
  user: PluginUser
  hub: HttpClient
  api: HttpClient
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
