import type { ComponentType } from 'react'
import { Navigate, useParams } from 'react-router-dom'
import { usePlugins } from '../../../api/plugins'
import { AgentPersonaSection } from '../AgentPersonaSection'
import { AgentChat } from './AgentChat'
import { ChannelSection } from './ChannelSection'
import {
  AgentImageSection,
  AgentToolsSection,
  AgentMCPSection,
  AgentSkillsSection,
} from '../AgentImageSection'
import { RuntimeSettings } from './RuntimeSettings'
import { PluginSlot } from '../../../plugins/PluginSlot'
import { SectionPage } from './SectionPage'
import { TriggersSection } from './TriggersSection'
import { parseSection, sectionPath, type SectionId } from './sections'
import { useWorkspaceAgent, useWorkspacePlugins } from './workspaceAgent'

function PersonaSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Persona">
      <AgentPersonaSection agent={agent} />
    </SectionPage>
  )
}

function RuntimeSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Runtime & env">
      {/* One grid-gap mechanism for the stacked cards (issue #215); the
          merged Environment card lives inside AgentImageSection's form. */}
      <div className="panel-stack">
        <RuntimeSettings agent={agent} />
        <AgentImageSection agent={agent} />
      </div>
    </SectionPage>
  )
}

/** The retired agent edit form: its settings now live on the Runtime & env section. */
export function LegacyEditRedirect() {
  const { id = '' } = useParams()
  return <Navigate to={sectionPath(id, 'runtime')} replace />
}

function ToolsSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Tools & MCP">
      <AgentToolsSection agent={agent} />
      <AgentMCPSection agent={agent} />
    </SectionPage>
  )
}

function SkillsSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Skills">
      <AgentSkillsSection agent={agent} />
    </SectionPage>
  )
}

function PluginSection({ pluginId }: { pluginId: string }) {
  const agent = useWorkspaceAgent()
  const plugins = useWorkspacePlugins()
  const { isPending } = usePlugins()
  const contribution = plugins.find((p) => p.id === pluginId)
  if (!contribution) {
    // The plugin list loads first. Once it is in, an id without an active
    // agent.panel contribution has no panel to show, so go to the overview.
    if (isPending)
      return (
        <p className="label" style={{ padding: '28px 32px' }}>
          Loading…
        </p>
      )
    return <Navigate to={sectionPath(agent.id, 'overview')} replace />
  }
  return (
    <SectionPage title={contribution.label}>
      <PluginSlot slot="agent.panel" props={{ agentName: agent.id }} pluginId={pluginId} />
    </SectionPage>
  )
}

type BuiltInSection = Exclude<SectionId, `plugin:${string}` | 'overview'>

const BUILT_IN_VIEWS: Record<BuiltInSection, ComponentType> = {
  chat: AgentChat,
  persona: PersonaSection,
  runtime: RuntimeSection,
  tools: ToolsSection,
  skills: SkillsSection,
  triggers: TriggersSection,
  channel: ChannelSection,
}

/** Renders the section named by the :section route segment. */
export function SectionRoute() {
  const { id = '', section = '' } = useParams()
  const parsed = parseSection(section)
  if (!parsed || parsed === 'overview') {
    return <Navigate to={sectionPath(id, 'overview')} replace />
  }
  if (parsed.startsWith('plugin:')) {
    return <PluginSection pluginId={parsed.slice('plugin:'.length)} />
  }
  const View = BUILT_IN_VIEWS[parsed as BuiltInSection]
  return <View />
}
