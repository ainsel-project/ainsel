import { render, screen } from '@testing-library/react'
import { useForm } from 'react-hook-form'
import { ToolEditor } from './ToolEditor'
import type { ImageFormValues } from './ImageDetail.types'

function Wrapper({
  toolIndex,
  tools,
}: {
  toolIndex: number | null
  tools: ImageFormValues['tools']
}) {
  const { control, register, setValue, watch } = useForm<ImageFormValues>({
    defaultValues: {
      tools,
      displayName: '',
      description: '',
      imageURL: '',
      env: [],
      mcpServers: [],
    },
  })
  return (
    <ToolEditor
      toolIndex={toolIndex}
      tools={watch('tools')}
      control={control}
      register={register}
      setValue={setValue}
      watch={watch}
      onRemove={() => {}}
    />
  )
}

describe('ToolEditor', () => {
  it('shows full MCP tool name for mcp tools', () => {
    render(
      <Wrapper
        toolIndex={0}
        tools={[
          {
            name: 'mcp__example-mcp__list',
            kind: 'mcp',
            mcpSource: 'example-mcp',
            isNew: false,
            description: 'Lists memories',
            examples: [],
          },
        ]}
      />,
    )
    expect(screen.getByText('mcp__example-mcp__list')).toBeInTheDocument()
  })

  it('offers no enable/disable switch — the image catalog grants nothing', () => {
    render(
      <Wrapper
        toolIndex={0}
        tools={[
          {
            name: 'mcp__example-mcp__list',
            kind: 'mcp',
            mcpSource: 'example-mcp',
            isNew: false,
            description: '',
            examples: [],
          },
        ]}
      />,
    )
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByText('Enabled')).not.toBeInTheDocument()
    expect(screen.queryByText('Disabled')).not.toBeInTheDocument()
    expect(screen.getByText(/granted tools on its own Tools page/i)).toBeInTheDocument()
  })

  it('shows edit form for native tools', () => {
    render(
      <Wrapper
        toolIndex={0}
        tools={[
          {
            name: 'run_shell',
            kind: 'shell',
            isNew: false,
            description: 'Runs a shell command',
            examples: [],
          },
        ]}
      />,
    )
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument()
  })
})
