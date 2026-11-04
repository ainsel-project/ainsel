import type { ComponentType } from 'react'
import { Navigate, useParams } from 'react-router-dom'
import { AgentChannelTab } from '../../channels/AgentChannelTab'
import { AgentTriggers } from '../AgentTriggers'
import { AgentSchedules } from '../AgentSchedules'
import { AgentPersonaSection } from '../AgentPersonaSection'
import {
  AgentImageSection,
  AgentToolsSection,
  AgentMCPSection,
  AgentSkillsSection,
} from '../AgentImageSection'
import { AgentEnvSection } from '../AgentEnvSection'
import { RuntimeSettings } from './RuntimeSettings'
import { PluginSlot } from '../../../plugins/PluginSlot'
import { SectionPage } from './SectionPage'
import { parseSection, sectionPath, type SectionId } from './sections'
import { useWorkspaceAgent, useWorkspacePlugins } from './workspaceAgent'

export function PersonaSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Persona">
      <AgentPersonaSection agent={agent} />
    </SectionPage>
  )
}

export function RuntimeSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Runtime & env">
      <RuntimeSettings agent={agent} />
      <AgentImageSection agent={agent} />
      <AgentEnvSection agent={agent} />
    </SectionPage>
  )
}

/** The retired agent edit form: its settings now live on the Runtime & env section. */
export function LegacyEditRedirect() {
  const { id = '' } = useParams()
  return <Navigate to={sectionPath(id, 'runtime')} replace />
}

export function ToolsSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Tools & MCP">
      <AgentToolsSection agent={agent} />
      <AgentMCPSection agent={agent} />
    </SectionPage>
  )
}

export function SkillsSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Skills">
      <AgentSkillsSection agent={agent} />
    </SectionPage>
  )
}

export function TriggersSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Triggers">
      <AgentTriggers agentId={agent.id} agentName={agent.name} />
      <AgentSchedules agentId={agent.id} />
    </SectionPage>
  )
}

export function ChannelSection() {
  const agent = useWorkspaceAgent()
  return (
    <SectionPage title="Channel">
      <AgentChannelTab agentId={agent.id} agentName={agent.name} />
    </SectionPage>
  )
}

export function ChatSection() {
  return <SectionPage title="Chat" />
}

function PluginSection({ pluginId }: { pluginId: string }) {
  const agent = useWorkspaceAgent()
  const plugins = useWorkspacePlugins()
  const label = plugins.find((p) => p.id === pluginId)?.label ?? pluginId
  return (
    <SectionPage title={label}>
      <PluginSlot slot="agent.panel" props={{ agentName: agent.id }} pluginId={pluginId} />
    </SectionPage>
  )
}

type BuiltInSection = Exclude<SectionId, `plugin:${string}` | 'overview'>

const BUILT_IN_VIEWS: Record<BuiltInSection, ComponentType> = {
  chat: ChatSection,
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
