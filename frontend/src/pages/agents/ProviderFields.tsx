import type { UseFormRegisterReturn } from 'react-hook-form'
import { Field } from '../../primitives/Field'
import { Input } from '../../primitives/Input'
import { Select } from '../../primitives/Select'
import { LLM_PROVIDERS } from './agentFormModel'
import { findLlmProvider } from './llmProviders'
import type { OpenAIAuthMethod } from '../../api/agents'

type ProviderFieldsProps = {
  provider: string | undefined
  onProviderChange: (id: string) => void
  urlRegistration: UseFormRegisterReturn
  keyRegistration: UseFormRegisterReturn
  providerError?: string
  urlError?: string
  mode: 'create' | 'edit'
  /** Edit: a credential block is stored for the selected provider. */
  storedKey?: boolean
  /** The chosen auth method, for providers that offer more than one. */
  authMethod?: OpenAIAuthMethod
  onAuthMethodChange?: (method: OpenAIAuthMethod) => void
}

function keyPlaceholder(
  mode: 'create' | 'edit',
  needsUrl: boolean,
  storedKey: boolean | undefined,
): string | undefined {
  if (mode === 'create') return undefined
  // The hub reports a custom provider's URL but not whether it has a key.
  if (needsUrl) return 'Leave blank to keep any stored key'
  return storedKey ? 'A key is stored — leave blank to keep it' : 'No key stored yet'
}

/**
 * The provider choice and the credential fields that choice needs, shared by
 * the creation wizard and the runtime settings. Only the selected provider's
 * fields render; there is no "no provider" option, because the operator
 * refuses an agent without one.
 */
export function ProviderFields({
  provider,
  onProviderChange,
  urlRegistration,
  keyRegistration,
  providerError,
  urlError,
  mode,
  storedKey,
  authMethod,
  onAuthMethodChange,
}: ProviderFieldsProps) {
  const p = findLlmProvider(provider)
  const signIn = p?.authMethods !== undefined && authMethod === 'ChatGPTSignIn'
  return (
    <>
      <Field label="Provider" htmlFor="llm.provider" error={providerError}>
        <Select
          id="llm.provider"
          value={provider ?? ''}
          onChange={onProviderChange}
          options={LLM_PROVIDERS}
          emptyLabel={provider ? undefined : 'Select a provider…'}
        />
      </Field>
      {p?.needsUrl ? (
        <Field label="Provider Base URL" htmlFor="customProviderUrl" error={urlError}>
          <Input
            id="customProviderUrl"
            {...urlRegistration}
            placeholder="https://api.example.com/v1"
          />
        </Field>
      ) : null}
      {p?.authMethods ? (
        <Field label="How to authenticate" htmlFor="openAIAuthMethod">
          <Select
            id="openAIAuthMethod"
            value={authMethod ?? 'APIKey'}
            onChange={(v) => onAuthMethodChange?.(v as OpenAIAuthMethod)}
            options={p.authMethods}
          />
        </Field>
      ) : null}
      {signIn ? (
        <div className="field">
          <div className="field-label">ChatGPT subscription</div>
          <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
            {mode === 'create'
              ? 'Create the agent, then sign in from the agent page. '
              : 'Save, then sign in from the agent page. '}
            {
              "Runs one task at a time. Needs a Pi image with pi-coding-agent 1.5.2 or later. Uses OpenAI's official Sign in with ChatGPT."
            }
          </p>
        </div>
      ) : null}
      {p && !signIn ? (
        <Field label={p.apiKeyLabel} htmlFor="providerApiKey">
          <Input
            id="providerApiKey"
            type="password"
            autoComplete="off"
            placeholder={keyPlaceholder(mode, p.needsUrl, storedKey)}
            {...keyRegistration}
          />
        </Field>
      ) : null}
    </>
  )
}

/**
 * The temperature input, or for Pi's built-in providers a note that it is not
 * applied (they run on Pi's own provider, which the temperature never reaches).
 */
export function TemperatureField({
  provider,
  registration,
}: {
  provider: string | undefined
  registration: UseFormRegisterReturn
}) {
  const p = findLlmProvider(provider)
  if (p?.piBuiltIn) {
    // No input to label: same field layout, plain heading.
    return (
      <div className="field">
        <div className="field-label">Temperature</div>
        <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
          {`Temperature is not applied for ${p.label}: it runs on Pi's built-in provider.`}
        </p>
      </div>
    )
  }
  return (
    <Field label="Temperature" htmlFor="llm.temperature">
      <Input id="llm.temperature" type="number" step={0.1} min={0} max={2} {...registration} />
    </Field>
  )
}

/**
 * The context window override. OpenAI-compatible endpoints get the uniform
 * 128k default unless overridden; Pi's built-in providers know each model's
 * real limit, so blank means "Pi's catalog value".
 */
export function ContextWindowField({
  provider,
  registration,
  error,
}: {
  provider: string | undefined
  registration: UseFormRegisterReturn
  error?: string
}) {
  const builtIn = findLlmProvider(provider)?.piBuiltIn ?? false
  return (
    <Field
      label="Context Window"
      htmlFor="llm.contextWindow"
      error={error}
      hint={
        builtIn
          ? "Leave blank to use the model's real limit from Pi's catalog. Set it only to lower the limit."
          : "Tokens declared in pi's models.json. Defaults to 131072 (128k) — must match what the LLM endpoint actually enforces, or context compaction fires too late; raise it only if the endpoint accepts more."
      }
    >
      <Input
        id="llm.contextWindow"
        type="number"
        min={1024}
        placeholder={builtIn ? "From Pi's model catalog" : '131072 (128k default)'}
        {...registration}
      />
    </Field>
  )
}
