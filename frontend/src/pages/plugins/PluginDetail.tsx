import { useMemo, useState, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  usePlugins,
  usePluginCallerContext,
  useEnablePlugin,
  useDisablePlugin,
  useApprovePluginPermissions,
  useSetPluginGroupAccess,
  useUninstallPlugin,
  type PluginListItem,
} from '../../api/plugins'
import { useGroups } from '../../api/groups'
import { EntityLink } from '../../components/EntityLink'
import { groupPath } from '../../components/entityPaths'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { DualListPicker } from '../../primitives/DualListPicker'
import { Notice } from '../../primitives/Notice'
import { Panel } from '../../primitives/Panel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Dot } from '../../primitives/Dot'
import { Tag } from '../../primitives/Tag'
import { Titleblock } from '../../layout/Titleblock'
import { permissionInfo } from './permissionCatalog'

type PermissionRow = {
  name: string
  declared: boolean
  approved: boolean
}

const dashStyle = { color: 'var(--ink-4)' } as const

const permissionColumns: Column<PermissionRow>[] = [
  {
    key: 'permission',
    header: 'Permission',
    cell: (row) => <code>{row.name}</code>,
  },
  {
    key: 'declared',
    header: 'Declared',
    cell: (row) =>
      row.declared ? <Dot state="warn" aria-label="Declared" /> : <span style={dashStyle}>—</span>,
  },
  {
    key: 'approved',
    header: 'Approved',
    cell: (row) =>
      row.approved ? <Dot state="ok" aria-label="Approved" /> : <span style={dashStyle}>—</span>,
  },
]

// Unknown names (fixtures, older data) render non-expandably. Wording follows
// the permission suffix: a `*:read` grant is not write-capable.
function renderPermissionDetail(row: PermissionRow) {
  const info = permissionInfo(row.name)
  if (!info) return null
  const access = row.name.endsWith(':write') ? 'write' : 'read'
  return (
    <div style={{ display: 'grid', gap: 4, fontSize: '0.9em' }}>
      <div style={{ color: 'var(--ink-2)' }}>
        Grants {access} access to <b>{info.label}</b>
        {info.readOnly ? '; write requests are rejected' : ''}.
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {info.routes.map((route) => (
          <code key={route}>{route}</code>
        ))}
      </div>
    </div>
  )
}

function digestDisplay(digest?: string): string | undefined {
  if (!digest) return undefined
  return digest.length > 22 ? `${digest.slice(0, 22)}…` : digest
}

type StatCardProps = {
  label: string
  figure: ReactNode
  sub?: ReactNode
  subTitle?: string
}

// Informational card mirroring the dashboard KPI markup contract
// (.kpi > .hd > .label, .figure, .sub). Non-interactive: plain div, no code line.
function StatCard({ label, figure, sub, subTitle }: StatCardProps) {
  return (
    <div className="kpi">
      <div className="hd">
        <span className="label">{label}</span>
      </div>
      <div className="figure">{figure}</div>
      {sub != null && (
        <div className="sub">
          <span title={subTitle}>{sub}</span>
        </div>
      )}
    </div>
  )
}

function getStatusText(plugin: PluginListItem): string {
  if (plugin.ready && !plugin.disabled) {
    return 'Ready'
  }
  if (plugin.disabled) {
    return 'Disabled'
  }
  return plugin.reason || 'Not ready'
}

type GroupAccessRow = {
  id: string
  name: string
  description: string
}

