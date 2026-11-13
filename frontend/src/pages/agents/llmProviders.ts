import type { AgentRequest, AgentResponse, OpenAIAuthMethod } from '../../api/agents'

/** The agent write-body field that carries a provider's credential block. */
export type ProviderRequestField = keyof Pick<
  AgentRequest,
  'ollamaCloud' | 'openCode' | 'alibabaCloud' | 'openAI' | 'anthropic' | 'google' | 'customProvider'
>

export type LlmProvider = {
  /** The spec.llm.provider value. */
  id: string
  label: string
  apiKeyLabel: string
  requestField: ProviderRequestField
  /** The provider has no fixed endpoint; the form asks for its base URL. */
  needsUrl: boolean
  /** Runs on one of Pi's built-in providers: the temperature is not applied
   *  and the context window comes from Pi's model catalog. */
  piBuiltIn: boolean
  /** More than one way to authenticate: the form asks which. Absent means
   *  an API key only. */
  authMethods?: readonly { value: OpenAIAuthMethod; label: string }[]
}

/**
 * Every LLM provider the console offers, in display order. Mirrors the Go
 * registry in shared/api/llmprovider — same ids, same order — which
 * TestFrontendRegistryMatchesGo checks.
 */
export const LLM_PROVIDER_REGISTRY: readonly LlmProvider[] = [
  {
    id: 'ollama-cloud',
    label: 'Ollama Cloud',
    apiKeyLabel: 'Ollama Cloud API Key',
    requestField: 'ollamaCloud',
    needsUrl: false,
    piBuiltIn: false,
  },
  {
    id: 'opencode',
    label: 'OpenCode',
    apiKeyLabel: 'OpenCode API Key',
    requestField: 'openCode',
    needsUrl: false,
    piBuiltIn: false,
  },
  {
    id: 'alibaba-cloud',
    label: 'Alibaba Token Plan',
    apiKeyLabel: 'Alibaba Token Plan API Key',
    requestField: 'alibabaCloud',
    needsUrl: false,
    piBuiltIn: false,
  },
  {
    id: 'openai',
    label: 'OpenAI',
    apiKeyLabel: 'OpenAI API Key',
    requestField: 'openAI',
    needsUrl: false,
    piBuiltIn: true,
    authMethods: [
      { value: 'APIKey', label: 'API key' },
      { value: 'ChatGPTSignIn', label: 'Sign in with ChatGPT' },
    ],
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    apiKeyLabel: 'Anthropic API Key',
    requestField: 'anthropic',
    needsUrl: false,
    piBuiltIn: true,
  },
  {
    id: 'google',
    label: 'Google Gemini',
    apiKeyLabel: 'Gemini API Key',
    requestField: 'google',
    needsUrl: false,
    piBuiltIn: true,
  },
  {
    id: 'custom',
    label: 'Custom',
    apiKeyLabel: 'API Key',
    requestField: 'customProvider',
    needsUrl: true,
    piBuiltIn: false,
  },
]

export function findLlmProvider(id: string | undefined): LlmProvider | undefined {
  return LLM_PROVIDER_REGISTRY.find((p) => p.id === id)
}

/**
 * Whether the stored agent holds a credential block for the provider. The hub
 * never returns the key itself, only that the block exists. Not meaningful for
 * custom: its block carries the URL and exists without a key.
 */
export function hasStoredKey(agent: AgentResponse, providerId: string): boolean {
  const p = findLlmProvider(providerId)
  if (p === undefined) return false
  const block = agent[p.requestField]
  if (block == null) return false
  // Hubs that report keyStored say it outright; older ones only report that
  // the block exists, which then meant a key.
  return 'keyStored' in block ? block.keyStored === true : true
}

/** The stored OpenAI auth method; API key when the agent has no openAI block. */
export function storedAuthMethod(agent: AgentResponse): OpenAIAuthMethod {
  return agent.openAI?.authMethod ?? 'APIKey'
}
