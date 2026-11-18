import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToolList } from './ToolList'
import type { ToolFormValue } from './ImageDetail.types'

function makeNative(name: string): ToolFormValue {
  return { name, kind: 'shell', isNew: false, description: '', examples: [] }
}
function makeMCP(
  server: string,
  toolName: string,
  opts: Partial<ToolFormValue> = {},
): ToolFormValue {
  return {
    name: `mcp__${server}__${toolName}`,
    kind: 'mcp',
    mcpSource: server,
    isNew: false,
    description: '',
    examples: [],
    ...opts,
  }
}

const tools = [
  makeNative('run_shell'),
  makeMCP('example-mcp', 'list'),
  makeMCP('example-mcp', 'add'),
  makeMCP('example-mcp', 'delete', { isNew: true }),
]

const noop = () => {}

function renderList(props: Partial<Parameters<typeof ToolList>[0]> = {}) {
  return render(
    <ToolList
      tools={tools}
      activeSource="all"
      selectedIndex={null}
      onSelect={noop}
      onAdd={noop}
      {...props}
    />,
  )
}

describe('ToolList', () => {
  it('shows all tools when activeSource is "all"', () => {
    renderList()
    expect(screen.getByText('run_shell')).toBeInTheDocument()
    expect(screen.getByText('list')).toBeInTheDocument()
    expect(screen.getByText('add')).toBeInTheDocument()
  })

  it('filters to native tools when activeSource is "native"', () => {
    renderList({ activeSource: 'native' })
    expect(screen.getByText('run_shell')).toBeInTheDocument()
    expect(screen.queryByText('list')).not.toBeInTheDocument()
  })

  it('filters to a specific MCP server when activeSource is a server name', () => {
    renderList({ activeSource: 'example-mcp' })
    expect(screen.queryByText('run_shell')).not.toBeInTheDocument()
    expect(screen.getByText('list')).toBeInTheDocument()
    expect(screen.getByText('add')).toBeInTheDocument()
  })

  it('offers no enable/disable switch — the image list is a catalog, not a grant', () => {
    renderList()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /all on/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /all off/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/· off/)).not.toBeInTheDocument()
  })

  it('offers no enable/disable switch for a large source either', () => {
    const manyTools = Array.from({ length: 16 }, (_, i) => makeMCP('big', `tool${i}`))
    renderList({ tools: manyTools, activeSource: 'big' })
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /all on/i })).not.toBeInTheDocument()
  })

  it('shows "new" badge on isNew tools', () => {
    renderList()
    expect(screen.getByText('new')).toBeInTheDocument()
  })

  it('selects a tool by clicking its row', async () => {
    const onSelect = vi.fn()
    renderList({ onSelect })
    await userEvent.click(screen.getByText('list'))
    expect(onSelect).toHaveBeenCalledWith(1)
  })

  it('search filters tool names within the active source', async () => {
    renderList({ activeSource: 'example-mcp' })
    const search = screen.getByRole('textbox')
    await userEvent.type(search, 'lis')
    expect(screen.getByText('list')).toBeInTheDocument()
    expect(screen.queryByText('add')).not.toBeInTheDocument()
  })
})
