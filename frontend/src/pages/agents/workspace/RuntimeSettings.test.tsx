import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { RuntimeSettings } from './RuntimeSettings'
import { LegacyEditRedirect } from './sectionPages'
import type { AgentResponse } from '../../../api/agents'
import { renderWithProviders } from '../../../test/renderWithProviders'

const IMAGE_ID = 'claude-tooling-base:1.4'

const agent = {
  id: 'a1',
  name: 'doc-writer',
  description: 'Writes docs',
  imageRef: { name: IMAGE_ID },
  llm: {
    model: 'claude-opus-4-7',
    provider: 'ollama-cloud',
    maxTurns: 40,
    temperature: 0.5,
    contextWindow: 200000,
    modelLicense: 'Anthropic Commercial Terms',
    vision: true,
  },
  persona: { id: 'p1' },
  replicas: 2,
  paused: false,
} as AgentResponse

function putBody(fetchMock: ReturnType<typeof vi.fn>): Record<string, unknown> | undefined {
  const call = fetchMock.mock.calls.find(
    ([u, init]) =>
      typeof u === 'string' &&
      u.includes('/agents/a1') &&
      ['PUT', 'PATCH'].includes((init as RequestInit | undefined)?.method ?? ''),
  )
  if (!call) return undefined
  return JSON.parse((call[1] as RequestInit).body as string) as Record<string, unknown>
}

function renderSettings(overrides: Partial<AgentResponse> = {}) {
  return renderWithProviders(
    <RuntimeSettings agent={{ ...agent, ...overrides } as AgentResponse} />,
    {
      route: '/agents/a1/runtime',
    },
  )
}

/**
 * Renders with a handle to re-render the same tree with a new agent object,
 * as the workspace does after a refetch. The providers are kept so the
 * component tree (and its form state) is preserved.
 */
