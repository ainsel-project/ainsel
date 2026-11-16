import { render } from '@testing-library/react'
import { Sparkbar } from './Sparkbar'

describe('Sparkbar', () => {
  it('renders one bar per value', () => {
    const { container } = render(<Sparkbar values={[1, 2, 3, 4]} />)
    const root = container.firstChild as HTMLElement
    expect(root).toHaveClass('spark')
    expect(root.children).toHaveLength(4)
  })

  it('scales bar heights as percentage of max', () => {
    const { container } = render(<Sparkbar values={[5, 10]} />)
    const bars = (container.firstChild as HTMLElement).children
    expect((bars[0] as HTMLElement).style.height).toBe('50%')
    expect((bars[1] as HTMLElement).style.height).toBe('100%')
  })

  it('renders empty placeholder when values is empty', () => {
    const { container } = render(<Sparkbar values={[]} />)
    const root = container.firstChild as HTMLElement
    expect(root.children).toHaveLength(0)
  })

  it('handles all-zero values (no NaN)', () => {
    const { container } = render(<Sparkbar values={[0, 0, 0]} />)
    const bars = (container.firstChild as HTMLElement).children
    expect((bars[0] as HTMLElement).style.height).toBe('0%')
  })

  it('applies alert class when alert prop set', () => {
    const { container } = render(<Sparkbar values={[1, 2]} alert />)
    expect(container.firstChild).toHaveClass('spark', 'alert')
  })

  it('downsamples series longer than maxBars to one bar per bucket', () => {
    const values = Array.from({ length: 50 }, (_, i) => i)
    const { container } = render(<Sparkbar values={values} maxBars={10} />)
    expect((container.firstChild as HTMLElement).children).toHaveLength(10)
  })

  it('caps at 24 bars by default', () => {
    const values = Array.from({ length: 100 }, () => 1)
    const { container } = render(<Sparkbar values={values} />)
    expect((container.firstChild as HTMLElement).children).toHaveLength(24)
  })

  it('does not downsample when the series fits maxBars', () => {
    const { container } = render(<Sparkbar values={[1, 2, 3]} maxBars={24} />)
    expect((container.firstChild as HTMLElement).children).toHaveLength(3)
  })

  it('keeps the last point in the last bar', () => {
    // Only the final point is non-zero; its bucket must be the final bar.
    const values = Array.from({ length: 49 }, () => 0).concat(7)
    const { container } = render(<Sparkbar values={values} maxBars={5} />)
    const bars = (container.firstChild as HTMLElement).children
    expect((bars[4] as HTMLElement).style.height).toBe('100%')
    for (let i = 0; i < 4; i++) {
      expect((bars[i] as HTMLElement).style.height).toBe('0%')
    }
  })

  it('each bar is the max of its bucket', () => {
    // Buckets [1, 8] and [2, 3] -> peaks 8 and 3; heights scale off the peak 8.
    const { container } = render(<Sparkbar values={[1, 8, 2, 3]} maxBars={2} />)
    const bars = (container.firstChild as HTMLElement).children
    expect((bars[0] as HTMLElement).style.height).toBe('100%')
    expect((bars[1] as HTMLElement).style.height).toBe('38%')
  })

  it('downsampled series still scales off the series max', () => {
    const values = Array.from({ length: 10 }, (_, i) => (i === 9 ? 10 : 1))
    const { container } = render(<Sparkbar values={values} maxBars={5} />)
    const bars = (container.firstChild as HTMLElement).children
    expect((bars[4] as HTMLElement).style.height).toBe('100%')
    expect((bars[0] as HTMLElement).style.height).toBe('10%')
  })
})
