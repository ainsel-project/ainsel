import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useUpdateAgent, type AgentResponse } from '../../api/agents'
import { useAgentImage, useAgentImages, type AgentImageResponse } from '../../api/agentImages'
import { useMCPServers } from '../../api/mcpServers'
import { ApiError } from '../../api/client'
import { DualListPicker } from '../../primitives/DualListPicker'
import { Panel } from '../../primitives/Panel'
import { Select } from '../../primitives/Select'
import { ManagedBadge } from '../../components/ManagedBadge'
import { agentImagePath } from '../../components/entityPaths'
import { SkillsSelector } from './SkillsSelector'
import { AgentEnvSection } from './AgentEnvSection'
import { EnvTable } from './workspace/EnvTable'
import { withDraftIds } from './workspace/envRows'

type AgentImageSectionProps = {
  agent: AgentResponse
}

/** Read-only label/value row of the image card — text where the image page has an input. */
function ImageFact({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <div className="label" style={{ fontSize: 11, color: 'var(--ink-3)' }}>
        {label}
      </div>
      <div className="tool-readonly">
        {value ? <code style={{ wordBreak: 'break-all' }}>{value}</code> : <em>—</em>}
      </div>
    </div>
  )
}

/**
 * The referenced image, shown read-only: identity (display name, container URL,
 * description) and its environment with secret values masked. Editing the image
 * happens on the image page — the "Edit image" link is the only action here —
 * because an image is shared: an edit updates every agent that references it.
 */
function ImageDetails({
  image,
  imageName,
}: {
  image: AgentImageResponse | undefined
  imageName: string
}) {
  const env = image?.env ?? []
  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <div style={{ display: 'grid', gap: 10 }}>
        <div>
          <div className="label" style={{ fontSize: 11, color: 'var(--ink-3)' }}>
            Display name <ManagedBadge managedBy={image?.managedBy} />
          </div>
          <div className="tool-readonly">
            <code style={{ wordBreak: 'break-all' }}>{image?.displayName ?? imageName}</code>
          </div>
        </div>
        <ImageFact label="Image URL" value={image?.imageURL} />
        <ImageFact label="Description" value={image?.description} />
      </div>
      <div style={{ display: 'grid', gap: 6 }}>
        <div className="label" style={{ fontSize: 11, color: 'var(--ink-3)' }}>
          Environment
        </div>
        {/* The image's own env, never editable here; secret values stay masked. */}
        <EnvTable
          variant="plain"
          readOnly
          agentEnv={withDraftIds(
            env.map((e) => ({ name: e.name, value: e.secret ? '' : e.value, secret: !!e.secret })),
          )}
        />
      </div>
    </div>
  )
}

/**
 * The image panel of the agent's Runtime & env section: a dropdown that
 * re-points the agent at another image (agent-scoped: switching writes only
 * this agent's `imageRef`) plus a read-only view of the referenced image. The
 * image itself — container URL, env, tools, MCP servers — is edited on its own
 * page, never here, so no save on this page can touch the shared image.
 */
function ImagePanel({ agent }: AgentImageSectionProps) {
  const images = useAgentImages({ pageSize: 200 })
  const updateAgent = useUpdateAgent()
  const [refError, setRefError] = useState<string | null>(null)
  const imageName = agent.imageRef?.name
  const image = useAgentImage(imageName)

  const onSwitchImage = (next: string) => {
    if (!next || next === imageName) return
    setRefError(null)
    updateAgent.mutate(
      { id: agent.id, body: { name: agent.name, imageRef: { name: next } } },
      {
        onError: (err) =>
          setRefError(err instanceof ApiError ? err.message : 'Failed to switch image.'),
      },
    )
  }

  return (
    <Panel
      title="Image"
      className="cropped"
      right={imageName ? <Link to={agentImagePath(imageName)}>Edit image</Link> : null}
    >
      <div style={{ display: 'grid', gap: 10 }}>
        <Select
          aria-label="Agent image"
          value={imageName ?? ''}
          onChange={onSwitchImage}
          options={(images.data?.items ?? []).map((i) => ({
            value: i.id,
            label: i.displayName ?? i.id,
          }))}
          emptyLabel="Select an image…"
          disabled={updateAgent.isPending}
        />
        <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
          The image bundles the runtime: container image URL, environment, tools, skills, and MCP
          servers. Switching re-points this agent only; the image itself is edited on its own page,
          which updates every agent that references it.
        </p>
        {refError ? (
          <p className="label" style={{ margin: 0, color: 'var(--signal)' }}>
            {refError}
          </p>
        ) : null}
        {imageName ? (
          image.isError ? (
            <p className="label" style={{ margin: 0, color: 'var(--signal)' }}>
              Could not load the referenced image — it may have been deleted. Open it on the image
              page to check.
            </p>
          ) : (
            <ImageDetails image={image.data} imageName={imageName} />
          )
        ) : null}
      </div>
    </Panel>
  )
}

function NoImagePanel({ what, hint }: { what: string; hint: string }) {
  return (
    <Panel title={what} className="cropped">
      <p className="label">{hint}</p>
    </Panel>
  )
}

/**
 * The Runtime & env section of the agent detail page: the image picker with a
 * read-only view of the referenced image, and the agent's own environment
 * overrides. Every save here writes only this agent — the shared image is
 * edited on the image page alone (its "Edit image" link leads there), which
 * removes the shared-write footgun from this page.
 */
export function AgentImageSection({ agent }: AgentImageSectionProps) {
  const imageName = agent.imageRef?.name
  return (
    <div className="panel-stack">
      <ImagePanel agent={agent} />
      {imageName ? null : (
        <NoImagePanel what="Image" hint="No image linked yet — pick one above to configure it." />
      )}
      <AgentEnvSection agent={agent} />
    </div>
  )
}

