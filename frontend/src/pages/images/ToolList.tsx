import { useState, useEffect } from 'react'
import type { ToolFormValue } from './ImageDetail.types'
import type { Source } from './ToolSourceSidebar'

type Props = {
  tools: ToolFormValue[]
  activeSource: Source
  selectedIndex: number | null
  onSelect: (index: number) => void
  onAdd: () => void
}

function shortName(tool: ToolFormValue): string {
  if (tool.kind !== 'mcp' || !tool.mcpSource) return tool.name
  const prefix = `mcp__${tool.mcpSource}__`
  return tool.name.startsWith(prefix) ? tool.name.slice(prefix.length) : tool.name
}

type IndexedTool = { t: ToolFormValue; i: number }

function filterTools(tools: ToolFormValue[], activeSource: Source, query: string): IndexedTool[] {
  const q = query.trim().toLowerCase()
  return tools
    .map((t, i) => ({ t, i }))
    .filter(({ t }) => {
      if (activeSource === 'all') return true
      if (activeSource === 'native') return t.kind !== 'mcp'
      return t.kind === 'mcp' && t.mcpSource === activeSource
    })
    .filter(({ t }) => q === '' || shortName(t).toLowerCase().includes(q))
}

export function ToolList({ tools, activeSource, selectedIndex, onSelect, onAdd }: Props) {
  const [query, setQuery] = useState('')

  useEffect(() => {
    setQuery('')
  }, [activeSource])

  const displayed = filterTools(tools, activeSource, query)

  const groups: { label: string; items: IndexedTool[] }[] = []
  if (activeSource === 'all') {
    const native = displayed.filter(({ t }) => t.kind !== 'mcp')
    if (native.length > 0) groups.push({ label: 'Native', items: native })
    const servers = new Map<string, IndexedTool[]>()
    for (const item of displayed) {
      if (item.t.kind === 'mcp' && item.t.mcpSource) {
        if (!servers.has(item.t.mcpSource)) servers.set(item.t.mcpSource, [])
        servers.get(item.t.mcpSource)!.push(item)
      }
    }
    for (const [server, items] of servers) {
      groups.push({ label: server, items })
    }
  } else {
    groups.push({ label: activeSource, items: displayed })
  }

  const headerLabel =
    activeSource === 'all' ? 'All Tools' : activeSource === 'native' ? 'Native' : activeSource

  return (
    <div className="md-left">
      <header className="md-bar">
        <h4>{headerLabel}</h4>
        <div className="md-bar-grow" />
        {activeSource === 'all' || activeSource === 'native' ? (
          <button type="button" className="btn btn-sm btn-primary" onClick={onAdd}>
            ＋ Add
          </button>
        ) : null}
      </header>

      <div className="tool-search">
        <input
          type="text"
          className="input"
          placeholder={`Filter ${headerLabel.toLowerCase()}…`}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>

      <div className="tool-list">
        {displayed.length === 0 ? (
          <div className="label" style={{ padding: 16, color: 'var(--ink-3)' }}>
            {query ? 'No tools match.' : 'No tools yet.'}
          </div>
        ) : (
          groups.map((group) => (
            <div key={group.label}>
              {activeSource === 'all' ? (
                <div className="tool-section-head">
                  <span>{group.label}</span>
                  <span>{group.items.length}</span>
                </div>
              ) : null}
              {group.items.map(({ t, i }) => {
                const name = shortName(t)
                return (
                  <div
                    key={i}
                    role="button"
                    tabIndex={0}
                    className={i === selectedIndex ? 'tool-row active' : 'tool-row'}
                    onClick={() => onSelect(i)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        onSelect(i)
                      }
                    }}
                    aria-pressed={i === selectedIndex}
                    aria-label={`Select tool ${name}`}
                  >
                    <div style={{ minWidth: 0 }}>
                      <div className="tool-name">
                        {name}
                        {t.isNew ? <span className="tool-new-badge">new</span> : null}
                      </div>
                      <div className="tool-meta">{t.kind}</div>
                    </div>
                  </div>
                )
              })}
            </div>
          ))
        )}
      </div>
    </div>
  )
}
