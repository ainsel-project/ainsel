import { z } from 'zod'
import type { AgentRequest, AgentWorkingHours, OpenAIAuthMethod } from '../../api/agents'
import { findLlmProvider, LLM_PROVIDER_REGISTRY, type ProviderRequestField } from './llmProviders'

export const LLM_PROVIDERS: ReadonlyArray<{ value: string; label: string }> =
  LLM_PROVIDER_REGISTRY.map((p) => ({ value: p.id, label: p.label }))

/**
 * Human-readable label for a stored LLM provider id. Derived from the same
 * LLM_PROVIDERS options the forms offer, so display can never drift from the
 * form vocabulary. An agent stored without a provider (the forms no longer
 * offer one) reads 'None'.
 * Unknown ids fall through to the raw value so a provider added later still
 * renders honestly until this list learns it.
 */
export function llmProviderLabel(provider: string | undefined): string {
  if (!provider) return 'None'
  return LLM_PROVIDERS.find((p) => p.value === provider)?.label ?? provider
}

export const API_KEY_LABELS: Record<string, string> = Object.fromEntries(
  LLM_PROVIDER_REGISTRY.map((p) => [p.id, p.apiKeyLabel]),
)

/** The LLM block: model, sampling, context and vision, shared by every agent write form. */
const llmSchema = z.object({
  model: z.string().min(1, 'Model is required'),
  // Required: the operator refuses an agent without a provider (issue #96),
  // and the hub reads an empty one on update as "leave unchanged".
  provider: z.string().min(1, 'Provider is required'),
  maxTurns: z.coerce.number().int().min(1).optional(),
  temperature: z.coerce.number().min(0).max(2).optional(),
  // Empty input means "use the operator default (131072)": the hub treats an
  // absent field on update as "leave unchanged", so blank keeps whatever
  // is stored instead of wiping it to a bogus 0.
  contextWindow: z.preprocess(
    (v) => (v === '' || v === undefined || v === null ? undefined : v),
    z.coerce.number().int().min(1024).optional(),
  ),
  // Always a concrete boolean in the payload: the hub treats a missing
  // `vision` on update as "leave unchanged", so the form must send false
  // explicitly to turn image input back off.
  vision: z.boolean().optional(),
  // Free-text licence record, optional. Empty input means "leave whatever is
  // stored": the hub's LLM patch semantics treat an absent/blank field as
  // "leave unchanged". The operator does not verify the value.
  modelLicense: z.preprocess(
    (v) => (v === '' || v === undefined || v === null ? undefined : v),
    z.string().max(256).optional(),
  ),
})

// Concurrency cap: the operator keeps at most this many per-task Jobs
// running. 0 pauses the agent. Always sent as a concrete number, like
// `vision`, because the hub reads an absent value on update as
// "leave unchanged".
const replicasSchema = z.coerce.number().int().min(0).optional()

/** Clock time as the hub stores it: 24-hour HH:MM. The hub is the authority —
 *  it rejects anything else — so the form catches it here rather than letting
 *  the save come back as a 400 the reader has to decode. */
const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/

/** Weekday numbers as the CRD numbers them: 0 and 7 are Sunday, 1 is Monday.
 *  Both spellings of Sunday are accepted on write; the form stores 0. */
export const WEEKDAYS: ReadonlyArray<{ value: number; label: string }> = [
  { value: 0, label: 'Sun' },
  { value: 1, label: 'Mon' },
  { value: 2, label: 'Tue' },
  { value: 3, label: 'Wed' },
  { value: 4, label: 'Thu' },
  { value: 5, label: 'Fri' },
  { value: 6, label: 'Sat' },
]

/**
 * The weekly window. `enabled` is the form's switch, not a stored field: off
 * means the window is removed from the request as an empty object, which is
 * how the hub reads "any hour again" (absent would mean "leave it alone").
 */
const workingHoursSchema = z
  .object({
    enabled: z.boolean(),
    timezone: z.string().optional(),
    days: z.array(z.number().int().min(0).max(7)),
    start: z.string().optional(),
    end: z.string().optional(),
  })
  .superRefine((v, ctx) => {
    if (!v.enabled) return
    for (const key of ['start', 'end'] as const) {
      const value = v[key] ?? ''
      if (!CLOCK.test(value)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: [key],
          message: 'Use HH:MM on a 24-hour clock.',
        })
      }
    }
    // An end equal to the start is no window at all; an end before it is one
    // window closing the next morning, which is a night shift and legal.
    if (CLOCK.test(v.start ?? '') && (v.start ?? '') === (v.end ?? '')) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['end'],
        message: 'The same time on both sides leaves no window at all.',
      })
    }
  })

