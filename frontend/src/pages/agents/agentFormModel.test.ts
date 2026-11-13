import { describe, expect, it } from 'vitest'
import {
  agentDefaults,
  agentSchema,
  buildAgentRequest,
  LLM_PROVIDERS,
  llmProviderLabel,
  runtimeSchema,
  workingHoursFromForm,
  workingHoursToForm,
} from './agentFormModel'
import type { AgentWorkingHours } from '../../api/agents'

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

describe('workingHoursToForm', () => {
  it('reads no stored window as a disabled, empty form', () => {
    expect(workingHoursToForm(undefined)).toEqual({
      enabled: false,
      timezone: '',
      days: [],
      start: '',
      end: '',
    })
  })

  it('normalises a Sunday written as 7 into the Sunday tick', () => {
    const stored: AgentWorkingHours = {
      timezone: 'UTC',
      days: [7, 1],
      start: '09:00',
      end: '17:00',
    }
    expect(workingHoursToForm(stored).days).toEqual([0, 1])
  })
})

describe('workingHoursFromForm', () => {
  it('sends an empty window when the toggle is off — absent would mean "leave it alone"', () => {
    expect(
      workingHoursFromForm({
        enabled: false,
        timezone: 'Europe/Berlin',
        days: [1],
        start: '09:00',
        end: '17:00',
      }),
    ).toEqual({})
  })

  it('sends the window as written, days deduplicated and in order', () => {
    expect(
      workingHoursFromForm({
        enabled: true,
        timezone: '  Europe/Berlin ',
        days: [5, 1, 5],
        start: '09:00',
        end: '22:00',
      }),
    ).toEqual({ timezone: 'Europe/Berlin', days: [1, 5], start: '09:00', end: '22:00' })
  })

  it('leaves a blank timezone empty, which the hub reads as UTC', () => {
    expect(
      workingHoursFromForm({
        enabled: true,
        timezone: '   ',
        days: [],
        start: '09:00',
        end: '17:00',
      }),
    ).toEqual({ timezone: '', days: [], start: '09:00', end: '17:00' })
  })
})

describe('runtimeSchema working hours', () => {
  // A provider on the LLM: the schema requires one (issue #96), so a base without
  // it would be rejected for a reason that has nothing to do with the window.
  const base = {
    llm: { model: 'm', provider: 'ollama-cloud', maxTurns: 1 },
    replicas: 1,
  }

  it('rejects a window with no clock on it', () => {
    const result = runtimeSchema.safeParse({
      ...base,
      workingHours: { enabled: true, timezone: '', days: [], start: '', end: '' },
    })
    expect(result.success).toBe(false)
    expect(JSON.stringify(result.error?.issues)).toContain('HH:MM')
  })

  it('rejects a window whose start equals its end', () => {
    const result = runtimeSchema.safeParse({
      ...base,
      workingHours: { enabled: true, timezone: '', days: [], start: '09:00', end: '09:00' },
    })
    expect(result.success).toBe(false)
    expect(JSON.stringify(result.error?.issues)).toContain('leaves no window at all')
  })

  it('accepts a window that runs past midnight', () => {
    const result = runtimeSchema.safeParse({
      ...base,
      workingHours: {
        enabled: true,
        timezone: 'Asia/Tokyo',
        days: [3],
        start: '22:00',
        end: '06:00',
      },
    })
    expect(result.success).toBe(true)
  })

  it('leaves an untouched window alone', () => {
    const result = runtimeSchema.safeParse({
      ...base,
      workingHours: { enabled: false, timezone: '', days: [], start: '', end: '' },
    })
    expect(result.success).toBe(true)
  })
})

describe('OpenAI auth method', () => {
  const base = {
    ...agentDefaults,
    name: 'Agent',
    imageRef: { name: 'image' },
    persona: { mode: 'template' as const, id: 'persona' },
  }
  const openai = (extra: Record<string, unknown>) => ({
    ...base,
    llm: { ...base.llm, model: 'gpt-5', provider: 'openai' },
    ...extra,
  })

  it('sends ChatGPT sign-in without a key', () => {
    const req = buildAgentRequest(
      agentSchema.parse(openai({ openAIAuthMethod: 'ChatGPTSignIn', providerApiKey: 'sk-typed' })),
      { isEdit: false },
    )
    expect(req.openAI).toEqual({ authMethod: 'ChatGPTSignIn' })
  })

  it('sends the method with an API key', () => {
    const req = buildAgentRequest(
      agentSchema.parse(openai({ openAIAuthMethod: 'APIKey', providerApiKey: 'sk-1' })),
      { isEdit: false },
    )
    expect(req.openAI).toEqual({ authMethod: 'APIKey', apiKey: 'sk-1' })
  })

  it('caps replicas at 1 for ChatGPT sign-in', () => {
    const result = agentSchema.safeParse(openai({ openAIAuthMethod: 'ChatGPTSignIn', replicas: 2 }))
    expect(result.success).toBe(false)
    expect(JSON.stringify(result.error?.issues)).toContain('one task at a time')
    expect(agentSchema.safeParse(openai({ openAIAuthMethod: 'APIKey', replicas: 2 })).success).toBe(
      true,
    )
  })

  it('ignores the method for other providers', () => {
    const req = buildAgentRequest(
      agentSchema.parse({
        ...base,
        llm: { ...base.llm, model: 'm', provider: 'anthropic' },
        openAIAuthMethod: 'ChatGPTSignIn',
        providerApiKey: 'k',
      }),
      { isEdit: false },
    )
    expect(req.openAI).toBeUndefined()
    expect(req.anthropic).toEqual({ apiKey: 'k' })
  })

  it('runtime schema applies the same replicas cap', () => {
    const result = runtimeSchema.safeParse({
      llm: { model: 'gpt-5', provider: 'openai' },
      openAIAuthMethod: 'ChatGPTSignIn',
      replicas: 3,
      workingHours: { enabled: false, days: [] },
    })
    expect(result.success).toBe(false)
  })
})
