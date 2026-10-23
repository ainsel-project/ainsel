import { useEffect, useState, type ComponentType } from 'react'
import { loadPluginModule } from './loader'
import type { ActivePlugin } from './registry'

type LoadState = {
  key: string
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  component?: ComponentType<any>
  error?: Error
}

/**
 * Loads one export of a plugin module and renders it with props. While the
 * module loads it shows "Loading plugin…"; a failed import or a missing export
 * is thrown during render so the surrounding PluginBoundary shows the failure.
 */
export function PluginExport({
  plugin,
  exportName,
  props,
}: {
  plugin: ActivePlugin
  exportName: string
  props?: object
}) {
  const key = `${plugin.id}:${exportName}`
  const [state, setState] = useState<LoadState>({ key: '' })

  useEffect(() => {
    let cancelled = false
    loadPluginModule(plugin)
      .then((mod) => {
        const exported = mod[exportName]
        if (typeof exported !== 'function' && (typeof exported !== 'object' || exported === null)) {
          throw new Error(`Plugin ${plugin.id} has no export "${exportName}"`)
        }
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        if (!cancelled) setState({ key, component: exported as ComponentType<any> })
      })
      .catch((err: unknown) => {
        if (!cancelled) setState({ key, error: err instanceof Error ? err : new Error(String(err)) })
      })
    return () => {
      cancelled = true
    }
  }, [plugin, exportName, key])

  if (state.key !== key) return <div>Loading plugin…</div>
  if (state.error) throw state.error
  const Loaded = state.component as ComponentType<object>
  return <Loaded {...props} />
}
