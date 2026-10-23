import { Suspense, lazy, useMemo, type ComponentType } from 'react'
import type { JSX } from 'react'
import type { SlotId, SlotProps } from '@ainsel/plugin-sdk'
import { loadPluginModule } from './loader'
import { slotContributions } from './registry'
import type { ActivePlugin } from './registry'
import { PluginBoundary } from './PluginBoundary'
import { PluginContextProvider } from './PluginContextProvider'

interface PluginSlotProps<S extends SlotId> {
  plugins: ActivePlugin[]
  slot: S
  props: SlotProps[S]
  /** Optional single plugin id to limit contributions to that plugin */
  pluginId?: string
  render?: (contribution: { plugin: ActivePlugin; exportName: string }, element: JSX.Element) => React.ReactNode
}

/**
 * Renders contributions to a slot from active plugins.
 * Returns null if there are no contributors.
 */
export function PluginSlot<S extends SlotId>({
  plugins,
  slot,
  props,
  pluginId,
  render,
}: PluginSlotProps<S>): JSX.Element | null {
  const contributions = useMemo(() => {
    let all = slotContributions(plugins, slot)
    if (pluginId) {
      all = all.filter((c) => c.plugin.id === pluginId)
    }
    return all
  }, [plugins, slot, pluginId])

  if (contributions.length === 0) {
    return null
  }

  return (
    <>
      {contributions.map((contribution) => (
        <PluginSlotContribution
          key={contribution.plugin.id}
          contribution={contribution}
          slotProps={props}
          render={render}
        />
      ))}
    </>
  )
}

interface PluginSlotContributionProps<S extends SlotId> {
  contribution: { plugin: ActivePlugin; exportName: string }
  slotProps: SlotProps[S]
  render?: (contribution: { plugin: ActivePlugin; exportName: string }, element: JSX.Element) => React.ReactNode
}

function PluginSlotContribution<S extends SlotId>({
  contribution,
  slotProps,
  render,
}: PluginSlotContributionProps<S>) {
  const { plugin, exportName } = contribution

  const Component = useMemo(() => {
    // Create the lazy component factory
    // The component is created dynamically per plugin, so it must be in useMemo
    // NOTE: This pattern is required for dynamic plugin loading - ESLint will flag
    // this as "Cannot create components during render", but it's correct because
    // the component is memoized and only recreated when plugin/exportName change
    const createComponent = async (): Promise<{ default: ComponentType<SlotProps[S]> }> => {
      try {
        const module = await loadPluginModule(plugin)
        const component = (module[exportName] ?? module.default) as unknown
        if (!component) {
          throw new Error(`Export ${exportName} not found in plugin ${plugin.id}`)
        }
        return { default: component as ComponentType<SlotProps[S]> }
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
  }, [plugin, exportName])

  const element = (
    <PluginContextProvider plugin={plugin}>
      <Suspense fallback={<div>Loading plugin…</div>}>
        <PluginBoundary plugin={plugin}>
          <Component {...slotProps} />
        </PluginBoundary>
      </Suspense>
    </PluginContextProvider>
  )

  if (render) {
    return <>{render(contribution, element)}</>
  }

  return element
}
