import { useMemo } from 'react'
import type { JSX } from 'react'
import type { SlotId, SlotProps } from '@ainsel/plugin-sdk'
import { PluginExport } from './PluginExport'
import { activePlugins, slotContributions } from './registry'
import { usePlugins } from '../api/plugins'
import type { ActivePlugin } from './registry'
import { PluginBoundary } from './PluginBoundary'
import { PluginContextProvider } from './PluginContextProvider'

interface PluginSlotProps<S extends SlotId> {
  /** Active plugins; defaults to the hub's plugin list. */
  plugins?: ActivePlugin[]
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
  const { data: items } = usePlugins()
  const fetched = useMemo(() => activePlugins(items ?? []), [items])
  const contributions = useMemo(() => {
    let all = slotContributions(plugins ?? fetched, slot)
    if (pluginId) {
      all = all.filter((c) => c.plugin.id === pluginId)
    }
    return all
  }, [plugins, fetched, slot, pluginId])

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
  const element = (
    <PluginContextProvider plugin={plugin}>
      <PluginBoundary plugin={plugin}>
        <PluginExport plugin={plugin} exportName={exportName} props={slotProps as object} />
      </PluginBoundary>
    </PluginContextProvider>
  )
  return render ? <>{render(contribution, element)}</> : element
}
