import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PluginBoundary } from './PluginBoundary'
import { PluginExport } from './PluginExport'
import type { ActivePlugin } from './registry'

vi.mock('./loader', () => ({ loadPluginModule: vi.fn() }))
import { loadPluginModule } from './loader'

const plugin: ActivePlugin = {
  id: 'mem0',
  displayName: 'Mem0',
  manifest: { id: 'mem0', displayName: 'Mem0', version: '1.0.0', pluginApi: '1.1', ui: { entry: '/ui/index.js' } },
}

describe('PluginExport', () => {
  beforeEach(() => {
    vi.mocked(loadPluginModule).mockReset()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders the named export with its props', async () => {
    vi.mocked(loadPluginModule).mockResolvedValue({
      MemoryPanel: ({ agentName }: { agentName: string }) => <p>agent={agentName}</p>,
    })
    render(
      <PluginBoundary plugin={plugin}>
        <PluginExport plugin={plugin} exportName="MemoryPanel" props={{ agentName: 'reviewer' }} />
      </PluginBoundary>,
    )
    expect(screen.getByText('Loading plugin…')).toBeInTheDocument()
    expect(await screen.findByText('agent=reviewer')).toBeInTheDocument()
  })

  it('fails instead of falling back to the default export when the named export is missing', async () => {
    vi.mocked(loadPluginModule).mockResolvedValue({ default: () => <p>default page</p> })
    render(
      <PluginBoundary plugin={plugin}>
        <PluginExport plugin={plugin} exportName="MemoryPanel" />
      </PluginBoundary>,
    )
    expect(await screen.findByText(/Plugin Mem0 failed to load/)).toBeInTheDocument()
    expect(screen.getByText(/has no export "MemoryPanel"/)).toBeInTheDocument()
    expect(screen.queryByText('default page')).not.toBeInTheDocument()
  })
})