export function PluginDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: plugins, isLoading: pluginsLoading } = usePlugins()
  const { data: callerContext } = usePluginCallerContext()
  const { data: groups = [] } = useGroups()
  const enable = useEnablePlugin()
  const disable = useDisablePlugin()
  const approvePermissions = useApprovePluginPermissions()
  const setGroupAccess = useSetPluginGroupAccess()
  const uninstall = useUninstallPlugin()

  const [confirmApproveOpen, setConfirmApproveOpen] = useState(false)
  const [confirmUninstallOpen, setConfirmUninstallOpen] = useState(false)
  const [selectedGroupIds, setSelectedGroupIds] = useState<string[]>([])
  const [groupAccessMode, setGroupAccessMode] = useState(false)

  const isAdmin = callerContext?.isAdmin === true

  const plugin = useMemo(() => {
    return plugins?.find((p) => p.id === id)
  }, [plugins, id])

  const hasUnapprovedPermissions = useMemo(() => {
    if (!plugin) return false
    const declared = new Set(plugin.declaredPermissions ?? [])
    const approved = new Set(plugin.approvedPermissions ?? [])
    return declared.size !== approved.size || [...declared].some((p) => !approved.has(p))
  }, [plugin])

  // One row per permission in the union of declared + approved, so pending
  // (declared only) and stale grants (approved only) are visible at a glance.
  const permissionRows = useMemo<PermissionRow[]>(() => {
    if (!plugin) return []
    const declared = new Set(plugin.declaredPermissions ?? [])
    const approved = new Set(plugin.approvedPermissions ?? [])
    const names = [...new Set([...declared, ...approved])].sort()
    return names.map((name) => ({
      name,
      declared: declared.has(name),
      approved: approved.has(name),
    }))
  }, [plugin])

  // The approve POST sends plugin.declaredPermissions, so the confirm modal
  // must list exactly the declared set — never stale approved-only rows.
  const declaredPermissionRows = useMemo(
    () => permissionRows.filter((row) => row.declared),
    [permissionRows],
  )

  // Read mode renders server state (plugin.groupAccess), never the edit draft.
  const knownGroupIds = useMemo(() => new Set(groups.map((g) => g.id)), [groups])
  const groupAccessRows = useMemo<GroupAccessRow[]>(() => {
    if (!plugin) return []
    return (plugin.groupAccess ?? []).map((gid) => {
      const group = groups.find((g) => g.id === gid)
      return { id: gid, name: group?.name ?? gid, description: group?.description ?? '' }
    })
  }, [plugin, groups])

  const groupAccessColumns = useMemo<Column<GroupAccessRow>[]>(
    () => [
      {
        key: 'group',
        header: 'Group',
        cell: (row) => (
          <EntityLink id={row.id} known={knownGroupIds} label={row.name} path={groupPath} />
        ),
      },
      {
        key: 'description',
        header: 'Description',
        cell: (row) => row.description || '—',
      },
    ],
    [knownGroupIds],
  )

  const handleApprovePermissions = () => {
    if (!id || !plugin?.declaredPermissions) return
    approvePermissions.mutate(
      { id, permissions: plugin.declaredPermissions },
      {
        onSuccess: () => setConfirmApproveOpen(false),
      },
    )
  }

  const handleUninstall = () => {
    if (!id) return
    uninstall.mutate(id, {
      onSuccess: () => {
        navigate('/plugins')
      },
    })
  }

  const handleGroupAccessSave = () => {
    if (!id) return
    setGroupAccess.mutate(
      { id, groups: selectedGroupIds },
      {
        onSuccess: () => setGroupAccessMode(false),
      },
    )
  }

  if (!isAdmin) {
    return (
      <>
        <Titleblock
          crumbs={
            <>
              Admin / <b>Plugins</b>
            </>
          }
          title="Plugin Details"
        />
        <div style={{ padding: '28px 32px' }}>
          <Notice variant="info">Plugins are managed by administrators.</Notice>
        </div>
      </>
    )
  }

  if (pluginsLoading) {
    return (
      <>
        <Titleblock title="Loading…" />
      </>
    )
  }

  if (!plugin) {
    return (
      <>
        <Titleblock title="Plugin not found" />
      </>
    )
  }

  const statusText = getStatusText(plugin)
  const statusVariant = statusText === 'Ready' ? 'ok' : statusText === 'Disabled' ? 'err' : 'warn'

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Plugins</b>
          </>
        }
        title={plugin.displayName || plugin.id}
        actions={
          <Button
            variant="ghost"
            onClick={() => {
              if (plugin.disabled) {
                enable.mutate(plugin.id)
              } else {
                disable.mutate(plugin.id)
              }
            }}
            disabled={enable.isPending || disable.isPending}
          >
            {plugin.disabled ? 'Enable' : 'Disable'}
          </Button>
        }
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 16 }}>
        <div className="kpi-row">
          <StatCard
            label="Status"
            figure={<Tag variant={statusVariant}>{statusText}</Tag>}
            sub={plugin.message}
          />
          <StatCard
            label="Signature"
            figure={
              !plugin.signature ? (
                <Tag variant="stale">Not recorded</Tag>
              ) : plugin.signature.verified ? (
                <Tag variant="ok">Verified</Tag>
              ) : (
                <Tag variant="err">Unsigned</Tag>
              )
            }
            sub={plugin.signature?.signer ?? plugin.signature?.message}
          />
          <StatCard
            label="Checked"
            figure={
              plugin.signature?.checkedAt
                ? new Date(plugin.signature.checkedAt).toLocaleDateString()
                : '—'
            }
            sub={
              plugin.signature?.checkedAt
                ? new Date(plugin.signature.checkedAt).toLocaleString()
                : undefined
            }
          />
          {plugin.imageRepository && (
            <StatCard
              label="Image"
              figure={plugin.imageRepository}
              sub={digestDisplay(plugin.imageDigest)}
              subTitle={plugin.imageDigest}
            />
          )}
        </div>

        <Panel title="Permissions">
          <div style={{ padding: 16, display: 'grid', gap: 14 }}>
            <RegisterTable
              rows={permissionRows}
              columns={permissionColumns}
              rowKey={(row) => row.name}
              emptyLabel="None"
              renderDetail={renderPermissionDetail}
            />
            {hasUnapprovedPermissions && (
              <Button
                variant="primary"
                onClick={() => setConfirmApproveOpen(true)}
                disabled={approvePermissions.isPending}
              >
                Approve permissions
              </Button>
            )}
          </div>
        </Panel>

        <Panel title="Group Access">
          <div style={{ padding: 16, display: 'grid', gap: 14 }}>
            {!groupAccessMode ? (
              <>
                <RegisterTable
                  rows={groupAccessRows}
                  columns={groupAccessColumns}
                  rowKey={(row) => row.id}
                  emptyLabel="No groups have access"
                />
                <Button
                  variant="primary"
                  onClick={() => {
                    setSelectedGroupIds(plugin.groupAccess ?? [])
                    setGroupAccessMode(true)
                  }}
                >
                  Edit groups
                </Button>
              </>
            ) : (
              <>
                <DualListPicker<{ id: string; name: string }>
                  items={groups}
                  selectedIds={selectedGroupIds}
                  onChange={setSelectedGroupIds}
                  getId={(g) => g.id}
                  getLabel={(g) => g.name}
                  availableTitle="Available groups"
                  enabledTitle="Selected groups"
                />
                <div style={{ display: 'flex', gap: 8 }}>
                  <Button
                    variant="primary"
                    onClick={handleGroupAccessSave}
                    disabled={setGroupAccess.isPending}
                  >
                    Save
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => setGroupAccessMode(false)}
                    disabled={setGroupAccess.isPending}
                  >
                    Cancel
                  </Button>
                </div>
              </>
            )}
          </div>
        </Panel>

        <Panel title="Danger Zone">
          <div style={{ padding: 16 }}>
            <Button
              variant="danger"
              onClick={() => setConfirmUninstallOpen(true)}
              disabled={uninstall.isPending}
            >
              Uninstall
            </Button>
          </div>
        </Panel>
      </div>

      <ConfirmModal
        open={confirmApproveOpen}
        title="Approve permissions"
        body={
          <div>
            <p>Approve the following permissions for this plugin?</p>
            <div style={{ marginTop: 12 }}>
              <RegisterTable
                rows={declaredPermissionRows}
                columns={permissionColumns}
                rowKey={(row) => row.name}
                emptyLabel="None"
                withRowNumbers={false}
              />
            </div>
          </div>
        }
        confirmLabel="Approve"
        error={approvePermissions.isError ? 'Failed to approve permissions' : null}
        onCancel={() => setConfirmApproveOpen(false)}
        onConfirm={handleApprovePermissions}
      />

      <ConfirmModal
        open={confirmUninstallOpen}
        title="Uninstall plugin"
        body={
          <p>
            Uninstall <b>{plugin.displayName || plugin.id}</b>? Agents, triggers, connectors and
            other resources this plugin created stay after uninstall.
          </p>
        }
        confirmLabel="Uninstall"
        destructive
        error={uninstall.isError ? 'Failed to uninstall plugin' : null}
        onCancel={() => setConfirmUninstallOpen(false)}
        onConfirm={handleUninstall}
      />
    </>
  )
}