/**
 * The Tools section of the agent detail page: this agent's own tool allowlist.
 * The referenced image carries the catalog — the capabilities it ships — and the
 * agent picks from it, so two agents on the same image can hold different
 * tools. Saving writes `spec.enabledTools` on this agent alone.
 *
 * An empty selection is not "nothing": the agent then runs with every tool the
 * image ships. Names are matched by the runtime, MCP tools included
 * (`mcp__<server>__<tool>`).
 */
export function AgentToolsSection({ agent }: AgentImageSectionProps) {
  const imageName = agent.imageRef?.name
  const image = useAgentImage(imageName)
  const updateAgent = useUpdateAgent()
  const [saveError, setSaveError] = useState<string | null>(null)

  const catalog = image.data?.tools ?? []
  const selected = agent.enabledTools ?? []

  const onChange = (names: string[]) => {
    setSaveError(null)
    updateAgent.mutate(
      { id: agent.id, body: { name: agent.name, enabledTools: names } },
      {
        onError: (err) =>
          setSaveError(err instanceof ApiError ? err.message : 'Failed to update tools.'),
      },
    )
  }

  if (!imageName) {
    return (
      <NoImagePanel
        what="Tools"
        hint="No image linked yet — pick one in Runtime & env to choose from the tools it ships."
      />
    )
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <Panel title="Tools">
        <DualListPicker
          items={catalog}
          selectedIds={selected}
          onChange={onChange}
          getId={(tool) => tool.name}
          getLabel={(tool) => tool.name}
          getDescription={(tool) => tool.description}
          isLoading={image.isLoading}
          emptyLabel="This image ships no tools."
          enabledTitle="Enabled on this agent"
        />
      </Panel>
      {selected.length === 0 ? (
        <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
          Nothing selected yet — this agent runs with every tool the image ships. Pick a subset to
          narrow it down.
        </p>
      ) : null}
      {saveError ? (
        <p className="label" style={{ margin: 0, color: 'var(--signal)' }}>
          {saveError}
        </p>
      ) : null}
    </div>
  )
}

/**
 * The Skills section of the agent detail page: this agent's own skill
 * selection, taken from the hub's skill library. Skills belong to the agent, not
 * to its runtime image, so an agent needs no image to be given one and two
 * agents on the same image can carry different skills.
 */
export function AgentSkillsSection({ agent }: AgentImageSectionProps) {
  const updateAgent = useUpdateAgent()
  const [saveError, setSaveError] = useState<string | null>(null)

  const onChange = (ids: string[]) => {
    setSaveError(null)
    updateAgent.mutate(
      { id: agent.id, body: { name: agent.name, skills: ids } },
      {
        onError: (err) =>
          setSaveError(err instanceof ApiError ? err.message : 'Failed to update skills.'),
      },
    )
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <SkillsSelector selectedIds={agent.skills ?? []} onChange={onChange} previewable />
      {saveError ? (
        <p className="label" style={{ margin: 0, color: 'var(--signal)' }}>
          {saveError}
        </p>
      ) : null}
    </div>
  )
}

/**
 * The agent-scoped MCP part of the Tools & MCP section: the agent's own server
 * selection, resolved from the hub's MCP registry at write time. Until the
 * agent has an explicit list (spec.mcp), the effective set is inherited from
 * the runtime image's servers; the first change pins the selection to this
 * agent.
 */
export function AgentMCPSection({ agent }: AgentImageSectionProps) {
  const imageName = agent.imageRef?.name
  const image = useAgentImage(imageName)
  const { data: registry, isLoading } = useMCPServers()
  const updateAgent = useUpdateAgent()
  const [saveError, setSaveError] = useState<string | null>(null)

  const effective = agent.mcp
    ? agent.mcp.servers.map((s) => s.name)
    : (image.data?.mcpServers ?? []).map((s) => s.name)
  const envNameSet = new Set((image.data?.env ?? []).map((e) => e.name))

  const onChange = (names: string[]) => {
    setSaveError(null)
    updateAgent.mutate(
      { id: agent.id, body: { name: agent.name, mcp: { servers: names } } },
      {
        onError: (err) =>
          setSaveError(err instanceof ApiError ? err.message : 'Failed to update MCP servers.'),
      },
    )
  }

  if (!imageName) {
    return (
      <NoImagePanel
        what="MCP Servers"
        hint="No image linked yet — pick one in Runtime & env to configure MCP servers."
      />
    )
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <div style={{ marginTop: 24 }}>
        <Panel title="MCP Servers">
          <DualListPicker
            items={registry ?? []}
            selectedIds={effective}
            onChange={onChange}
            getId={(s) => s.name}
            getLabel={(s) => s.displayName || s.name}
            getDescription={(s) => {
              const tokenEnv = s.tokenFromEnv ?? ''
              if (tokenEnv === '') return null
              const missing = !envNameSet.has(tokenEnv)
              return (
                <span style={{ color: missing ? 'var(--signal)' : undefined }}>
                  {missing ? `⚠ $${tokenEnv} not in env` : `reads token from $${tokenEnv}`}
                </span>
              )
            }}
            getSearchText={(s) => `${s.displayName ?? ''} ${s.name} ${s.tokenFromEnv ?? ''}`}
            isLoading={isLoading}
            emptyLabel="No MCP servers configured."
            enabledTitle="Enabled on this agent"
          />
        </Panel>
      </div>
      {agent.mcp ? null : (
        <p className="label" style={{ margin: '0 0 4px', color: 'var(--ink-3)' }}>
          Currently inherited from the runtime image — the first change pins the MCP selection to
          this agent.
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
