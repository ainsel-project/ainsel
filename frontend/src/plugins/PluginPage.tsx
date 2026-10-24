import { useMemo } from 'react'
import type { PluginPage as PluginPageSpec } from '@ainsel/plugin-sdk'
import type { JSX } from 'react'
import { useParams, Navigate } from 'react-router-dom'
import { usePlugins, usePluginCallerContext } from '../api/plugins'
import { activePlugins, findPage, type ActivePlugin } from './registry'
import { PluginExport } from './PluginExport'
import { PluginBoundary } from './PluginBoundary'
import { PluginContextProvider } from './PluginContextProvider'
import { Titleblock } from '../layout/Titleblock'

/**
 * Routes plugin pages at /p/:pluginId/* routes.
 * Loads and renders the specified plugin page with the SDK context.
 */
export function PluginPage(): JSX.Element {
  const { pluginId, '*': splat = '' } = useParams()
  const { data: pluginItems, isPending: pluginsPending } = usePlugins()
  const { data: callerContext, isPending: callerPending } = usePluginCallerContext()

  const available = useMemo(() => activePlugins(pluginItems ?? []), [pluginItems])
  const plugin = useMemo(
    () => available.find((p) => p.id === pluginId),
    [available, pluginId],
  )
  const page = useMemo(() => {
    if (!plugin) return undefined
    return findPage(plugin, splat ?? '')
  }, [plugin, splat])

  // Wait for the plugin list before deciding a deep link is unknown.
  if (pluginsPending) {
    return <div>Loading plugin…</div>
  }

  // Redirect if plugin not found
  if (!plugin) {
    return <Navigate to="/dashboard" replace />
  }

  // Redirect if page not found
  if (!page) {
    return <Navigate to="/dashboard" replace />
  }

  // Redirect if page is admin-only and caller is not admin
  if (page.visibility === 'admin' && callerPending) {
    return <div>Loading plugin…</div>
  }
  if (page.visibility === 'admin' && !callerContext?.isAdmin) {
    return <Navigate to="/dashboard" replace />
  }

  return (
    <PluginPageContent plugin={plugin} page={page} />
  )
}

interface PluginPageContentProps {
  plugin: ActivePlugin
  page: PluginPageSpec
}

// The console frames every plugin page the way it frames its own: a title
// block (crumbs: the page's sidebar section, then its label) above a padded
// body. A plugin renders only the body, so every plugin looks the same.
function PluginPageContent({ plugin, page }: PluginPageContentProps): JSX.Element {
  return (
    <>
      <Titleblock
        crumbs={
          <>
            {page.section || plugin.displayName} / <b>{page.label}</b>
          </>
        }
        title={page.label}
      />
      <div style={{ padding: '28px 32px' }}>
        <PluginContextProvider plugin={plugin}>
          <PluginBoundary plugin={plugin}>
            <PluginExport plugin={plugin} exportName={page.component || 'default'} />
          </PluginBoundary>
        </PluginContextProvider>
      </div>
    </>
  )
}
