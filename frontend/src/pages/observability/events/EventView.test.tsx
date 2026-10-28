import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, fireEvent, act, within } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { EventView } from './EventView'
import { renderWithProviders } from '../../../test/renderWithProviders'

// Mock usePlugins to return a mem0 plugin with all four slots
vi.mock('../../../api/plugins', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/plugins')>()
  return {
    ...actual,
    usePlugins: () => ({
      data: [
        {
          id: 'mem0',
          displayName: 'Mem0',
          ready: true,
          disabled: false,
          manifest: {
            id: 'mem0',
            displayName: 'Mem0',
            version: '1.0.0',
            pluginApi: '1.1',
            ui: {
              entry: '/ui/index.js',
              slots: {
                'agent.panel': 'AgentPanel',
                'persona.panel': 'PersonaPanel',
                'observability.section': 'ObsSection',
                'invocation.panel': 'RunPanel',
              },
            },
          },
        },
      ],
      isPending: false,
    }),
  }
})

// Mock loadPluginModule to return components that render their props
vi.mock('../../../plugins/loader', () => {
  return {
    loadPluginModule: vi.fn(async () => ({
      AgentPanel: ({ agentName }: { agentName: string }) => (
        <p>{`agentName=${agentName}`}</p>
      ),
      PersonaPanel: ({ personaId }: { personaId: string }) => (
        <p>{`personaId=${personaId}`}</p>
      ),
      ObsSection: ({ range }: { range: string }) => <p>{`range=${range}`}</p>,
      RunPanel: ({ invocationId, agentName }: { invocationId: string; agentName: string }) => (
        <p>{`invocationId=${invocationId} agentName=${agentName}`}</p>
      ),
    })),
  }
})

const sampleEvent = {
  id: 'evt-1234567890000000000',
  timestamp: '2026-06-10T08:31:25Z',
  connector: 'c-111',
  status: 'matched',
  matches: [
    { trigger: 't1', agent: 'doc-writer' },
    { trigger: 't2', agent: 'review-bot' },
  ],
  payload: { action: 'opened', repository: { full_name: 'ainsel-project/ainsel' } },
}

const sampleInvocations = {
  invocations: [
    {
      id: 'inv-1',
      agent: 'review-bot',
      agentName: 'review-bot',
      trigger: 't1',
      triggerName: 't1',
      status: 'success',
      durationMs: 14000,
      timestamp: '2026-06-10T08:31:25Z',
    },
  ],
  total: 1,
  capacity: 1000,
  page: 1,
  pageSize: 50,
  totalPages: 1,
}

const sampleConversation = {
  messages: [
    {
      id: 1,
      role: 'user',
      content: '[{"type":"text","text":"Handle the event"}]',
      agentName: 'review-bot',
      createdAt: '2026-06-10T08:31:26Z',
    },
    {
      id: 2,
      role: 'assistant',
      content: '[{"type":"text","text":"Done reviewing"}]',
      model: 'claude-x',
      inputTokens: 100,
      outputTokens: 50,
      stopReason: 'endTurn',
      agentName: 'review-bot',
      createdAt: '2026-06-10T08:31:30Z',
    },
  ],
  total: 2,
}

function mockFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('/invocations') && url.includes('event=')) {
        return Promise.resolve(
          new Response(JSON.stringify(sampleInvocations), { status: 200 }),
        )
      }
      if (url.includes('/observability/conversations') && url.includes('invocation=inv-1')) {
        return Promise.resolve(
          new Response(JSON.stringify(sampleConversation), { status: 200 }),
        )
      }
      if (url.includes('/events/evt-')) {
        return Promise.resolve(
          new Response(JSON.stringify(sampleEvent), { status: 200 }),
        )
      }
      if (url.includes('/connectors')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: [
                { id: 'c-111', name: 'forgejo-main', signatureHeader: 'x-ainsel-signature', disabled: false },
              ],
              total: 1,
              page: 1,
              pageSize: 200,
              totalPages: 1,
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    }),
  )
}

function renderEventView(route: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/observability/events/:id" element={<EventView />} />
    </Routes>,
    { route },
  )
}