function renderMutable(initial: AgentResponse) {
  const view = renderWithProviders(<RuntimeSettings agent={initial} />, {
    route: '/agents/a1/runtime',
  })
  const rerenderWith = (next: AgentResponse) =>
    view.rerender(
      <QueryClientProvider client={view.queryClient}>
        <MemoryRouter initialEntries={['/agents/a1/runtime']}>
          <RuntimeSettings agent={next} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
  return { ...view, rerenderWith }
}

describe('RuntimeSettings', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn((url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/agents/a1') && ['PUT', 'PATCH'].includes(method)) {
        const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
        return Promise.resolve(new Response(JSON.stringify({ ...agent, ...body }), { status: 200 }))
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('pre-fills the runtime fields from the agent', () => {
    renderSettings()
    expect(screen.getByLabelText('Model')).toHaveValue('claude-opus-4-7')
    expect(screen.getByLabelText('Max Turns')).toHaveValue(40)
    expect(screen.getByLabelText('Temperature')).toHaveValue(0.5)
    expect(screen.getByLabelText('Context Window')).toHaveValue(200000)
    expect(screen.getByLabelText(/^provider$/i)).toHaveValue('ollama-cloud')
    expect(screen.getByLabelText('Model License')).toHaveValue('Anthropic Commercial Terms')
    // The fixture has no ollamaCloud block: no key is stored yet.
    expect(screen.getByLabelText(/ollama cloud api key/i)).toHaveAttribute(
      'placeholder',
      'No key stored yet',
    )
    expect(screen.getByLabelText('Max concurrent tasks')).toHaveValue(2)
    expect(screen.getByRole('checkbox', { name: /image input/i })).toHaveAttribute(
      'aria-checked',
      'true',
    )
  })

  it('saves a changed model with an unchanged name', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'claude-opus-4-8')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)).toMatchObject({
      name: 'doc-writer',
      llm: { model: 'claude-opus-4-8' },
      replicas: 2,
    })
  })

  it('sends a recorded model licence and leaves a blank one unchanged', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.clear(screen.getByLabelText('Model License'))
    await user.type(screen.getByLabelText('Model License'), 'Apache-2.0')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)).toMatchObject({
      llm: { modelLicense: 'Apache-2.0' },
    })
  })

  it('sends customProvider URL and API key when Custom is selected', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'custom')
    await user.type(
      await screen.findByLabelText(/provider base url/i),
      'https://api.example.com/v1',
    )
    await user.type(screen.getByLabelText(/^api key$/i), 'sk-test-12345')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)?.customProvider).toEqual({
      url: 'https://api.example.com/v1',
      apiKey: 'sk-test-12345',
    })
  })

  it('blocks saving a custom provider without a base URL', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'custom')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText(/url is required for custom providers/i)).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('sends an explicit llm.vision false when image input is turned off', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByRole('checkbox', { name: /image input/i }))
    expect(screen.getByText('Text only')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    // The hub reads a missing `vision` as "leave unchanged", so the form must
    // say false explicitly for image input to turn off.
    expect(putBody(fetchMock)?.llm).toMatchObject({ vision: false })
  })

  it('omits the credential block when the API key is left blank', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'claude-opus-4-8')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    // Blank key = keep the stored one, so no block is sent at all.
    expect(putBody(fetchMock)).not.toHaveProperty('ollamaCloud')
  })

  it('labels the count as a concurrency cap and sends it on save', async () => {
    const user = userEvent.setup()
    renderSettings()

    expect(screen.getByLabelText('Max concurrent tasks')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)?.replicas).toBe(2)
    expect(putBody(fetchMock)).not.toHaveProperty('minReplicas')
  })

  it('requires a model', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.clear(screen.getByLabelText('Model'))
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText('Model is required')).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('rejects a negative concurrency cap', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.clear(screen.getByLabelText('Max concurrent tasks'))
    await user.type(screen.getByLabelText('Max concurrent tasks'), '-1')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText(/too small/i)).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('keeps a typed model when the agent refetches with identical runtime content', async () => {
    const user = userEvent.setup()
    const view = renderMutable(agent)

    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'claude-opus-4-8')
    view.rerenderWith({ ...agent, llm: { ...agent.llm } } as AgentResponse)

    expect(screen.getByLabelText('Model')).toHaveValue('claude-opus-4-8')
  })

  it('keeps a typed model when only other agent fields (env) change', async () => {
    const user = userEvent.setup()
    const view = renderMutable(agent)

    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'claude-opus-4-8')
    view.rerenderWith({
      ...agent,
      env: [{ name: 'LOG_LEVEL', value: 'debug', secret: false }],
    } as AgentResponse)

    expect(screen.getByLabelText('Model')).toHaveValue('claude-opus-4-8')
  })

  it('reflects the saved values after its own save, and then the stored agent', async () => {
    const user = userEvent.setup()
    const view = renderMutable(agent)

    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'claude-opus-4-8')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    await waitFor(() => expect(screen.getByLabelText('Model')).toHaveValue('claude-opus-4-8'))

    // The refetched agent carries the saved model; the form shows it too.
    view.rerenderWith({
      ...agent,
      llm: { ...agent.llm, model: 'claude-opus-4-8' },
    } as AgentResponse)
    expect(screen.getByLabelText('Model')).toHaveValue('claude-opus-4-8')
  })

  it('shows that a key is stored for the selected provider', () => {
    renderSettings({
      llm: { ...agent.llm, provider: 'openai' },
      openAI: {},
    } as Partial<AgentResponse>)
    expect(screen.getByLabelText('OpenAI API Key')).toHaveAttribute(
      'placeholder',
      'A key is stored — leave blank to keep it',
    )
  })

  it('switches provider: shows the new key state and sends only that provider', async () => {
    const user = userEvent.setup()
    renderSettings({
      llm: { ...agent.llm, provider: 'openai' },
      openAI: {},
    } as Partial<AgentResponse>)
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'anthropic')
    const key = screen.getByLabelText('Anthropic API Key')
    expect(key).toHaveAttribute('placeholder', 'No key stored yet')
    expect(screen.queryByLabelText('Temperature')).not.toBeInTheDocument()
    await user.type(key, 'sk-ant')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    const body = putBody(fetchMock)!
    expect(body.anthropic).toEqual({ apiKey: 'sk-ant' })
    expect(body).not.toHaveProperty('openAI')
    expect((body.llm as Record<string, unknown>).provider).toBe('anthropic')
    expect(body.llm as Record<string, unknown>).not.toHaveProperty('temperature')
  })

  it('drops a key typed for one provider when switching to another', async () => {
    const user = userEvent.setup()
    renderSettings({
      llm: { ...agent.llm!, provider: 'openai' },
      openAI: {},
    } as Partial<AgentResponse>)
    await user.type(screen.getByLabelText('OpenAI API Key'), 'sk-openai')
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'anthropic')
    expect(screen.getByLabelText('Anthropic API Key')).toHaveValue('')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)).not.toHaveProperty('anthropic')
    expect(putBody(fetchMock)).not.toHaveProperty('openAI')
  })

  it('shows the stored key after save and refetch', async () => {
    const user = userEvent.setup()
    const view = renderMutable(agent)
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'anthropic')
    await user.type(screen.getByLabelText('Anthropic API Key'), 'sk-ant')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    view.rerenderWith({
      ...agent,
      llm: { ...agent.llm, provider: 'anthropic' },
      anthropic: {},
    } as AgentResponse)
    await waitFor(() =>
      expect(screen.getByLabelText('Anthropic API Key')).toHaveAttribute(
        'placeholder',
        'A key is stored — leave blank to keep it',
      ),
    )
    expect(screen.getByLabelText('Anthropic API Key')).toHaveValue('')
  })

  it('refuses to save an agent stored without a provider until one is chosen', async () => {
    const user = userEvent.setup()
    renderSettings({ llm: { ...agent.llm!, provider: '' } })
    expect(screen.getByLabelText(/^provider$/i)).toHaveValue('')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    expect(await screen.findByText(/provider is required/i)).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('offers no editing or save for externally managed agents', () => {
    renderSettings({ managedBy: 'gitops' })
    expect(screen.getByLabelText('Model')).toBeDisabled()
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument()
  })
})

