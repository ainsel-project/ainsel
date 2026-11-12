import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useUpdateAgent, type AgentResponse } from '../../../api/agents'
import { ApiError } from '../../../api/client'
import { Button } from '../../../primitives/Button'
import { Check } from '../../../primitives/Check'
import { Field } from '../../../primitives/Field'
import { Input } from '../../../primitives/Input'
import { Panel } from '../../../primitives/Panel'
import { isManaged } from '../../../utils/managed'
import {
  WEEKDAYS,
  buildRuntimeRequest,
  runtimeSchema,
  workingHoursToForm,
  type RuntimeFormInput,
  type RuntimeFormValues,
} from '../agentFormModel'
import { ContextWindowField, ProviderFields, TemperatureField } from '../ProviderFields'
import { hasStoredKey } from '../llmProviders'

type RuntimeSettingsProps = {
  agent: AgentResponse
}

/** The browser's own zone, offered as the first timezone suggestion: whoever fills
 *  this form in usually means the hours of the machine they are sitting at. */
function localTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

const TIMEZONE_SUGGESTIONS = [
  'UTC',
  'Europe/Berlin',
  'Europe/London',
  'America/New_York',
  'Asia/Kolkata',
  'Asia/Tokyo',
  'Australia/Sydney',
]

function timezoneSuggestions(): string[] {
  return [...new Set([localTimezone(), ...TIMEZONE_SUGGESTIONS])]
}

/** What the stored window means right now, in the reader's own timezone — the
 *  answer to "why has nothing happened?" without the arithmetic. */
function hoursState(agent: AgentResponse): string {
  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '')
  if (!agent.workingHours) return 'Work is handed to this agent at any hour'
  return agent.hoursOpen
    ? `Open now · until ${when(agent.hoursClosesAt)}`
    : `Closed now · queued work starts ${when(agent.hoursNextOpenAt)}`
}

function toFormValues(agent: AgentResponse): RuntimeFormInput {
  return {
    llm: {
      model: agent.llm?.model ?? '',
      provider: agent.llm?.provider ?? '',
      maxTurns: agent.llm?.maxTurns,
      temperature: agent.llm?.temperature,
      contextWindow: agent.llm?.contextWindow,
      vision: agent.llm?.vision ?? false,
      modelLicense: agent.llm?.modelLicense ?? '',
    },
    customProviderUrl: agent.customProvider?.url ?? '',
    providerApiKey: '',
    replicas: agent.replicas ?? 1,
    workingHours: workingHoursToForm(agent.workingHours),
  }
}

/**
 * The model, provider, context and scaling settings of an agent, edited on the
 * Runtime & env section. Saving sends only these fields plus the agent's name;
 * the hub reads absent fields on update as "leave unchanged".
 *
 * Externally managed agents (issue #146) are read-only here: the hub refuses
 * the PUT with 409, so the fields are disabled and Save is not offered.
 */
