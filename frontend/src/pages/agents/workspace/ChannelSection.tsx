import { ChannelEditor } from '../../channels/ChannelEditor'
import { useChannels } from '../../../api/channels'
import { Panel } from '../../../primitives/Panel'
import { SectionStatus } from '../../../primitives/SectionStatus'
import { SectionPage } from './SectionPage'
import { useWorkspaceAgent } from './workspaceAgent'

/**
 * The agent's inbox channel, edited in place. The inbox is provisioned from
 * this agent, so its entity ref is the stable handle, not the name.
 */
export function ChannelSection() {
  const agent = useWorkspaceAgent()
  const { data, isLoading, error } = useChannels({ pageSize: 500 })
  const channel = data?.items.find((c) => c.kind === 'agent' && c.entityRef === agent.id)

  return (
    <SectionPage title="Channel">
      {channel ? (
        <ChannelEditor channelId={channel.id} hideSubscriptions />
      ) : isLoading ? (
        <Panel className="cropped">
          <SectionStatus state="loading" />
        </Panel>
      ) : error ? (
        <Panel className="cropped">
          <SectionStatus state="error" />
        </Panel>
      ) : (
        <Panel title="Channel" className="cropped">
          <div className="label" style={{ color: 'var(--ink-3)' }}>
            This agent has no channel yet — the hub provisions one per agent on its next registry
            sync.
          </div>
        </Panel>
      )}
    </SectionPage>
  )
}
