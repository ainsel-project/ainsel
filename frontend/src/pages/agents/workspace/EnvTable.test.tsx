import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AgentEnvVar } from '../../../api/agents'
import type { EnvVar } from '../../../api/agentImages'
import { EnvTable } from './EnvTable'
import { withDraftIds, type DraftEnvVar } from './envRows'

const imageEnv: EnvVar[] = [
  { name: 'A', value: '2' },
  { name: 'B', value: '3' },
]

/** Keeps the draft in state, the way AgentEnvSection does, and records every change. */
function Harness({
  saved,
  imageEnv: image = imageEnv,
  readOnly = false,
  onChange = () => {},
}: {
  saved: AgentEnvVar[]
  imageEnv?: EnvVar[]
  readOnly?: boolean
  onChange?: (next: DraftEnvVar[]) => void
}) {
  const [savedEnv] = useState(() => withDraftIds(saved))
  const [draft, setDraft] = useState<DraftEnvVar[]>(savedEnv)
  return (
    <EnvTable
      agentEnv={draft}
      savedEnv={savedEnv}
      imageEnv={image}
      imageName="img-x"
      readOnly={readOnly}
      onChange={(next) => {
        onChange(next)
        setDraft(next)
      }}
    />
  )
}

/** Entries without the editor's ids, for comparing against onChange payloads. */
const payload = (env: DraftEnvVar[]) =>
  env.map(({ name, value, secret }) => ({ name, value, secret }))

function rowNames(): string[] {
  return screen
    .getAllByRole('row')
    .slice(1)
    .map((row) => {
      const input = row.querySelector<HTMLInputElement>('input[aria-label="Variable name"]')
      return input ? input.value : (row.querySelector('code')?.textContent ?? '')
    })
}

describe('EnvTable', () => {
  it('disables image rows and labels their lock with the image name', () => {
    render(<Harness saved={[]} imageEnv={[{ name: 'C', value: 'z' }]} />)

    expect(screen.getByTitle('From image img-x')).toBeInTheDocument()
    expect(screen.getByLabelText('Image value')).toBeDisabled()
  })

  it('shows the overridden image value on an override row', () => {
    render(<Harness saved={[{ name: 'A', value: '1' }]} />)

    expect(screen.getByTitle('Overrides the image value 2 (img-x)')).toHaveTextContent('!')
    expect(screen.getByText('2 from image')).toBeInTheDocument()
  })

  it('never renders an overridden secret image value', () => {
    const { container } = render(
      <Harness
        saved={[{ name: 'S', value: '' }]}
        imageEnv={[{ name: 'S', value: 'hunter2', secret: true }]}
      />,
    )

    expect(screen.getByTitle(/••••••••/)).toBeInTheDocument()
    expect(container.innerHTML).not.toContain('hunter2')
  })

  it('shows a plain image value under an agent entry that is secret', () => {
    render(<Harness saved={[{ name: 'A', value: '1', secret: true }]} />)

    expect(screen.getByTitle('Overrides the image value 2 (img-x)')).toBeInTheDocument()
    expect(screen.getByText('2 from image')).toBeInTheDocument()
  })

  it('resets an override by removing the agent entry', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <Harness
        saved={[
          { name: 'A', value: '1' },
          { name: 'C', value: 'x' },
        ]}
        onChange={onChange}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Reset to image' }))

    expect(payload(onChange.mock.lastCall?.[0] as DraftEnvVar[])).toEqual([
      { name: 'C', value: 'x' },
    ])
  })

  it('overrides an image row by appending the image value as an agent entry', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness saved={[{ name: 'A', value: '1' }]} onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: 'Override' }))

    expect(payload(onChange.mock.lastCall?.[0] as DraftEnvVar[])).toEqual([
      { name: 'A', value: '1', secret: undefined },
      { name: 'B', value: '3', secret: false },
    ])
  })

  it('overriding a secret image row appends an empty secret entry', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { container } = render(
      <Harness
        saved={[]}
        imageEnv={[{ name: 'S', value: 'hunter2', secret: true }]}
        onChange={onChange}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Override' }))

    expect(payload(onChange.mock.lastCall?.[0] as DraftEnvVar[])).toEqual([
      { name: 'S', value: '', secret: true },
    ])
    expect(container.innerHTML).not.toContain('hunter2')
  })

  it('shows no action buttons or editable inputs for a managed agent', () => {
    render(<Harness saved={[{ name: 'A', value: '1' }]} readOnly />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Variable value')).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: 'Secret' })).toBeDisabled()
  })

  it('has four columns: name, value, source, actions', () => {
    render(<Harness saved={[{ name: 'A', value: '1' }]} />)

    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent?.trim())
    expect(headers).toEqual(['Name', 'Value', 'Source', ''])
  })

  it('orders rows by the saved env: overrides, agent entries, then image rows', () => {
    render(
      <Harness
        saved={[
          { name: 'Z', value: '1' },
          { name: 'B', value: '2' },
          { name: 'A', value: '3' },
        ]}
        imageEnv={[
          { name: 'A', value: '2' },
          { name: 'Z', value: '2' },
          { name: 'C', value: '2' },
        ]}
      />,
    )

    expect(rowNames()).toEqual(['A', 'Z', 'B', 'C'])
  })

  it('keeps a new row last among agent entries until the next save', async () => {
    const user = userEvent.setup()
    render(
      <Harness
        saved={[
          { name: 'Z', value: '1' },
          { name: 'B', value: '2' },
        ]}
        imageEnv={[
          { name: 'Z', value: '2' },
          { name: 'C', value: '2' },
        ]}
      />,
    )
    expect(rowNames()).toEqual(['Z', 'B', 'C'])

    await user.click(screen.getByRole('button', { name: /add variable/i }))
    expect(rowNames()).toEqual(['Z', 'B', '', 'C'])

    // Typing a name that sorts before B does not move the row.
    await user.type(screen.getAllByLabelText('Variable name')[2], 'A')
    expect(rowNames()).toEqual(['Z', 'B', 'A', 'C'])
  })
})

