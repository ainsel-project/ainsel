import { Suspense, lazy, useMemo, type ComponentType } from 'react'
import type { JSX } from 'react'
import { useParams, Navigate } from 'react-router-dom'
import { usePlugins, usePluginCallerContext } from '../api/plugins'
import { activePlugins, findPage } from './registry'
import { loadPluginModule } from './loader'
import { PluginBoundary } from './PluginBoundary'
import { PluginContextProvider } from './PluginContextProvider'

/**
 * Routes plugin pages at /p/:pluginId/* routes.
 * Loads and renders the specified plugin page with the SDK context.
 */
export function PluginPage(): JSX.Element {
  const { pluginId, '*': splat = '' } = useParams()
  const { data: pluginItems } = usePlugins()
  const { data: callerContext } = usePluginCallerContext()

  const available = useMemo(() => activePlugins(pluginItems ?? []), [pluginItems])
  const plugin = useMemo(
    () => available.find((p) => p.id === pluginId),
    [available, pluginId],
  )
  const page = useMemo(() => {
    if (!plugin) return undefined
    return findPage(plugin, splat ?? '')
  }, [plugin, splat])

  // Redirect if plugin not found
  if (!plugin) {
    return <Navigate to="/dashboard" replace />
  }

  // Redirect if page not found
  if (!page) {
    return <Navigate to="/dashboard" replace />
  }

  // Redirect if page is admin-only and caller is not admin
  if (page.visibility === 'admin' && !callerContext?.isAdmin) {
    return <Navigate to="/dashboard" replace />
  }

  return (
    <PluginPageContent plugin={plugin} page={page} />
  )
}

interface PluginPageContentProps {
  plugin: Parameters<typeof PluginContextProvider>[0]['plugin']
  page: ReturnType<typeof findPage>
}

function PluginPageContent({
  plugin,
  page,
}: PluginPageContentProps): JSX.Element {
  if (!page) {
    // This shouldn't happen but TypeScript needs it
    return <Navigate to="/dashboard" replace />
  }

  const Component = useMemo(() => {
    // Create the lazy component factory
    // NOTE: This pattern is required for dynamic plugin loading - ESLint will flag
    // this as "Cannot create components during render", but it's correct because
    // the component is memoized and only recreated when plugin/page change
    const createComponent = async (): Promise<{ default: ComponentType }> => {
      try {
        const module = await loadPluginModule(plugin)
        const exportName = page.component || 'default'
        const component = (module[exportName] ?? module.default) as ComponentType | undefined
        if (!component) {
          throw new Error(`Export ${exportName} not found in plugin ${plugin.id}`)
        }
        return { default: component }
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        const err = new Error(message)
        // Preserve the original error as cause
        Object.defineProperty(err, 'cause', { value: error })
        throw err
      }
    }
    // eslint-disable-next-line react-hooks/static-components
    return lazy(createComponent)
  }, [plugin, page])

  return (
    <PluginContextProvider plugin={plugin}>
      <Suspense fallback={<div>Loading plugin…</div>}>
        <PluginBoundary plugin={plugin}>
          <Component />
        </PluginBoundary>
      </Suspense>
    </PluginContextProvider>
  )
}