describe('EventView', () => {
  beforeEach(() => {
    mockFetch()
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('renders the page heading', () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    expect(screen.getByRole('heading', { name: /event\s+detail/i })).toBeInTheDocument()
  })

  it('renders event metadata', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    // connector name resolves in the KPI figure and again as the journey's home channel
    await waitFor(() =>
      expect(screen.getAllByText('forgejo-main').length).toBeGreaterThanOrEqual(2),
    )
    expect(screen.getByText('MATCH')).toBeInTheDocument()
  })

  // #152 — the Connector KPI must show the connector's name, not its raw id.
  it('shows the connector name in the Connector KPI', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    const kpi = await waitFor(
      () => screen.getByText('Connector').closest('.kpi') as HTMLElement,
    )
    await waitFor(() => expect(within(kpi).getByText('forgejo-main')).toBeInTheDocument())
    expect(within(kpi).queryByText('c-111')).toBeNull()
  })

  // #152 — an unknown/deleted connector still renders its id, as a secondary
  // fallback carrying the full id in `title`.
  it('falls back to the connector id with a title when the name cannot be resolved', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/events/evt-')) {
          return Promise.resolve(new Response(JSON.stringify(sampleEvent), { status: 200 }))
        }
        if (url.includes('/connectors')) {
          return Promise.resolve(
            new Response(JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }), {
              status: 200,
            }),
          )
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000000')
    const kpi = await waitFor(
      () => screen.getByText('Connector').closest('.kpi') as HTMLElement,
    )
    const el = await waitFor(() => within(kpi).getByText('c-111'))
    expect(el).toHaveAttribute('title', 'c-111')
  })

  it('renders the absolute timestamp in the When KPI', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() =>
      expect(screen.getByText('2026-06-10 08:31:25Z')).toBeInTheDocument(),
    )
  })

  it('renders matches', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    // agent appears in the matches list, the journey fan-out, and the invocation summary
    await waitFor(() => expect(screen.getAllByText('doc-writer').length).toBeGreaterThanOrEqual(2))
    // review-bot appears in the matches list and in the invocation summary
    expect(screen.getAllByText('review-bot').length).toBeGreaterThanOrEqual(2)
  })

  it('renders payload as formatted JSON', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() => expect(screen.getByText('Payload')).toBeInTheDocument())
    expect(screen.getByText(/opened/)).toBeInTheDocument()
  })

  it('renders the invocation summary with duration and token usage', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() => expect(screen.getByText('150')).toBeInTheDocument())
    expect(screen.getByText('14.0s')).toBeInTheDocument()
    expect(screen.getByText('Invocation inv-1 · review-bot')).toBeInTheDocument()
    const successTag = screen.getByText('success')
    expect(successTag).toBeInTheDocument()
    expect(successTag).toHaveClass('ok')
  })

  it('renders a failed invocation with the error variant', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/invocations') && url.includes('event=')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                ...sampleInvocations,
                invocations: [{ ...sampleInvocations.invocations[0], status: 'failure' }],
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/events/evt-')) {
          return Promise.resolve(new Response(JSON.stringify(sampleEvent), { status: 200 }))
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000000')
    const failureTag = await screen.findByText('failure')
    expect(failureTag).toHaveClass('err')
  })

  it('renders the conversation transcript for the invocation', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() => expect(screen.getByText('Done reviewing')).toBeInTheDocument())
    expect(screen.getByText('Handle the event')).toBeInTheDocument()
    expect(screen.getByText(/claude-x/)).toBeInTheDocument()
  })

  it('shows a note when the event has no invocations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/invocations')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                invocations: [],
                total: 0,
                capacity: 1000,
                page: 1,
                pageSize: 50,
                totalPages: 0,
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/events/evt-')) {
          return Promise.resolve(
            new Response(JSON.stringify(sampleEvent), { status: 200 }),
          )
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() =>
      expect(screen.getByText('No invocations recorded for this event.')).toBeInTheDocument(),
    )
  })

  it('explains an empty transcript for a task that is still queued (#195)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/invocations') && url.includes('event=')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                invocations: [
                  {
                    id: 'inv-2',
                    agent: 'dev-bot',
                    agentName: 'dev-bot',
                    trigger: 't2',
                    triggerName: 't2',
                    status: 'running',
                    timestamp: '2026-06-10T08:35:25Z',
                    task: { status: 'pending', attempts: 2, error: 'Turn timed out after 600000ms' },
                  },
                ],
                total: 1,
                capacity: 1000,
                page: 1,
                pageSize: 50,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/observability/conversations') && url.includes('invocation=inv-2')) {
          return Promise.resolve(new Response(JSON.stringify({ messages: [], total: 0 }), { status: 200 }))
        }
        if (url.includes('/events/evt-')) {
          return Promise.resolve(new Response(JSON.stringify(sampleEvent), { status: 200 }))
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000001')
    await waitFor(() =>
      expect(screen.getByText(/Waiting in queue/)).toBeInTheDocument(),
    )
    expect(screen.getByText(/attempt 2 failed, retrying/)).toBeInTheDocument()
    expect(screen.getByText('queued · attempt 2')).toBeInTheDocument()
    expect(screen.queryByText(/No conversation recorded/)).not.toBeInTheDocument()
  })

  // Review #427: a retried task that has waited past the start deadline must
  // show both suffixes — "stuck" is the more actionable signal and must not
  // be hidden behind "attempt N".
  it('shows attempt and stuck together for a retried task past the start deadline', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/invocations') && url.includes('event=')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                invocations: [
                  {
                    id: 'inv-stuck',
                    agent: 'dev-bot',
                    agentName: 'dev-bot',
                    trigger: 't2',
                    triggerName: 't2',
                    status: 'running',
                    timestamp: '2026-06-10T08:35:25Z',
                    task: { status: 'pending', attempts: 1, stuck: true, runState: 'queued' },
                  },
                ],
                total: 1,
                capacity: 1000,
                page: 1,
                pageSize: 50,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/observability/conversations') && url.includes('invocation=inv-stuck')) {
          return Promise.resolve(new Response(JSON.stringify({ messages: [], total: 0 }), { status: 200 }))
        }
        if (url.includes('/events/evt-')) {
          return Promise.resolve(new Response(JSON.stringify(sampleEvent), { status: 200 }))
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000003')
    await waitFor(() =>
      expect(screen.getByText('queued · attempt 1 · stuck')).toBeInTheDocument(),
    )
  })

  // #422: in per-task Job mode the task row stays "pending" while the pod
  // works. The queued tag must follow the derived state, so a live run shows
  // running and no queued tag — and a task with no worker still shows queued.
  it('shows running — not queued — for a per-task Job run with start evidence', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/invocations') && url.includes('event=')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                invocations: [
                  {
                    id: 'inv-job',
                    agent: 'dev-bot',
                    agentName: 'dev-bot',
                    trigger: 't2',
                    triggerName: 't2',
                    status: 'running',
                    timestamp: '2026-06-10T08:35:25Z',
                    task: {
                      status: 'pending',
                      attempts: 0,
                      startedAt: '2026-06-10T08:35:26Z',
                      runState: 'running',
                    },
                  },
                ],
                total: 1,
                capacity: 1000,
                page: 1,
                pageSize: 50,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/observability/conversations') && url.includes('invocation=inv-job')) {
          return Promise.resolve(new Response(JSON.stringify({ messages: [], total: 0 }), { status: 200 }))
        }
        if (url.includes('/events/evt-')) {
          return Promise.resolve(new Response(JSON.stringify(sampleEvent), { status: 200 }))
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderEventView('/observability/events/evt-1234567890000000002')
    await waitFor(() =>
      expect(screen.getByText(/Processing — the agent is working/)).toBeInTheDocument(),
    )
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.queryByText(/queued/)).not.toBeInTheDocument()
  })

  it('polls a running invocation until the transcript arrives', async () => {
    vi.useFakeTimers()
    const runningInvocations = {
      ...sampleInvocations,
      invocations: [
        { ...sampleInvocations.invocations[0], status: 'running', durationMs: undefined },
      ],
    }
    // The conversation endpoint answers with an empty transcript first and
    // only returns the messages once the run has produced them.
    let conversationArrived = false
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.includes('/invocations') && url.includes('event=')) {
          return new Response(JSON.stringify(runningInvocations), { status: 200 })
        }
        if (url.includes('/observability/conversations') && url.includes('invocation=inv-1')) {
          const body = conversationArrived ? sampleConversation : { messages: [], total: 0 }
          return new Response(JSON.stringify(body), { status: 200 })
        }
        if (url.includes('/events/evt-')) {
          return new Response(JSON.stringify(sampleEvent), { status: 200 })
        }
        return new Response('{}', { status: 200 })
      }),
    )

    try {
      renderEventView('/observability/events/evt-1234567890000000000')
      // Initially the invocation is running and no transcript exists yet.
      // Fake timers freeze react-query's setTimeout-based notification
      // batching, so each flush round must advance the clock (firing the
      // batchers) and let microtasks settle before re-checking the DOM.
      for (
        let i = 0;
        i < 20 && !screen.queryByText('No conversation recorded for this invocation.');
        i++
      ) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(10)
        })
      }
      expect(
        screen.getByText('No conversation recorded for this invocation.'),
      ).toBeInTheDocument()

      // The run produces its transcript messages; without polling the page
      // would keep showing the empty state until a manual refresh. Advance one
      // poll interval (RUNNING_POLL_MS) and the transcript must appear.
      conversationArrived = true
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })
      for (let i = 0; i < 20 && !screen.queryByText('Done reviewing'); i++) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(10)
        })
      }
      expect(screen.getByText('Done reviewing')).toBeInTheDocument()
      expect(screen.getByText('Handle the event')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows error state when fetch fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('{}', { status: 500 }))),
    )
    renderEventView('/observability/events/evt-1234567890000000000')
    await waitFor(() => expect(screen.getByText(/failed to load/i)).toBeInTheDocument())
  })

  it('renders invocations before the payload', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    const invocationHeading = await screen.findByText('Invocation inv-1 · review-bot')
    const payloadHeading = screen.getByText('Payload')
    expect(
      invocationHeading.compareDocumentPosition(payloadHeading) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('caps the payload and invocations regions with a scroll container', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    const invocationHeading = await screen.findByText('Invocation inv-1 · review-bot')
    expect(invocationHeading.closest('.scroll-cap')).toBeInTheDocument()
    const payloadPanel = screen.getByText('Payload').closest('.panel')
    expect(payloadPanel?.querySelector('.scroll-cap')).toBeInTheDocument()
  })

  it('keeps the invocations scroll cap out of the panel layout', async () => {
    // A capped region only scrolls while its content overflows it. Laying the
    // cap out as a grid lets `.panel`, which is a scroll container in its own
    // right, shrink to the cap's height instead: the transcript gets clipped by
    // the panel and the cap reports nothing to scroll, so the rest of the
    // conversation is unreachable. The cap must stay a plain scroll container
    // and the panel stack must be its single child.
    renderEventView('/observability/events/evt-1234567890000000000')
    const invocationHeading = await screen.findByText('Invocation inv-1 · review-bot')
    const cap = invocationHeading.closest('.scroll-cap')
    const panel = invocationHeading.closest('.panel')

    expect(cap?.firstElementChild).not.toBeNull()
    expect(cap?.firstElementChild).not.toBe(panel)
    expect(cap?.firstElementChild?.contains(panel)).toBe(true)
  })

  it('shows a back-to-top button only after scrolling', async () => {
    renderEventView('/observability/events/evt-1234567890000000000')
    await screen.findByText('Invocation inv-1 · review-bot')
    expect(screen.queryByRole('button', { name: /back to top/i })).not.toBeInTheDocument()

    const scrollTo = vi.fn()
    Object.defineProperty(window, 'scrollTo', {
      value: scrollTo,
      configurable: true,
      writable: true,
    })
    Object.defineProperty(window, 'scrollY', { value: 500, configurable: true })
    fireEvent.scroll(window)

    const button = await screen.findByRole('button', { name: /back to top/i })
    fireEvent.click(button)
    expect(scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' })
  })

  describe('plugin slots', () => {
    it('renders a plugin slot below the transcript with invocation details', async () => {
      renderEventView('/observability/events/evt-1234567890000000000')
      await waitFor(() =>
        expect(screen.getByText('invocationId=inv-1 agentName=review-bot')).toBeInTheDocument(),
      )
    })
  })
})