/**
 * The persona step: either the agent's own instructions, written inline and
 * stored by the hub as a persona owned by the new agent, or a shared template
 * from the persona library. Inline is the default so the first agent does not
 * need a library entry first (issue #88).
 */
const personaSchema = z
  .object({
    mode: z.enum(['inline', 'template']),
    id: z.string().optional(),
    text: z.string().optional(),
  })
  .superRefine((p, ctx) => {
    if (p.mode === 'inline' && !p.text?.trim()) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['text'],
        message: 'Instructions are required',
      })
    }
    if (p.mode === 'template' && !p.id) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['id'], message: 'Persona is required' })
    }
  })

const openAIAuthMethodSchema = z.enum(['APIKey', 'ChatGPTSignIn']).optional()

/** ChatGPT sign-in runs one task at a time: the hub refuses more, and the
 *  form says so before the save does. */
function capSignInReplicas(
  data: { llm: { provider?: string }; openAIAuthMethod?: string; replicas?: number },
  ctx: z.RefinementCtx,
) {
  if (
    data.llm.provider === 'openai' &&
    data.openAIAuthMethod === 'ChatGPTSignIn' &&
    (data.replicas ?? 1) > 1
  ) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['replicas'],
      message: 'ChatGPT sign-in runs one task at a time; set this to 1',
    })
  }
}

/** A custom provider needs its base URL. */
function requireCustomProviderUrl(
  data: { llm: { provider?: string }; customProviderUrl?: string },
  ctx: z.RefinementCtx,
) {
  if (data.llm.provider === 'custom' && !data.customProviderUrl?.trim()) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['customProviderUrl'],
      message: 'URL is required for custom providers',
    })
  }
}

/**
 * Validation for both agent write forms: the creation wizard and the edit
 * form. Kept in one module so the two cannot drift.
 */
export const agentSchema = z
  .object({
    name: z.string().min(1, 'Name is required'),
    description: z.string().optional(),
    imageRef: z.object({ name: z.string().min(1, 'Image is required') }),
    llm: llmSchema,
    providerApiKey: z.string().optional(),
    customProviderUrl: z.string().optional(),
    openAIAuthMethod: openAIAuthMethodSchema,
    persona: personaSchema,
    replicas: replicasSchema,
    groupId: z.string().optional(),
  })
  .superRefine(requireCustomProviderUrl)
  .superRefine(capSignInReplicas)

/**
 * The runtime section's form: the LLM and scaling fields only. Identity,
 * image and persona are edited elsewhere, so they are not validated here.
 */
export const runtimeSchema = z
  .object({
    llm: llmSchema,
    providerApiKey: z.string().optional(),
    customProviderUrl: z.string().optional(),
    openAIAuthMethod: openAIAuthMethodSchema,
    replicas: replicasSchema,
    workingHours: workingHoursSchema,
  })
  .superRefine(requireCustomProviderUrl)
  .superRefine(capSignInReplicas)

// zod 4 tracks input/output types separately (coercions & defaults make
// inputs wider); useForm needs both so the resolver types line up.
export type AgentFormInput = z.input<typeof agentSchema>
export type AgentFormValues = z.infer<typeof agentSchema>

export const agentDefaults: AgentFormInput = {
  name: '',
  description: '',
  imageRef: { name: '' },
  llm: {
    model: '',
    provider: 'ollama-cloud',
    maxTurns: 100,
    temperature: 1,
    vision: false,
    contextWindow: undefined,
    modelLicense: undefined,
  },
  customProviderUrl: '',
  providerApiKey: '',
  openAIAuthMethod: 'APIKey',
  persona: { mode: 'inline', id: '', text: '' },
  replicas: 1,
  groupId: '',
}

/**
 * The provider credential blocks for a write. A key is attached only when one
 * was entered, so an edit that leaves the key blank keeps the stored one. A
 * provider with a choice of auth method always sends the chosen one, and in
 * ChatGPT sign-in mode never a key (the hub refuses both together).
 */