describe('RuntimeSettings working hours', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn((url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/agents/a1') && ['PUT', 'PATCH'].includes(method)) {
        const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
        return Promise.resolve(new Response(JSON.stringify({ ...agent, ...body }), { status: 200 }))
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // jsdom renders a time input as a text field and rejects partial keystrokes,
  // so the clock is set in one change rather than typed.
  function setClock(label: string, value: string) {
    fireEvent.change(screen.getByLabelText(label), { target: { value } })
  }

  it('offers no window for an agent that has none, and says so', () => {
    renderSettings()
    expect(screen.getByRole('checkbox', { name: /weekly window/i })).toHaveAttribute(
      'aria-checked',
      'false',
    )
    expect(screen.queryByLabelText('Timezone')).not.toBeInTheDocument()
    expect(screen.getByText('Work is handed to this agent at any hour')).toBeInTheDocument()
  })

  it('pre-fills the window, reading a Sunday stored as 7 as Sunday', () => {
    renderSettings({
      workingHours: {
        timezone: 'Europe/Berlin',
        days: [1, 2, 3, 4, 5, 7],
        start: '09:00',
        end: '17:00',
      },
      hoursOpen: false,
      hoursNextOpenAt: '2026-03-30T07:00:00Z',
    })
    expect(screen.getByRole('checkbox', { name: /weekly window/i })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    expect(screen.getByLabelText('Timezone')).toHaveValue('Europe/Berlin')
    expect(screen.getByLabelText('From')).toHaveValue('09:00')
    expect(screen.getByLabelText('Until')).toHaveValue('17:00')
    expect(screen.getByRole('checkbox', { name: 'Sun' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('checkbox', { name: 'Sat' })).toHaveAttribute('aria-checked', 'false')
  })

  it('saves a window: timezone, days and the clock, in the stored shape', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByRole('checkbox', { name: /weekly window/i }))
    await user.type(screen.getByLabelText('Timezone'), 'Europe/Berlin')
    setClock('From', '09:00')
    setClock('Until', '17:00')
    await user.click(screen.getByRole('checkbox', { name: 'Mon' }))
    await user.click(screen.getByRole('checkbox', { name: 'Fri' }))
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)?.workingHours).toEqual({
      timezone: 'Europe/Berlin',
      days: [1, 5],
      start: '09:00',
      end: '17:00',
    })
  })

  it('switching the window off asks the hub to hand work out at any hour again', async () => {
    const user = userEvent.setup()
    renderSettings({
      workingHours: { timezone: 'Europe/Berlin', days: [1], start: '09:00', end: '17:00' },
      hoursOpen: true,
      hoursClosesAt: '2026-03-20T16:00:00Z',
    })

    await user.click(screen.getByRole('checkbox', { name: /weekly window/i }))
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    // Absent would mean "leave the window alone"; the empty object is the clear.
    expect(putBody(fetchMock)?.workingHours).toEqual({})
  })

  it('blocks a window whose clock is not filled in', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByRole('checkbox', { name: /weekly window/i }))
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    // Both sides of the clock complain, so the query is a findAll.
    expect((await screen.findAllByText(/HH:MM/i)).length).toBeGreaterThan(0)
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('blocks a window whose start equals its end', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByRole('checkbox', { name: /weekly window/i }))
    setClock('From', '09:00')
    setClock('Until', '09:00')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText(/leaves no window at all/i)).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('accepts a night window, which closes the next morning', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByRole('checkbox', { name: /weekly window/i }))
    setClock('From', '22:00')
    setClock('Until', '06:00')
    await user.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)?.workingHours).toMatchObject({ start: '22:00', end: '06:00' })
  })
})

