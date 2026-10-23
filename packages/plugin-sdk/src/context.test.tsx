import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  PluginContext,
  usePluginContext,
  type PluginContextValue,
} from './context'

describe('usePluginContext', () => {
  it('throws error when used outside a provider', () => {
    const Component = () => {
      usePluginContext()
      return <div>test</div>
    }

    expect(() => {
      render(<Component />)
    }).toThrow('usePluginContext must be used inside a plugin page or slot')
  })

  it('returns the value when inside a provider', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const mockHttpClient: any = {
      get: () => Promise.resolve({}),
      post: () => Promise.resolve({}),
      put: () => Promise.resolve({}),
      patch: () => Promise.resolve({}),
      delete: () => Promise.resolve({}),
    }

    const mockValue: PluginContextValue = {
      pluginId: 'test-plugin',
      user: {
        sub: 'user-123',
        username: 'testuser',
        isAdmin: false,
        groups: [],
      },
      hub: mockHttpClient,
      api: mockHttpClient,
      navigate: () => {},
      theme: 'light',
    }

    const Component = () => {
      const ctx = usePluginContext()
      return <div>{ctx.pluginId}</div>
    }

    render(
      <PluginContext.Provider value={mockValue}>
        <Component />
      </PluginContext.Provider>,
    )

    expect(screen.getByText('test-plugin')).toBeDefined()
  })
})