function buildProviderRequest(
  provider: string,
  apiKey: string,
  customProviderUrl: string,
  authMethod: OpenAIAuthMethod | undefined,
): Pick<AgentRequest, ProviderRequestField> {
  const p = findLlmProvider(provider)
  if (!p) return {}
  if (p.authMethods) {
    const method = authMethod ?? 'APIKey'
    const key = method === 'APIKey' && apiKey ? { apiKey } : {}
    return { [p.requestField]: { authMethod: method, ...key } }
  }
  const key = apiKey ? { apiKey } : undefined
  if (p.needsUrl) return { [p.requestField]: { url: customProviderUrl, ...(key ?? {}) } }
  return key ? { [p.requestField]: key } : {}
}

/**
 * The LLM block as sent for the chosen provider. Pi's built-in providers
 * (OpenAI, Anthropic, Google) do not apply a temperature, so none is sent;
 * the operator reports one set by other means as TemperatureApplied=False.
 */
function llmForProvider<T extends { provider?: string; temperature?: number }>(llm: T): T {
  if (!findLlmProvider(llm.provider)?.piBuiltIn) return llm
  const rest = { ...llm }
  delete rest.temperature
  return rest
}

/**
 * Builds the write-side agent body from validated form values.
 *
 * The group is only sent on create: an agent's group is fixed for its lifetime.
 *
 * `replicas` is the concurrency cap on the agent's per-task Jobs — always sent
 * as a number, never omitted, so an edit that lowers or raises it lands.
 */
export function buildAgentRequest(
  values: AgentFormValues,
  opts: { isEdit: boolean },
): AgentRequest {
  return {
    name: values.name,
    description: values.description || undefined,
    imageRef: values.imageRef,
    llm: llmForProvider(values.llm),
    ...buildProviderRequest(
      values.llm.provider ?? '',
      values.providerApiKey ?? '',
      values.customProviderUrl ?? '',
      values.openAIAuthMethod,
    ),
    persona:
      values.persona.mode === 'inline'
        ? { text: values.persona.text ?? '' }
        : { id: values.persona.id ?? '' },
    replicas: values.replicas,
    groupId: opts.isEdit ? undefined : values.groupId,
  }
}

export type RuntimeFormInput = z.input<typeof runtimeSchema>
export type RuntimeFormValues = z.infer<typeof runtimeSchema>

/**
 * The runtime section's write body: the agent's name (the hub reads a partial
 * update as a PUT of the fields present), the LLM block, the scaling cap and
 * the provider credentials. Identity, image and persona stay untouched.
 */
export function buildRuntimeRequest(name: string, values: RuntimeFormValues): AgentRequest {
  return {
    name,
    llm: llmForProvider(values.llm),
    ...buildProviderRequest(
      values.llm.provider ?? '',
      values.providerApiKey ?? '',
      values.customProviderUrl ?? '',
      values.openAIAuthMethod,
    ),
    replicas: values.replicas,
    // Always sent: absent would read as "leave the schedule alone", so turning
    // the window off has to be an explicit empty window, not an omission.
    workingHours: workingHoursFromForm(values.workingHours),
  }
}

/**
 * The form's window as the hub stores it: an empty object when the switch is
 * off — the patch convention for "no window, any hour" — and the window itself
 * when it is on. The clock is validated above, so what reaches here is usable.
 */
export function workingHoursFromForm(v: RuntimeFormValues['workingHours']): AgentWorkingHours {
  if (!v.enabled) return {}
  return {
    timezone: v.timezone?.trim() ?? '',
    days: [...new Set(v.days)].sort((a, b) => a - b),
    start: v.start ?? '',
    end: v.end ?? '',
  }
}

/**
 * The stored window as the form reads it. Sunday may be stored as 0 or 7 (cron
 * numbers it both ways), so both land on the Sunday toggle: a window written with
 * 7 by kubectl still shows as Sunday here, and goes back as 0.
 */
export function workingHoursToForm(
  wh: AgentWorkingHours | undefined,
): RuntimeFormInput['workingHours'] {
  const days = (wh?.days ?? []).map((d) => (d === 7 ? 0 : d)).filter((d) => d >= 0 && d <= 6)
  return {
    enabled: wh != null,
    timezone: wh?.timezone ?? '',
    days: [...new Set(days)].sort((a, b) => a - b),
    start: wh?.start ?? '',
    end: wh?.end ?? '',
  }
}
