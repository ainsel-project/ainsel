import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  useGroup,
  useGroupResources,
  useAddGroupMembers,
  useRemoveGroupMember,
  type GroupMember,
  type GroupResourceItem,
  type GroupResourceType,
  type GroupRole,
} from '../../api/groups'
import { useUsers, userDisplayName } from '../../api/users'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { Panel } from '../../primitives/Panel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Autocomplete } from '../../primitives/Autocomplete'
import { Tag } from '../../primitives/Tag'
import { Titleblock } from '../../layout/Titleblock'

const ROLE_LABEL: Record<GroupRole, string> = {
  reader: 'Reader',
  writer: 'Writer',
  owner: 'Owner',
}

// One panel per resource type, in this order — the panel-per-type shape
// matches how AgentDetail groups its sections and keeps small groups
// scannable. Types with no items still render, with an empty-state row.
const RESOURCE_PANELS: { type: GroupResourceType; label: string; empty: string }[] = [
  { type: 'agent', label: 'Agents', empty: 'No agents in this group.' },
  { type: 'agent-image', label: 'Agent images', empty: 'No agent images in this group.' },
  { type: 'connector', label: 'Connectors', empty: 'No connectors in this group.' },
  { type: 'persona', label: 'Personas', empty: 'No personas in this group.' },
  { type: 'skill', label: 'Skills', empty: 'No skills in this group.' },
  { type: 'channel', label: 'Channels', empty: 'No channels in this group.' },
  { type: 'mcp-server', label: 'MCP servers', empty: 'No MCP servers in this group.' },
  { type: 'trigger', label: 'Triggers', empty: 'No triggers in this group.' },
  { type: 'cron-trigger', label: 'Cron triggers', empty: 'No cron triggers in this group.' },
]

// Detail route for one resource. Triggers have no detail route of their own,
// so they link to the owning agent's trigger tab; an unresolvable owner
// renders the name unlinked rather than pointing at a 404.
function groupResourceHref(item: GroupResourceItem): string | null {
  const id = encodeURIComponent(item.id)
  switch (item.type) {
    case 'agent':
      return `/agents/${id}`
    case 'agent-image':
      return `/agent-images/${id}`
    case 'connector':
      return `/connectors/${id}`
    case 'persona':
      return `/personas/${id}`
    case 'skill':
      return `/skills/${id}`
    case 'channel':
      return `/channels/${id}`
    case 'mcp-server':
      return `/settings/mcp-servers/${id}`
    case 'trigger':
    case 'cron-trigger':
      return item.agentRef ? `/agents/${encodeURIComponent(item.agentRef)}?tab=triggers` : null
  }
}

