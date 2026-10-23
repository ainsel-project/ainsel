import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Button } from './primitives'
import { registerHost } from './host'

describe('primitives', () => {

  it('throws error before registerHost is called', () => {
    const Component = () => {
      return <Button>Click me</Button>
    }

    expect(() => {
      render(<Component />)
    }).toThrow('@ainsel/plugin-sdk: host primitives not registered')
  })

  it('renders the registered host component after registerHost', () => {
    const MockButton = ({ children, ...props }: any) => (
      <button data-testid="mock-button" {...props}>
        {children}
      </button>
    )

    registerHost({
      primitives: {
        Button: MockButton,
        Panel: () => null,
        Tabs: () => null,
        Tag: () => null,
        Notice: () => null,
        Field: () => null,
        Input: () => null,
        Select: () => null,
        Textarea: () => null,
        Dot: () => null,
        RegisterTable: () => null,
        Markdown: () => null,
        TimeSeriesChart: () => null,
        Sparkbar: () => null,
      },
    })

    const Component = () => {
      return <Button>Click me</Button>
    }

    render(<Component />)
    expect(screen.getByTestId('mock-button')).toBeDefined()
    expect(screen.getByText('Click me')).toBeDefined()
  })

  it('forwards props to the host component', () => {
    const MockButton = ({ testProp, ...props }: any) => (
      <button data-testid="mock-button" data-test-prop={testProp} {...props} />
    )

    registerHost({
      primitives: {
        Button: MockButton,
        Panel: () => null,
        Tabs: () => null,
        Tag: () => null,
        Notice: () => null,
        Field: () => null,
        Input: () => null,
        Select: () => null,
        Textarea: () => null,
        Dot: () => null,
        RegisterTable: () => null,
        Markdown: () => null,
        TimeSeriesChart: () => null,
        Sparkbar: () => null,
      },
    })

    const Component = () => {
      return <Button testProp="hello">Test</Button>
    }

    render(<Component />)
    const button = screen.getByTestId('mock-button')
    expect(button.getAttribute('data-test-prop')).toBe('hello')
  })
})