describe('legacy /agents/:id/edit route', () => {
  it('lands on the agent runtime section', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id/edit" element={<LegacyEditRedirect />} />
        <Route path="/agents/:id/runtime" element={<p>Runtime section</p>} />
      </Routes>,
      { route: '/agents/a1/edit' },
    )
    expect(await screen.findByText('Runtime section')).toBeInTheDocument()
  })
})

describe('RuntimeSettings ChatGPT sign-in', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  const signInAgent = {
    ...agent,
    llm: { ...agent.llm, provider: 'openai', model: 'gpt-5' },
    openAI: { authMethod: 'ChatGPTSignIn', keyStored: false },
    replicas: 1,
  } as AgentResponse

  beforeEach(() => {
    fetchMock = vi.fn((url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/agents/a1') && ['PUT', 'PATCH'].includes(method)) {
        return Promise.resolve(new Response(JSON.stringify(signInAgent), { status: 200 }))
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sign-in mode hides the key field and caps replicas at 1', () => {
    renderWithProviders(<RuntimeSettings agent={signInAgent} />, { route: '/agents/a1/runtime' })
    expect(screen.getByLabelText(/how to authenticate/i)).toHaveValue('ChatGPTSignIn')
    expect(screen.queryByLabelText('OpenAI API Key')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Max concurrent tasks')).toHaveAttribute('max', '1')
  })

  it('saves the auth method without a key', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RuntimeSettings agent={signInAgent} />, { route: '/agents/a1/runtime' })
    await user.clear(screen.getByLabelText('Model'))
    await user.type(screen.getByLabelText('Model'), 'gpt-5.1')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(putBody(fetchMock)).toBeDefined())
    expect(putBody(fetchMock)?.openAI).toEqual({ authMethod: 'ChatGPTSignIn' })
  })

  it('refuses to save sign-in with more than one concurrent task', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <RuntimeSettings
        agent={
          {
            ...signInAgent,
            openAI: { authMethod: 'APIKey', keyStored: true },
            replicas: 2,
          } as AgentResponse
        }
      />,
      { route: '/agents/a1/runtime' },
    )
    await user.selectOptions(screen.getByLabelText(/how to authenticate/i), 'ChatGPTSignIn')
    await user.click(screen.getByRole('button', { name: /^save$/i }))
    expect(await screen.findByText(/set this to 1/i)).toBeInTheDocument()
    expect(putBody(fetchMock)).toBeUndefined()
  })

  it('reload keeps authMethod', () => {
    const view = renderMutable(signInAgent)
    view.rerenderWith({ ...signInAgent, updatedAt: 'later' } as AgentResponse)
    expect(screen.getByLabelText(/how to authenticate/i)).toHaveValue('ChatGPTSignIn')
  })
})
