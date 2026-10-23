import { ReactNode, useMemo } from 'react'
import type { JSX } from 'react'
import { useNavigate } from 'react-router-dom'
import { PluginContext } from '@ainsel/plugin-sdk'
import type { PluginContextValue, HttpClient } from '@ainsel/plugin-sdk'
import { usePluginCallerContext } from '../api/plugins'
import { request } from '../api/client'
import type { ActivePlugin } from './registry'

interface PluginContextProviderProps {
  plugin: ActivePlugin
  children: ReactNode
}

/**
 * Provider for the plugin SDK context.
 * Builds HttpClient implementations and wraps the plugin context.
 */
export function PluginContextProvider({
  plugin,
  children,
}: PluginContextProviderProps): JSX.Element {
  const { data: callerContext } = usePluginCallerContext()
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const navigate = useNavigate()

  // Get current theme from document
  const theme = useMemo(() => {
    const theme = document.documentElement.dataset.theme as 'light' | 'dark' | undefined
    return theme ?? 'light'
  }, [])

  // Build the hub client (bound to /api/v1)
  const hubClient: HttpClient = useMemo(
    () => ({
      async get(path, init) {
        return request(path, { query: init?.query })
      },
      async post(path, body) {
        return request(path, { method: 'POST', body })
      },
      async put(path, body) {
        return request(path, { method: 'PUT', body })
      },
      async patch(path, body) {
        return request(path, { method: 'PATCH', body })
      },
      async delete(path) {
        return request(path, { method: 'DELETE' })
      },
    }),
    [],
  )

  // Build the plugin API client (bound to /plugins/<id>/api)
  const apiClient: HttpClient = useMemo(
    () => ({
      async get(path, init) {
        return request(`/plugins/${plugin.id}/api${path}`, { query: init?.query })
      },
      async post(path, body) {
        return request(`/plugins/${plugin.id}/api${path}`, { method: 'POST', body })
      },
      async put(path, body) {
        return request(`/plugins/${plugin.id}/api${path}`, { method: 'PUT', body })
      },
      async patch(path, body) {
        return request(`/plugins/${plugin.id}/api${path}`, { method: 'PATCH', body })
      },
      async delete(path) {
        return request(`/plugins/${plugin.id}/api${path}`, { method: 'DELETE' })
      },
    }),
    [plugin.id],
  )

  // Build the context value
  const contextValue: PluginContextValue = useMemo(
    () => ({
      pluginId: plugin.id,
      user: callerContext
        ? {
            sub: callerContext.sub,
            username: callerContext.username,
            isAdmin: callerContext.isAdmin,
            groups: callerContext.groups,
          }
        : {
            sub: '',
            username: '',
            isAdmin: false,
            groups: [],
          },
      hub: hubClient,
      api: apiClient,
      navigate: (to: string) => {
        const path = to.startsWith('/') ? `/p/${plugin.id}${to}` : `/p/${plugin.id}/${to}`
        navigate(path)
      },
      theme,
    }),
    [plugin.id, callerContext, hubClient, apiClient, navigate, theme],
  )

  return <PluginContext.Provider value={contextValue}>{children}</PluginContext.Provider>
}
