import { Link } from 'react-router-dom'
import {
  usePlugins,
  usePluginCallerContext,
  useDisablePlugin,
  useEnablePlugin,
  type PluginListItem,
} from '../../api/plugins'
import { Button } from '../../primitives/Button'
import { Notice } from '../../primitives/Notice'
import { Panel } from '../../primitives/Panel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
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

export function PluginList() {
  const { data: plugins, isLoading, error } = usePlugins()
  const { data: callerContext } = usePluginCallerContext()
  const disable = useDisablePlugin()
  const enable = useEnablePlugin()

  const isAdmin = callerContext?.isAdmin === true

  if (!isAdmin) {
    return (
      <>
        <Titleblock
          crumbs={
            <>
              Admin / <b>Plugins</b>
            </>
          }
          title={
            <>
              Plugin <em>Registry</em>
            </>
          }
        />
        <div style={{ padding: '28px 32px' }}>
          <Notice variant="info">Plugins are managed by administrators.</Notice>
        </div>
      </>
    )
  }

  const columns: readonly Column<PluginListItem>[] = [
    {
      key: 'id',
      header: 'ID',
      cell: (p) => <Link to={`/plugins/${encodeURIComponent(p.id)}`}>{p.id}</Link>,
    },
    {
      key: 'name',
      header: 'Name',
      cell: (p) => p.displayName || '—',
    },
    {
      key: 'version',
      header: 'Version',
      cell: (p) => <span className="num">{p.version || '—'}</span>,
    },
    {
      key: 'status',
      header: 'Status',
      cell: (p) => {
        const statusText = getStatusText(p)
        const variant =
          statusText === 'Ready' ? 'ok' : statusText === 'Disabled' ? 'err' : 'warn'
        return <Tag variant={variant}>{statusText}</Tag>
      },
    },
    {
      key: 'signed',
      header: 'Signed',
      cell: (p) =>
        p.signature?.verified ? (
          <span title={`Signer: ${p.signature.signer || '(unknown)'}`}>
            {p.signature.signer || 'verified'}
          </span>
        ) : (
          'unsigned'
        ),
    },
    {
      key: 'permissions',
      header: 'Permissions',
      cell: (p) => {
        const approved = p.approvedPermissions?.length ?? 0
        const declared = p.declaredPermissions?.length ?? 0
        return `${approved}/${declared}`
      },
    },
    {
      key: 'actions',
      header: '',
      width: 120,
      align: 'right',
      cell: (p) => (
        <div style={{ display: 'flex', gap: 6 }}>
          <Button
            variant="ghost"
            size="sm"
            disabled={disable.isPending || enable.isPending}
            onClick={() => {
              if (p.disabled) {
                enable.mutate(p.id)
              } else {
                disable.mutate(p.id)
              }
            }}
            aria-label={p.disabled ? `Enable ${p.id}` : `Disable ${p.id}`}
          >
            {p.disabled ? 'Enable' : 'Disable'}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Plugins</b>
          </>
        }
        title={
          <>
            Plugin <em>Registry</em>
          </>
        }
      />
      <div style={{ padding: '28px 32px' }}>
        <Panel className="cropped">
          {isLoading ? (
            <div className="label" style={{ padding: 14 }}>
              Loading…
            </div>
          ) : error ? (
            <div className="label" style={{ padding: 14, color: 'var(--signal)' }}>
              Failed to load plugins.
            </div>
          ) : plugins ? (
            <RegisterTable
              rows={plugins}
              columns={columns}
              rowKey={(p) => p.id}
              emptyLabel="No plugins found."
            />
          ) : null}
        </Panel>
      </div>
    </>
  )
}
