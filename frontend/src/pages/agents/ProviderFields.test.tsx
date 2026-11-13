import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import type { AgentResponse } from '../../api/agents'
import { ContextWindowField, ProviderFields, TemperatureField } from './ProviderFields'
import { hasStoredKey } from './llmProviders'

type Shape = {
  provider: string
  url: string
  key: string
  authMethod: 'APIKey' | 'ChatGPTSignIn'
  temperature?: number
  contextWindow?: number
}

function Harness({
  initial,
  mode,
  storedKey,
  initialMethod,
}: {
  initial: string
  mode: 'create' | 'edit'
  storedKey?: boolean
  initialMethod?: 'APIKey' | 'ChatGPTSignIn'
}) {
  const { register, setValue, watch } = useForm<Shape>({
    defaultValues: { provider: initial, url: '', key: '', authMethod: initialMethod ?? 'APIKey' },
  })
  const provider = watch('provider')
  const authMethod = watch('authMethod')
  return (
    <>
      <ProviderFields
        provider={provider}
        onProviderChange={(v) => setValue('provider', v)}
        urlRegistration={register('url')}
        keyRegistration={register('key')}
        mode={mode}
        storedKey={storedKey}
        authMethod={authMethod}
        onAuthMethodChange={(v) => setValue('authMethod', v)}
      />
      <TemperatureField provider={provider} registration={register('temperature')} />
      <ContextWindowField provider={provider} registration={register('contextWindow')} />
    </>
  )
}

describe('ProviderFields', () => {
  it('offers no "None" provider and asks for one while none is chosen', () => {
    render(<Harness initial="" mode="edit" />)
    const select = screen.getByLabelText(/^provider$/i)
    const labels = Array.from((select as HTMLSelectElement).options).map((o) => o.text)
    expect(labels).not.toContain('None')
    expect(labels[0]).toBe('Select a provider…')
  })

  it('does not offer the empty choice once a provider is set', () => {
    render(<Harness initial="openai" mode="edit" />)
    const labels = Array.from(
      (screen.getByLabelText(/^provider$/i) as HTMLSelectElement).options,
    ).map((o) => o.text)
    expect(labels).not.toContain('Select a provider…')
  })

  it('shows no key placeholder when creating', () => {
    render(<Harness initial="openai" mode="create" />)
    expect(screen.getByLabelText('OpenAI API Key')).not.toHaveAttribute('placeholder')
  })

  it('tells an editor whether a key is stored', () => {
    const { unmount } = render(<Harness initial="openai" mode="edit" storedKey />)
    expect(screen.getByLabelText('OpenAI API Key')).toHaveAttribute(
      'placeholder',
      'A key is stored — leave blank to keep it',
    )
    unmount()
    render(<Harness initial="openai" mode="edit" storedKey={false} />)
    expect(screen.getByLabelText('OpenAI API Key')).toHaveAttribute(
      'placeholder',
      'No key stored yet',
    )
  })

  it('asks for a base URL only for the custom provider', async () => {
    const user = userEvent.setup()
    render(<Harness initial="ollama-cloud" mode="edit" />)
    expect(screen.queryByLabelText(/provider base url/i)).not.toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'custom')
    expect(screen.getByLabelText(/provider base url/i)).toBeInTheDocument()
    expect(screen.getByLabelText('API Key')).toHaveAttribute(
      'placeholder',
      'Leave blank to keep any stored key',
    )
  })

  it("replaces temperature with a note for Pi's built-in providers", async () => {
    const user = userEvent.setup()
    render(<Harness initial="ollama-cloud" mode="edit" />)
    expect(screen.getByLabelText('Temperature')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'anthropic')
    expect(screen.queryByLabelText('Temperature')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        "Temperature is not applied for Anthropic: it runs on Pi's built-in provider.",
      ),
    ).toBeInTheDocument()
  })

  it("explains that built-in providers take the context window from Pi's catalog", async () => {
    const user = userEvent.setup()
    render(<Harness initial="ollama-cloud" mode="edit" />)
    expect(screen.getByLabelText('Context Window')).toHaveAttribute(
      'placeholder',
      '131072 (128k default)',
    )
    await user.selectOptions(screen.getByLabelText(/^provider$/i), 'google')
    expect(screen.getByLabelText('Context Window')).toHaveAttribute(
      'placeholder',
      "From Pi's model catalog",
    )
    expect(
      screen.getByText(
        "Leave blank to use the model's real limit from Pi's catalog. Set it only to lower the limit.",
      ),
    ).toBeInTheDocument()
  })
})

describe('hasStoredKey', () => {
  it('reads the provider block from the stored agent', () => {
    const agent = { openAI: {} } as AgentResponse
    expect(hasStoredKey(agent, 'openai')).toBe(true)
    expect(hasStoredKey(agent, 'anthropic')).toBe(false)
    expect(hasStoredKey(agent, 'unknown')).toBe(false)
  })
})

describe('ProviderFields auth method', () => {
  it('shows only the auth method choice for OpenAI', () => {
    const { unmount } = render(<Harness initial="openai" mode="create" />)
    const select = screen.getByLabelText(/how to authenticate/i) as HTMLSelectElement
    expect(Array.from(select.options).map((o) => o.text)).toEqual([
      'API key',
      'Sign in with ChatGPT',
    ])
    unmount()
    render(<Harness initial="anthropic" mode="create" />)
    expect(screen.queryByLabelText(/how to authenticate/i)).not.toBeInTheDocument()
  })

  it('hides the key field in sign-in mode and explains the next step', async () => {
    render(<Harness initial="openai" mode="create" />)
    await userEvent.selectOptions(screen.getByLabelText(/how to authenticate/i), 'ChatGPTSignIn')
    expect(screen.queryByLabelText('OpenAI API Key')).not.toBeInTheDocument()
    expect(screen.getByText(/pi-coding-agent 1\.5\.2 or later/)).toBeInTheDocument()
    expect(screen.getByText(/sign in from the agent page/i)).toBeInTheDocument()
  })

  it('switching back to API key shows the key field with the stored-key hint', async () => {
    render(<Harness initial="openai" mode="edit" storedKey initialMethod="ChatGPTSignIn" />)
    expect(screen.queryByLabelText('OpenAI API Key')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText(/how to authenticate/i), 'APIKey')
    expect(screen.getByLabelText('OpenAI API Key')).toHaveAttribute(
      'placeholder',
      'A key is stored — leave blank to keep it',
    )
  })
})

describe('hasStoredKey with keyStored', () => {
  it('trusts keyStored when the hub reports it', () => {
    expect(
      hasStoredKey(
        { openAI: { authMethod: 'ChatGPTSignIn', keyStored: false } } as AgentResponse,
        'openai',
      ),
    ).toBe(false)
    expect(hasStoredKey({ openAI: { keyStored: true } } as AgentResponse, 'openai')).toBe(true)
    expect(hasStoredKey({ openAI: {} } as AgentResponse, 'openai')).toBe(true)
  })
})
