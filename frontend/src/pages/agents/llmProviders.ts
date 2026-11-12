import type { AgentRequest } from '../../api/agents'

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
}

/**
 * Every LLM provider the console offers, in display order. Mirrors the Go
 * registry in shared/api/llmprovider — same ids, same order — which
 * TestFrontendRegistryMatchesGo checks.
 */
export const LLM_PROVIDER_REGISTRY: readonly LlmProvider[] = [
  { id: 'ollama-cloud', label: 'Ollama Cloud', apiKeyLabel: 'Ollama Cloud API Key', requestField: 'ollamaCloud', needsUrl: false },
  { id: 'opencode', label: 'OpenCode', apiKeyLabel: 'OpenCode API Key', requestField: 'openCode', needsUrl: false },
  { id: 'alibaba-cloud', label: 'Alibaba Token Plan', apiKeyLabel: 'Alibaba Token Plan API Key', requestField: 'alibabaCloud', needsUrl: false },
  { id: 'openai', label: 'OpenAI', apiKeyLabel: 'OpenAI API Key', requestField: 'openAI', needsUrl: false },
  { id: 'anthropic', label: 'Anthropic', apiKeyLabel: 'Anthropic API Key', requestField: 'anthropic', needsUrl: false },
  { id: 'google', label: 'Google Gemini', apiKeyLabel: 'Gemini API Key', requestField: 'google', needsUrl: false },
  { id: 'custom', label: 'Custom', apiKeyLabel: 'API Key', requestField: 'customProvider', needsUrl: true },
]

export function findLlmProvider(id: string | undefined): LlmProvider | undefined {
  return LLM_PROVIDER_REGISTRY.find((p) => p.id === id)
}
