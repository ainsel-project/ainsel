import { describe, expect, it } from 'vitest'
import { agentDefaults, agentSchema, buildAgentRequest, llmProviderLabel } from './agentFormModel'
import { LLM_PROVIDER_REGISTRY } from './llmProviders'

function requestFor(provider: string, providerApiKey: string) {
  const values = agentSchema.parse({
    ...agentDefaults,
    name: 'Agent',
    imageRef: { name: 'image' },
    persona: { id: 'persona' },
    llm: { ...agentDefaults.llm, model: 'm', provider },
    providerApiKey,
  })
  return buildAgentRequest(values, { isEdit: true })
}

const PROVIDER_FIELDS = [
  'ollamaCloud',
  'openCode',
  'alibabaCloud',
  'openAI',
  'anthropic',
  'google',
  'customProvider',
] as const

describe('LLM_PROVIDER_REGISTRY', () => {
  it('lists the providers in the same order as the Go registry', () => {
    expect(LLM_PROVIDER_REGISTRY.map((p) => p.id)).toEqual([
      'ollama-cloud',
      'opencode',
      'alibaba-cloud',
      'openai',
      'anthropic',
      'google',
      'custom',
    ])
  })

  it('labels the new providers', () => {
    expect(llmProviderLabel('openai')).toBe('OpenAI')
    expect(llmProviderLabel('anthropic')).toBe('Anthropic')
    expect(llmProviderLabel('google')).toBe('Google Gemini')
  })
})

describe('buildAgentRequest provider blocks', () => {
  it('sends only the selected provider block with the entered key', () => {
    const body = requestFor('anthropic', 'k')
    expect(body.anthropic).toEqual({ apiKey: 'k' })
    for (const field of PROVIDER_FIELDS.filter((f) => f !== 'anthropic')) {
      expect(body[field]).toBeUndefined()
    }
  })

  it('sends no provider block when the key is blank, so the stored key is kept', () => {
    const body = requestFor('google', '')
    for (const field of PROVIDER_FIELDS) {
      expect(body[field]).toBeUndefined()
    }
  })
})
