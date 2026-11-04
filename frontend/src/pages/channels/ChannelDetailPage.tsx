import { useState } from 'react'
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom'
import {
  buildConnections,
  isChannelDeletable,
  useChannel,
  useChannelSubscriptions,
  useChannels,
  useDeleteChannel,
} from '../../api/channels'
import { Titleblock } from '../../layout/Titleblock'
import { Panel } from '../../primitives/Panel'
import { SectionStatus } from '../../primitives/SectionStatus'
import { Tag } from '../../primitives/Tag'
import { sectionPath } from '../agents/workspace/sections'
import { ChannelEditor } from './ChannelEditor'

export function ChannelDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: channel, isLoading, error } = useChannel(id)
  const { data: subs } = useChannelSubscriptions()
  const { data: list } = useChannels({ pageSize: 500 })
  const edges = buildConnections(subs?.items, list?.items)

  const isCustom = channel?.kind === 'custom'
  const del = useDeleteChannel()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const deletable = channel ? isChannelDeletable(channel, edges) : false

  // An agent inbox is configured in its agent's workspace, not on this page.
  if (channel?.kind === 'agent' && channel.entityRef) {
    return <Navigate to={sectionPath(channel.entityRef, 'channel')} replace />
  }

  if (isLoading) {
    return (
      <>
        <Titleblock
          crumbs={
            <>
              <Link to="/channels">Channels</Link> / <b>…</b>
            </>
          }
          title={<>Channel</>}
        />
        <div style={{ padding: '28px 32px' }}>
          <Panel className="cropped">
            <SectionStatus state="loading" />
          </Panel>
        </div>
      </>
    )
  }

  if (error || !channel) {
    return (
      <>
        <Titleblock
          crumbs={
            <>
              <Link to="/channels">Channels</Link> / <b>{id}</b>
            </>
          }
          title={<>Channel</>}
        />
        <div style={{ padding: '28px 32px', display: 'grid', gap: 16 }}>
          <div className="label" style={{ color: 'var(--ink-3)' }}>
            This channel is not in the registry — its source may have been deleted.{' '}
            <Tag variant="stale">id {id}</Tag>
          </div>
          <Panel className="cropped">
            {error ? (
              <SectionStatus state="error" />
            ) : (
              <div className="label" style={{ padding: '4px 0' }}>
                Nothing to show.
              </div>
            )}
          </Panel>
        </div>
      </>
    )
  }

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Operations / <Link to="/channels">Channels</Link> / <b>{channel.name}</b>
          </>
        }
        title={
          <>
            Channel <em>{channel.name}</em>
          </>
        }
        actions={
          isCustom ? (
            deletable ? (
              confirmDelete ? (
                <span style={{ display: 'inline-flex', gap: 8 }}>
                  <button
                    style={{ color: 'var(--err)' }}
                    onClick={() =>
                      del.mutate(channel.id, { onSuccess: () => navigate('/channels') })
                    }
                  >
                    Confirm delete
                  </button>
                  <button onClick={() => setConfirmDelete(false)}>Keep</button>
                </span>
              ) : (
                <button onClick={() => setConfirmDelete(true)}>Delete channel</button>
              )
            ) : (
              <button disabled title="Remove all subscriptions attached to this channel first">
                Delete channel
              </button>
            )
          ) : null
        }
      />
      <div style={{ padding: '28px 32px' }}>
        <ChannelEditor channelId={id} />
      </div>
    </>
  )
}