export function GroupDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data, isLoading, error } = useGroup(id)
  const resources = useGroupResources(id)
  const users = useUsers()
  const addMembers = useAddGroupMembers()
  const removeMember = useRemoveGroupMember()
  const [confirmUserId, setConfirmUserId] = useState<string | null>(null)
  const [selectedUserId, setSelectedUserId] = useState('')
  const [selectedRole, setSelectedRole] = useState<GroupRole>('reader')

  const memberIds = new Set((data?.members ?? []).map((m) => m.user.id))
  const nonMembers = (users.data ?? []).filter((u) => !memberIds.has(u.id))
  const userById = useMemo(
    () => new Map((users.data ?? []).map((u) => [u.id, u])),
    [users.data],
  )

  const onAddMember = async () => {
    if (!id || !selectedUserId) return
    await addMembers.mutateAsync({ groupId: id, userIds: [selectedUserId], role: selectedRole })
    setSelectedUserId('')
  }

  const onConfirmRemove = async () => {
    if (!id || !confirmUserId) return
    await removeMember.mutateAsync({ groupId: id, userId: confirmUserId })
    setConfirmUserId(null)
  }

  const confirmMember = (data?.members ?? []).find((m) => m.user.id === confirmUserId)

  const resourcesByType = useMemo(() => {
    const map = new Map<GroupResourceType, GroupResourceItem[]>()
    for (const item of resources.data?.items ?? []) {
      const list = map.get(item.type)
      if (list) list.push(item)
      else map.set(item.type, [item])
    }
    return map
  }, [resources.data])
  const totalResources = resources.data?.items.length ?? 0

  const resourceColumns: readonly Column<GroupResourceItem>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (item) => {
        const href = groupResourceHref(item)
        return (
          <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
            {href ? <Link to={href}>{item.name}</Link> : <b>{item.name}</b>}
            {item.public ? <Tag>Public</Tag> : null}
          </span>
        )
      },
    },
  ]

  const memberColumns: readonly Column<GroupMember>[] = [
    {
      key: 'username',
      header: 'Username',
      cell: (m) => <b>{userDisplayName(m.user)}</b>,
    },
    {
      key: 'email',
      header: 'Email',
      cell: (m) => <span className="num">{m.user.email}</span>,
    },
    {
      key: 'role',
      header: 'Role',
      width: 100,
      cell: (m) => <b>{ROLE_LABEL[m.role] ?? m.role}</b>,
    },
    {
      key: 'admin',
      header: 'Admin',
      width: 80,
      align: 'center',
      cell: (m) => (m.user.isAdmin ? 'Yes' : '—'),
    },
    {
      key: 'actions',
      header: '',
      width: 80,
      align: 'right',
      cell: (m) => (
        <Button
          variant="danger"
          size="sm"
          onClick={() => setConfirmUserId(m.user.id)}
        >
          Remove
        </Button>
      ),
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <Link to="/groups">Groups</Link> /{' '}
            <b>{data?.group.name ?? id ?? '—'}</b>
          </>
        }
        title={<>{data?.group.name ?? <em>Group</em>}</>}
        actions={
          <Button onClick={() => id && navigate(`/groups/${encodeURIComponent(id)}/edit`)}>
            Edit
          </Button>
        }
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 24 }}>
        {isLoading ? <p className="label">Loading…</p> : null}
        {error ? (
          <p className="label" style={{ color: 'var(--signal)' }}>
            Failed to load group.
          </p>
        ) : null}

        {data ? (
          <>
            <Panel title="General" className="cropped">
              <div className="info-grid">
                <div>
                  <div className="k">Name</div>
                  <div className="v">{data.group.name}</div>
                </div>
                <div>
                  <div className="k">Description</div>
                  <div className="v">{data.group.description || '—'}</div>
                </div>
              </div>
            </Panel>

            <Panel title={`Members · ${data.members.length}`} className="cropped">
              <RegisterTable
                rows={data.members}
                columns={memberColumns}
                rowKey={(m) => m.user.id}
                emptyLabel="No members yet."
              />
              <div
                style={{
                  display: 'flex',
                  gap: 8,
                  padding: '12px 14px',
                  borderTop: '1px solid var(--chrome-1)',
                }}
              >
                <Autocomplete
                  value={selectedUserId}
                  onChange={setSelectedUserId}
                  options={nonMembers.map((u) => ({
                    value: u.id,
                    label: userDisplayName(u),
                  }))}
                  placeholder="Add a member…"
                  filter={(option, query) => {
                    const u = userById.get(option.value)
                    if (!u) return false
                    const q = query.toLowerCase()
                    return (
                      userDisplayName(u).toLowerCase().includes(q) ||
                      u.email.toLowerCase().includes(q)
                    )
                  }}
                  aria-label="Select user to add"
                />
                <select
                  value={selectedRole}
                  onChange={(e) => setSelectedRole(e.target.value as GroupRole)}
                  aria-label="Role for new member"
                  style={{ padding: '6px 8px' }}
                >
                  <option value="reader">Reader</option>
                  <option value="writer">Writer</option>
                  <option value="owner">Owner</option>
                </select>
                <Button
                  variant="primary"
                  size="sm"
                  onClick={onAddMember}
                  disabled={!selectedUserId || addMembers.isPending}
                >
                  {addMembers.isPending ? 'Adding…' : 'Add'}
                </Button>
              </div>
            </Panel>

            {resources.isLoading ? <p className="label">Loading resources…</p> : null}
            {resources.isError ? (
              <p className="label" style={{ color: 'var(--signal)' }}>
                Failed to load group resources.
              </p>
            ) : null}
            {resources.data ? (
              totalResources === 0 ? (
                <Panel title="Resources" className="cropped">
                  <p className="label" style={{ padding: '12px 14px', margin: 0 }}>
                    This group owns no resources.
                  </p>
                </Panel>
              ) : (
                RESOURCE_PANELS.map((panel) => {
                  const rows = resourcesByType.get(panel.type) ?? []
                  return (
                    <Panel
                      key={panel.type}
                      title={`${panel.label} · ${rows.length}`}
                      className="cropped"
                    >
                      <RegisterTable
                        rows={rows}
                        columns={resourceColumns}
                        rowKey={(r) => `${r.type}/${r.id}`}
                        emptyLabel={panel.empty}
                      />
                    </Panel>
                  )
                })
              )
            ) : null}
          </>
        ) : null}
      </div>

      <ConfirmModal
        open={confirmUserId !== null}
        title="Remove member?"
        body={
          <>
            <b>{confirmMember ? userDisplayName(confirmMember.user) : confirmUserId}</b> will be removed from this group.
          </>
        }
        confirmLabel={removeMember.isPending ? 'Removing…' : 'Remove'}
        destructive
        onConfirm={onConfirmRemove}
        onCancel={() => setConfirmUserId(null)}
      />
    </>
  )
}