export function RuntimeSettings({ agent }: RuntimeSettingsProps) {
  const readOnly = isManaged(agent.managedBy)
  const updateAgent = useUpdateAgent()
  const [saveError, setSaveError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    reset,
    setValue,
    watch,
    formState: { errors, isSubmitting, isDirty },
  } = useForm<RuntimeFormInput, unknown, RuntimeFormValues>({
    resolver: zodResolver(runtimeSchema),
    defaultValues: toFormValues(agent),
  })

  // Re-seed only when the runtime content of the stored agent changes. The
  // agent object is refetched whenever any section saves (image, env, tools),
  // so identity alone must not reset the form; and typed edits survive a
  // refetch unless they were already saved.
  const storedKey = JSON.stringify(toFormValues(agent))
  const seededKey = useRef<string | null>(null)
  useEffect(() => {
    if (seededKey.current === storedKey) return
    if (seededKey.current !== null && isDirty) return
    seededKey.current = storedKey
    reset(JSON.parse(storedKey) as RuntimeFormInput)
  }, [storedKey, isDirty, reset])

  const provider = watch('llm.provider')
  const vision = watch('llm.vision') ?? false
  const hours = watch('workingHours')

  const setDay = (day: number, on: boolean) => {
    const next = on ? [...hours.days, day] : hours.days.filter((d) => d !== day)
    setValue('workingHours.days', next, { shouldDirty: true })
  }

  const onSubmit = handleSubmit(async (values) => {
    setSaveError(null)
    try {
      await updateAgent.mutateAsync({
        id: agent.id,
        body: buildRuntimeRequest(agent.name, values),
      })
      // The saved values are now the form's baseline: clear the dirty flag so
      // the refetch that follows re-seeds from the stored agent.
      reset({ ...values, providerApiKey: '' })
    } catch (err) {
      setSaveError(err instanceof ApiError ? err.message : 'Save failed. Please try again.')
    }
  })

  return (
    <Panel title="Model & scaling" className="cropped">
      <form onSubmit={onSubmit} noValidate style={{ display: 'grid', gap: 18 }}>
        <fieldset
          disabled={readOnly}
          style={{ border: 0, margin: 0, padding: 0, minWidth: 0, display: 'grid', gap: 18 }}
        >
          <div className="form-grid cols-2">
            <Field label="Model" htmlFor="llm.model" error={errors.llm?.model?.message}>
              <Input id="llm.model" {...register('llm.model')} />
            </Field>
            <Field label="Max Turns" htmlFor="llm.maxTurns">
              <Input id="llm.maxTurns" type="number" min={1} {...register('llm.maxTurns')} />
            </Field>
            <TemperatureField provider={provider} registration={register('llm.temperature')} />
            <ContextWindowField
              provider={provider}
              registration={register('llm.contextWindow')}
              error={errors.llm?.contextWindow?.message}
            />
            <Field
              label="Model License"
              htmlFor="llm.modelLicense"
              hint="The licence this model arrives under, as you reviewed it (e.g. Apache-2.0). Recorded in the agent's status, never verified by the operator. Blank leaves it unchanged."
            >
              <Input
                id="llm.modelLicense"
                placeholder="Apache-2.0"
                {...register('llm.modelLicense')}
              />
            </Field>
            <Field
              label="Image Input"
              htmlFor="llm.vision"
              hint="pi attaches screenshots and image files only while this is on. The endpoint is never probed: images sent to a model that cannot see them fail the request, and while this is off a model that can see them receives none. Takes effect when the agent's pod next starts."
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  minHeight: 36,
                }}
              >
                <Check
                  checked={vision}
                  onChange={(v) => setValue('llm.vision', v, { shouldDirty: true })}
                  aria-label="Image Input"
                  disabled={readOnly}
                />
                <span style={{ fontFamily: 'var(--mono)', fontSize: 11, color: 'var(--ink-3)' }}>
                  {vision ? 'Images are sent' : 'Text only'}
                </span>
              </div>
            </Field>
          </div>

          <div className="form-grid cols-2">
            <ProviderFields
              provider={provider}
              onProviderChange={(v) => {
                // A key belongs to the provider it was typed for; never send
                // it as another provider's key.
                setValue('providerApiKey', '')
                setValue('llm.provider', v, { shouldDirty: true, shouldValidate: true })
              }}
              urlRegistration={register('customProviderUrl')}
              keyRegistration={register('providerApiKey')}
              providerError={errors.llm?.provider?.message}
              urlError={errors.customProviderUrl?.message}
              mode="edit"
              storedKey={provider ? hasStoredKey(agent, provider) : false}
            />
            <input type="hidden" {...register('llm.provider')} />
          </div>

          <div className="form-grid cols-2">
            <Field
              label="Max concurrent tasks"
              htmlFor="replicas"
              error={errors.replicas?.message}
              hint="Up to this many tasks run at once, each in its own container. 0 pauses the agent."
            >
              <Input id="replicas" type="number" min={0} {...register('replicas')} />
            </Field>
          </div>

          <header className="tool-subhead">
            <h4>Working hours</h4>
          </header>

          <div style={{ display: 'grid', gap: 12 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Check
                checked={hours.enabled}
                onChange={(on) => setValue('workingHours.enabled', on, { shouldDirty: true })}
                aria-label="Only hand this agent work during a weekly window"
                disabled={readOnly}
              />
              <span style={{ fontFamily: 'var(--mono)', fontSize: 11, color: 'var(--ink-3)' }}>
                {hours.enabled ? hoursState(agent) : 'Work is handed to this agent at any hour'}
              </span>
            </div>

            {hours.enabled ? (
              <div className="form-grid cols-2">
                <Field
                  label="Timezone"
                  htmlFor="workingHours.timezone"
                  hint={`IANA zone the clock is read in, so 09:00 means 09:00 there. Blank means
                    UTC. This machine is in ${localTimezone()}.`}
                >
                  <Input
                    id="workingHours.timezone"
                    list="working-hours-timezones"
                    placeholder="Europe/Berlin"
                    {...register('workingHours.timezone')}
                  />
                  <datalist id="working-hours-timezones">
                    {timezoneSuggestions().map((tz) => (
                      <option key={tz} value={tz} />
                    ))}
                  </datalist>
                </Field>
                <Field
                  label="Days"
                  htmlFor="workingHours.days"
                  hint="Nothing ticked means every day. A window on Mon-Fri is a working week."
                >
                  <div
                    role="group"
                    aria-label="Days"
                    style={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      gap: 10,
                      minHeight: 36,
                      alignItems: 'center',
                    }}
                  >
                    {WEEKDAYS.map((d) => (
                      <span
                        key={d.value}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 6,
                          fontFamily: 'var(--mono)',
                          fontSize: 11,
                          color: 'var(--ink-2)',
                        }}
                      >
                        <Check
                          checked={hours.days.includes(d.value)}
                          onChange={(on) => setDay(d.value, on)}
                          aria-label={d.label}
                          disabled={readOnly}
                        />
                        {d.label}
                      </span>
                    ))}
                  </div>
                </Field>
                <Field
                  label="From"
                  htmlFor="workingHours.start"
                  error={errors.workingHours?.start?.message}
                >
                  <Input
                    id="workingHours.start"
                    type="time"
                    step={60}
                    {...register('workingHours.start')}
                  />
                </Field>
                <Field
                  label="Until"
                  htmlFor="workingHours.end"
                  error={errors.workingHours?.end?.message}
                  hint="An end before the start is one window that closes the next morning. Tasks that arrive outside the window are kept and run when it opens — including chat messages."
                >
                  <Input
                    id="workingHours.end"
                    type="time"
                    step={60}
                    {...register('workingHours.end')}
                  />
                </Field>
              </div>
            ) : null}
          </div>
        </fieldset>

        {saveError ? (
          <p role="alert" className="label" style={{ margin: 0, color: 'var(--signal)' }}>
            {saveError}
          </p>
        ) : null}

        {readOnly ? null : (
          <div>
            <Button type="submit" variant="primary" disabled={isSubmitting}>
              {isSubmitting ? 'Saving…' : 'Save'}
            </Button>
          </div>
        )}
      </form>
    </Panel>
  )
}