describe('EnvTable plain variant', () => {
  /** Keeps the list in state, the way the image form's adapter does. */
  function PlainHarness({
    env,
    readOnly = false,
    onChange = () => {},
  }: {
    env: AgentEnvVar[]
    readOnly?: boolean
    onChange?: (next: DraftEnvVar[]) => void
  }) {
    const [draft, setDraft] = useState<DraftEnvVar[]>(() => withDraftIds(env))
    return (
      <EnvTable
        variant="plain"
        agentEnv={draft}
        readOnly={readOnly}
        onChange={(next) => {
          onChange(next)
          setDraft(next)
        }}
      />
    )
  }

  it('has three columns — name, value, actions — and no Source column', () => {
    render(<PlainHarness env={[{ name: 'A', value: '1' }]} />)

    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent?.trim())
    expect(headers).toEqual(['Name', 'Value', ''])
  })

  it('keeps entry order and offers only Remove as an action', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <PlainHarness
        env={[
          { name: 'Z', value: '1' },
          { name: 'A', value: '2' },
        ]}
        onChange={onChange}
      />,
    )

    // Entry order, not mergeEnv's alphabetical order.
    const names = () =>
      screen.getAllByLabelText('Variable name').map((i) => (i as HTMLInputElement).value)
    expect(names()).toEqual(['Z', 'A'])
    expect(screen.queryByRole('button', { name: /override/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /reset to image/i })).not.toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: 'Remove variable' })[0])
    expect(payload(onChange.mock.lastCall?.[0] as DraftEnvVar[])).toEqual([
      { name: 'A', value: '2', secret: undefined },
    ])
  })

  it('appends an empty row on add', async () => {
    const user = userEvent.setup()
    render(<PlainHarness env={[{ name: 'A', value: '1' }]} />)

    await user.click(screen.getByRole('button', { name: /add variable/i }))
    expect(
      screen.getAllByLabelText('Variable name').map((i) => (i as HTMLInputElement).value),
    ).toEqual(['A', ''])
  })

  it('masks a secret value behind a password field', () => {
    render(<PlainHarness env={[{ name: 'S', value: 'hunter2', secret: true }]} />)

    // Same rule as the old image env editor: the field is a password input and
    // an existing secret arrives empty from the hub — submitting it unchanged
    // keeps the stored value.
    const value = screen.getByLabelText('Variable value')
    expect(value).toHaveAttribute('type', 'password')
    expect(value).toHaveAttribute('placeholder', '••••••••')
  })

  it('offers no controls when read-only', () => {
    render(<PlainHarness env={[{ name: 'A', value: '1' }]} readOnly />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Variable name')).toBeDisabled()
    expect(screen.getByLabelText('Variable value')).toBeDisabled()
  })
})
