import { useMemo, useState } from 'react'
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
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { DualListPicker } from '../../primitives/DualListPicker'
import { Notice } from '../../primitives/Notice'
import { Panel } from '../../primitives/Panel'
import { Tag } from '../../primitives/Tag'
import { Titleblock } from '../../layout/Titleblock'

function getStatusText(plugin: PluginListItem): string {
  if (plugin.ready && !plugin.disabled) {
    return 'Ready'
  }
  if (plugin.disabled) {
    return 'Disabled'
  }
  return plugin.reason || 'Not ready'
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
  const statusVariant =
    statusText === 'Ready' ? 'ok' : statusText === 'Disabled' ? 'err' : 'warn'

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Plugins</b>
          </>
        }
        title={plugin.displayName || plugin.id}
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 16 }}>
        <Panel>
          <div style={{ padding: 16, display: 'grid', gap: 14 }}>
            <div>
              <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                Status
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <Tag variant={statusVariant}>{statusText}</Tag>
                {plugin.message && (
                  <span style={{ color: 'var(--ink-3)', fontSize: '0.9em' }}>
                    {plugin.message}
                  </span>
                )}
              </div>
            </div>

            {plugin.imageRepository && (
              <div>
                <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                  Image
                </div>
                <code style={{ fontSize: '0.9em' }}>
                  {plugin.imageRepository}
                  {plugin.imageDigest && `@${plugin.imageDigest}`}
                </code>
              </div>
            )}

            {plugin.signature && (
              <div>
                <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                  Signature
                </div>
                <div style={{ display: 'grid', gap: 6 }}>
                  <div>
                    {plugin.signature.verified ? (
                      <Tag variant="ok">Verified</Tag>
                    ) : (
                      <Tag variant="err">Unsigned</Tag>
                    )}
                  </div>
                  {plugin.signature.signer && (
                    <div>
                      <span style={{ fontSize: '0.9em', color: 'var(--ink-3)' }}>
                        Signer: <code>{plugin.signature.signer}</code>
                      </span>
                    </div>
                  )}
                  {plugin.signature.checkedAt && (
                    <div>
                      <span style={{ fontSize: '0.9em', color: 'var(--ink-3)' }}>
                        Checked: {new Date(plugin.signature.checkedAt).toLocaleString()}
                      </span>
                    </div>
                  )}
                  {plugin.signature.message && (
                    <div style={{ fontSize: '0.9em', color: 'var(--ink-3)' }}>
                      {plugin.signature.message}
                    </div>
                  )}
                </div>
              </div>
            )}

            <div>
              <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                Enable / Disable
              </div>
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
            </div>
          </div>
        </Panel>

        {hasUnapprovedPermissions && (
          <Panel title="Permissions">
            <div style={{ padding: 16, display: 'grid', gap: 14 }}>
              <div>
                <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                  Declared Permissions
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {plugin.declaredPermissions?.map((perm) => (
                    <Tag key={perm} variant="warn">
                      {perm}
                    </Tag>
                  )) || <span style={{ color: 'var(--ink-3)' }}>None</span>}
                </div>
              </div>
              <div>
                <div style={{ fontSize: '0.75em', color: 'var(--ink-3)', marginBottom: 4 }}>
                  Approved Permissions
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {plugin.approvedPermissions?.map((perm) => (
                    <Tag key={perm} variant="ok">
                      {perm}
                    </Tag>
                  )) || <span style={{ color: 'var(--ink-3)' }}>None</span>}
                </div>
              </div>
              <Button
                variant="primary"
                onClick={() => setConfirmApproveOpen(true)}
                disabled={approvePermissions.isPending}
              >
                Approve permissions
              </Button>
            </div>
          </Panel>
        )}

        <Panel title="Group Access">
          <div style={{ padding: 16, display: 'grid', gap: 14 }}>
            {!groupAccessMode ? (
              <>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {selectedGroupIds.length > 0 ? (
                    selectedGroupIds.map((gid) => {
                      const group = groups.find((g) => g.id === gid)
                      return (
                        <Tag key={gid} variant="ok">
                          {group?.name || gid}
                        </Tag>
                      )
                    })
                  ) : (
                    <span style={{ color: 'var(--ink-3)' }}>No groups selected</span>
                  )}
                </div>
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
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 12 }}>
              {plugin.declaredPermissions?.map((perm) => (
                <Tag key={perm} variant="warn">
                  {perm}
                </Tag>
              ))}
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
