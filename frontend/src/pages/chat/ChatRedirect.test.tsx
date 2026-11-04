import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { ChatRedirect } from './ChatRedirect'

const sessionState = vi.hoisted(() => ({
  session: { id: 's1', name: 'chat', agentName: 'dev', userId: 'u1', createdAt: '', updatedAt: '' },
}))

vi.mock('../../api/chat', () => ({
  useChatSession: () => ({ data: sessionState.session, isLoading: false, isError: false }),
}))

vi.mock('../../api/agents', () => ({
  // Sessions carry the agent's display name; the workspace route takes the CR id.
  useAgents: () => ({
    data: {
      items: [{ id: 'a-dev', name: 'dev' }],
      total: 1,
      page: 1,
      pageSize: 100,
      totalPages: 1,
    },
    isError: false,
  }),
}))

function LocationProbe() {
  const location = useLocation()
  return <p data-testid="location">{location.pathname}</p>
}

describe('ChatRedirect', () => {
  it('sends /chat/:id to the agent workspace chat section', () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={['/chat/s1']}>
          <Routes>
            <Route path="/chat/:id" element={<ChatRedirect />} />
            <Route path="/agents/:id/:section" element={<LocationProbe />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/agents\/a-dev\/chat$/)
  })
})
