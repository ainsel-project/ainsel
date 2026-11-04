import { useState } from 'react'
import { useUpdateAgent, type AgentEnvVar, type AgentResponse } from '../../api/agents'
import { useAgentImage } from '../../api/agentImages'
import { ApiError } from '../../api/client'
import { Button } from '../../primitives/Button'
import { Panel } from '../../primitives/Panel'
import { isManaged } from '../../utils/managed'
import { EnvTable } from './workspace/EnvTable'
import { envNameErrors, withDraftIds, type DraftEnvVar } from './workspace/envRows'

type AgentEnvSectionProps = {
  agent: AgentResponse
}

/** The agent's own entries in the shape the hub stores: trimmed names, boolean secret flags. */
function toPayload(env: AgentEnvVar[]): AgentEnvVar[] {
  return env.map((e) => ({
    name: e.name.trim(),
    value: e.value,
    secret: !!e.secret,
  }))
}

/**
 * The Runtime tab's agent-scoped environment section: one table of the image's
 * variables and this agent's own, where the first override pins an env list to
 * this agent and clearing every row hands control back to the image.
 *
 * Secret values are masked by the hub, so an existing secret row shows an
 * empty field: submitting it unchanged keeps the stored value.
 */
export function AgentEnvSection({ agent }: AgentEnvSectionProps) {
  // Externally managed agents (issue #146) are read-only here: the hub
  // refuses the PUT with 409, so the editor's mutation controls are hidden.
  const readOnly = isManaged(agent.managedBy)
  const imageName = agent.imageRef?.name
  const image = useAgentImage(imageName)
  const updateAgent = useUpdateAgent()

  const stored = agent.env ?? []
  const serialized = JSON.stringify(stored)
  const [draft, setDraft] = useState<DraftEnvVar[]>(() => withDraftIds(stored))
  // The env the table orders its rows by: set on load, re-seeded, and at each save.
  const [saved, setSaved] = useState<DraftEnvVar[]>(draft)
  const [syncedFrom, setSyncedFrom] = useState(serialized)
  const [saveError, setSaveError] = useState<string | null>(null)

  // Re-seed the draft when the stored list changes underneath us (a save
  // landing, or another tab re-pointing the image), never while typing.
  if (serialized !== syncedFrom) {
    const fresh = withDraftIds(stored)
    setSyncedFrom(serialized)
    setDraft(fresh)
    setSaved(fresh)
    setSaveError(null)
  }

  const dirty = JSON.stringify(toPayload(draft)) !== JSON.stringify(toPayload(stored))
  const hasErrors = envNameErrors(draft).some((e) => e !== null)

  const onChange = (next: DraftEnvVar[]) => {
    setSaveError(null)
    setDraft(next)
  }

  const onSave = () => {
    if (hasErrors) return
    setSaveError(null)
    setSaved(draft)
    updateAgent.mutate(
      { id: agent.id, body: { name: agent.name, env: toPayload(draft) } },
      {
        onError: (err) =>
          setSaveError(err instanceof ApiError ? err.message : 'Failed to save environment.'),
      },
    )
  }

  if (!imageName) {
    return (
      <Panel title="Environment">
        <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
          No image linked yet — pick one above to configure environment variables.
        </p>
      </Panel>
    )
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <div style={{ marginTop: 24 }}>
        <Panel title="Environment">
          <div style={{ display: 'grid', gap: 8 }}>
            <EnvTable
              agentEnv={draft}
              savedEnv={saved}
              imageEnv={image.data?.env ?? []}
              imageName={imageName}
              readOnly={readOnly}
              onChange={onChange}
            />
            {readOnly ? null : (
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <Button
                  type="button"
                  onClick={onSave}
                  disabled={!dirty || hasErrors || updateAgent.isPending}
                >
                  {updateAgent.isPending ? 'Saving…' : 'Save overrides'}
                </Button>
              </div>
            )}
          </div>
        </Panel>
      </div>

      {agent.env ? null : (
        <p className="label" style={{ margin: '0 0 4px', color: 'var(--ink-3)' }}>
          Currently inherited from the runtime image — the first change pins the environment to this
          agent.
        </p>
      )}
      {saveError ? (
        <p className="label" style={{ margin: 0, color: 'var(--signal)' }}>
          {saveError}
        </p>
      ) : null}
    </div>
  )
}
