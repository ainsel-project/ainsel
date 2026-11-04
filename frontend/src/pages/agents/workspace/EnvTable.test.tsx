import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EnvTable } from './EnvTable'

const imageEnv = [
  { name: 'A', value: '2' },
  { name: 'B', value: '3' },
]

function renderTable(
  props: Partial<Parameters<typeof EnvTable>[0]> & {
    agentEnv: { name: string; value: string; secret?: boolean }[]
  },
) {
  const onChange = vi.fn()
  render(
    <EnvTable
      imageEnv={imageEnv}
      imageName="img-x"
      readOnly={false}
      onChange={onChange}
      {...props}
    />,
  )
  return { onChange }
}

describe('EnvTable', () => {
  it('disables image rows and labels their lock with the image name', () => {
    renderTable({ agentEnv: [], imageEnv: [{ name: 'C', value: 'z' }] })

    expect(screen.getByTitle('From image img-x')).toBeInTheDocument()
    expect(screen.getByLabelText('Image value')).toBeDisabled()
  })

  it('shows the overridden image value on an override row', () => {
    renderTable({ agentEnv: [{ name: 'A', value: '1' }] })

    expect(screen.getByTitle('Overrides the image value 2 (img-x)')).toHaveTextContent('!')
    expect(screen.getByText('2 from image')).toBeInTheDocument()
  })

  it('never renders an overridden secret image value', () => {
    const { container } = render(
      <EnvTable
        agentEnv={[{ name: 'S', value: '' }]}
        imageEnv={[{ name: 'S', value: 'hunter2', secret: true }]}
        imageName="img-x"
        readOnly={false}
        onChange={vi.fn()}
      />,
    )

    expect(screen.getByTitle(/••••••••/)).toBeInTheDocument()
    expect(container.innerHTML).not.toContain('hunter2')
  })

  it('resets an override by removing the agent entry', async () => {
    const user = userEvent.setup()
    const { onChange } = renderTable({
      agentEnv: [
        { name: 'A', value: '1' },
        { name: 'C', value: 'x' },
      ],
    })

    await user.click(screen.getByRole('button', { name: 'Reset to image' }))

    expect(onChange).toHaveBeenCalledWith([{ name: 'C', value: 'x' }])
  })

  it('overrides an image row by appending the image value as an agent entry', async () => {
    const user = userEvent.setup()
    const { onChange } = renderTable({ agentEnv: [{ name: 'A', value: '1' }] })

    await user.click(screen.getByRole('button', { name: 'Override' }))

    expect(onChange).toHaveBeenCalledWith([
      { name: 'A', value: '1' },
      { name: 'B', value: '3', secret: false },
    ])
  })

  it('shows no action buttons or editable inputs for a managed agent', () => {
    renderTable({ agentEnv: [{ name: 'A', value: '1' }], readOnly: true })

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Variable value')).toBeDisabled()
  })
})
