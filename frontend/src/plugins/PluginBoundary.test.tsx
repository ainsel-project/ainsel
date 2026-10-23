import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PluginBoundary } from './PluginBoundary'
import type { ActivePlugin } from './registry'

const mockPlugin = (id: string): ActivePlugin => ({
  id,
  displayName: 'Mem0',
  manifest: {
    id,
    displayName: 'Mem0',
    version: '1.0.0',
    pluginApi: '1.1',
  },
})

describe('PluginBoundary', () => {
  it('renders children normally', () => {
    const plugin = mockPlugin('test')
    render(
      <PluginBoundary plugin={plugin}>
        <div>Test content</div>
      </PluginBoundary>,
    )
    expect(screen.getByText('Test content')).toBeInTheDocument()
  })

  it('catches errors and renders failure notice', () => {
    const plugin = mockPlugin('test')
    const ThrowingComponent = () => {
      throw new Error('Component failed')
    }

    // Suppress console.error for this test
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <PluginBoundary plugin={plugin}>
        <ThrowingComponent />
      </PluginBoundary>,
    )

    expect(screen.getByText(/Plugin Mem0 failed to load/)).toBeInTheDocument()
    expect(screen.getByText(/Component failed/)).toBeInTheDocument()

    consoleSpy.mockRestore()
  })

  it('renders failure notice with the error message', () => {
    const plugin = mockPlugin('test')
    const ThrowingComponent = () => {
      throw new Error('Specific error message')
    }

    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <PluginBoundary plugin={plugin}>
        <ThrowingComponent />
      </PluginBoundary>,
    )

    expect(screen.getByText(/Specific error message/)).toBeInTheDocument()

    consoleSpy.mockRestore()
  })

  it('error boundary only affects the bounded plugin', () => {
    const plugin = mockPlugin('test')
    const ThrowingComponent = () => {
      throw new Error('Plugin error')
    }

    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <div>
        <div>Sibling outside boundary</div>
        <PluginBoundary plugin={plugin}>
          <ThrowingComponent />
        </PluginBoundary>
      </div>,
    )

    expect(screen.getByText('Sibling outside boundary')).toBeInTheDocument()
    expect(screen.getByText(/Plugin Mem0 failed to load/)).toBeInTheDocument()

    consoleSpy.mockRestore()
  })
})
