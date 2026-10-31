import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { MCPServerForm } from './MCPServerForm'
import { renderWithProviders } from '../../test/renderWithProviders'

// Issue #146: an externally managed MCP server is read-only in the console —
// the hub refuses update/delete with 409, so the form offers neither.
function stubServer(server: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('/mcp-servers/example-mcp')) {
        return Promise.resolve(new Response(JSON.stringify(server), { status: 200 }))
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    }),
  )
}

function renderForm() {
  return renderWithProviders(
    <Routes>
      <Route path="/settings/mcp-servers/:name" element={<MCPServerForm />} />
    </Routes>,
    { route: '/settings/mcp-servers/example-mcp' },
  )
}

describe('MCPServerForm (externally managed)', () => {
  beforeEach(() => {
    stubServer({
      name: 'example-mcp',
      displayName: 'Example MCP',
      url: 'http://example-mcp',
      managedBy: 'helm',
      createdAt: '2026-05-01T00:00:00Z',
      updatedAt: '2026-05-01T00:00:00Z',
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('hides Save and Delete and marks the server as managed', async () => {
    renderForm()
    await waitFor(() => expect(screen.getByText('managed by helm')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument()
  })
})

describe('MCPServerForm (console-managed)', () => {
  beforeEach(() => {
    stubServer({
      name: 'example-mcp',
      displayName: 'Example MCP',
      url: 'http://example-mcp',
      managedBy: 'user',
      createdAt: '2026-05-01T00:00:00Z',
      updatedAt: '2026-05-01T00:00:00Z',
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps Save and Delete and shows no badge', async () => {
    renderForm()
    await waitFor(() => expect(screen.getByDisplayValue('http://example-mcp')).toBeInTheDocument())
    expect(screen.queryByText('managed by user')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^save$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^delete$/i })).toBeInTheDocument()
  })
})
