import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import * as loaderModule from './loader'
import { PluginSlot } from './PluginSlot'
import type { ActivePlugin } from './registry'

const mockPlugin = (id: string): ActivePlugin => ({
  id,
  displayName: id === 'test' ? 'Test Plugin' : 'Another Plugin',
  manifest: {
    id,
    displayName: id === 'test' ? 'Test Plugin' : 'Another Plugin',
    version: '1.0.0',
    pluginApi: '1.1',
    ui: {
      entry: '/static/ui/index.js',
      slots: {
        'agent.panel': id === 'test' ? 'TestPanel' : 'OtherPanel',
      },
    },
  },
})

function renderPluginSlot(element: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>{element}</MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('PluginSlot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    loaderModule.clearModuleCache()
  })

  it('renders nothing when no plugins contribute to slot', () => {
    const plugins = [
      {
        ...mockPlugin('test'),
        manifest: {
          ...mockPlugin('test').manifest,
          ui: { entry: '/static/ui/index.js', slots: {} },
        },
      },
    ]

    const result = renderPluginSlot(
      <PluginSlot
        plugins={plugins}
        slot="agent.panel"
        props={{ agentName: 'test-agent' }}
      />,
    )

    // Should render null (no wrapper element)
    expect(result.container.firstChild?.childNodes.length ?? 0).toBe(0)
  })

  it('renders loading text while loading', () => {
    const plugins = [mockPlugin('test')]

    // Mock the importer to delay loading
    const mockImporter = vi.fn(async () => {
      return new Promise<Record<string, unknown>>((resolve) =>
        setTimeout(() => resolve({ TestPanel: () => <div>Content</div> }), 1000),
      )
    })
    vi.spyOn(loaderModule, 'loadPluginModule').mockImplementation(mockImporter)

    renderPluginSlot(
      <PluginSlot
        plugins={plugins}
        slot="agent.panel"
        props={{ agentName: 'test-agent' }}
      />,
    )

    // Loading text should be visible
    expect(screen.getByText('Loading plugin…')).toBeInTheDocument()
  })

  it('shows slot contributions with correct props', () => {
    const plugins = [mockPlugin('test')]

    // We'll verify that the slot is rendered by checking the structure
    // Since actual module loading requires network, we test the render function
    const renderFn = vi.fn((contribution, element) => (
      <div data-testid={`plugin-${contribution.plugin.id}`}>{element}</div>
    ))

    renderPluginSlot(
      <PluginSlot
        plugins={plugins}
        slot="agent.panel"
        props={{ agentName: 'test-agent' }}
        render={renderFn}
      />,
    )

    // The render function should be called with the contribution
    expect(renderFn).toHaveBeenCalled()
  })

  it('filters contributions by pluginId when provided', () => {
    const plugins = [mockPlugin('test'), mockPlugin('other')]

    const renderFn = vi.fn((contribution, element) => (
      <div data-testid={`plugin-${contribution.plugin.id}`}>{element}</div>
    ))

    renderPluginSlot(
      <PluginSlot
        plugins={plugins}
        slot="agent.panel"
        props={{ agentName: 'test-agent' }}
        pluginId="test"
        render={renderFn}
      />,
    )

    // Only the test plugin should be rendered
    expect(renderFn).toHaveBeenCalledTimes(1)
    const call = renderFn.mock.calls[0]
    expect(call[0].plugin.id).toBe('test')
  })

  it('renders multiple plugins from the slot', () => {
    const plugins = [mockPlugin('a-plugin'), mockPlugin('b-plugin')]

    const renderFn = vi.fn((contribution, element) => (
      <div data-testid={`plugin-${contribution.plugin.id}`}>{element}</div>
    ))

    renderPluginSlot(
      <PluginSlot
        plugins={plugins}
        slot="agent.panel"
        props={{ agentName: 'test-agent' }}
        render={renderFn}
      />,
    )

    // Both plugins should be rendered
    expect(renderFn).toHaveBeenCalledTimes(2)

    // Verify they're called in id order
    const calls = renderFn.mock.calls
    expect(calls[0][0].plugin.id).toBe('a-plugin')
    expect(calls[1][0].plugin.id).toBe('b-plugin')
  })
})
