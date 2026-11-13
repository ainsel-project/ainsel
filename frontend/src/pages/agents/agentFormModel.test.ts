import { describe, expect, it } from 'vitest'
import {
  agentDefaults,
  agentSchema,
  buildAgentRequest,
  LLM_PROVIDERS,
  llmProviderLabel,
} from './agentFormModel'

describe('llmProviderLabel', () => {
  it('maps every form option id to its label', () => {
    for (const { value, label } of LLM_PROVIDERS) {
      expect(llmProviderLabel(value)).toBe(label)
    }
  })

  it("labels an agent stored without a provider 'None'", () => {
    expect(llmProviderLabel(undefined)).toBe('None')
    expect(llmProviderLabel('')).toBe('None')
  })

  it('falls through unknown provider ids unchanged', () => {
    expect(llmProviderLabel('future-provider')).toBe('future-provider')
  })
})

describe('agentSchema', () => {
  it('requires a custom provider URL when provider is custom', () => {
    const result = agentSchema.safeParse({
      name: 'a',
      imageRef: { name: 'img' },
      llm: { model: 'm', provider: 'custom' },
      persona: { mode: 'template', id: 'p' },
      replicas: 1,
    })
    expect(result.success).toBe(false)
    expect(JSON.stringify(result.error?.issues)).toContain('customProviderUrl')
  })
})

describe('provider rules', () => {
  const base = {
    ...agentDefaults,
    name: 'Agent',
    imageRef: { name: 'image' },
    persona: { mode: 'template', id: 'persona' },
  }

  it('requires a provider', () => {
    const result = agentSchema.safeParse({
      ...base,
      llm: { ...base.llm, model: 'm', provider: '' },
    })
    expect(result.success).toBe(false)
  })

  it("omits the temperature for Pi's built-in providers and keeps it otherwise", () => {
    const build = (provider: string) =>
      buildAgentRequest(
        agentSchema.parse({
          ...base,
          llm: { ...base.llm, model: 'm', provider, temperature: 0.4 },
        }),
        { isEdit: false },
      )
    expect(build('openai').llm).not.toHaveProperty('temperature')
    expect(build('ollama-cloud').llm?.temperature).toBe(0.4)
  })
})
