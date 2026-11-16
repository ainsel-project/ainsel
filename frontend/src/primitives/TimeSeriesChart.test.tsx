import { render } from '@testing-library/react'
import { TimeSeriesChart, type SeriesStyle } from './TimeSeriesChart'

const styles: SeriesStyle[] = [
  { name: 'a', color: 'var(--ink)', label: 'Series A' },
  { name: 'b', color: 'var(--signal)', label: 'Series B', dashed: true },
]

describe('TimeSeriesChart', () => {
  it('renders a path per styled series', () => {
    const series = [
      {
        name: 'a',
        points: [
          { timestamp: '2026-05-20T00:00:00Z', value: 1 },
          { timestamp: '2026-05-20T01:00:00Z', value: 5 },
        ],
      },
      {
        name: 'b',
        points: [
          { timestamp: '2026-05-20T00:00:00Z', value: 2 },
          { timestamp: '2026-05-20T01:00:00Z', value: 3 },
        ],
      },
    ]
    const { container } = render(<TimeSeriesChart series={series} styles={styles} />)
    const paths = container.querySelectorAll('svg path')
    expect(paths).toHaveLength(2)
  })

  it('renders legend swatch + label per style', () => {
    const { container } = render(<TimeSeriesChart series={[]} styles={styles} />)
    const entries = container.querySelectorAll('.chart-legend .series')
    expect(entries).toHaveLength(2)
  })

  it('handles empty series gracefully', () => {
    const { container } = render(<TimeSeriesChart series={[]} styles={styles} />)
    expect(container.querySelector('svg')).not.toBeNull()
  })

  const axisText = (container: HTMLElement) =>
    [...container.querySelectorAll('svg text')].map((t) => t.textContent)

  const peakSeries = [
    {
      name: 'a',
      points: [
        { timestamp: '2026-05-20T00:00:00Z', value: 1 },
        { timestamp: '2026-05-20T01:00:00Z', value: 42 },
      ],
    },
  ]

  // The axis max is the only number on the chart, and a point is a count per
  // bucket on the hub's records but a rate on Prometheus — so when the caller
  // passes a unit, the number carries it (#341).
  it('labels the axis max with the unit it is given', () => {
    const { container } = render(
      <TimeSeriesChart series={peakSeries} styles={styles} unit="events / 10m" />,
    )
    expect(axisText(container)).toContain('42 events / 10m')
    expect(container.querySelector('svg')).toHaveAttribute(
      'aria-label',
      'Time series chart, max 42 events / 10m',
    )
  })

  // `unit` is optional and additive: a caller that does not pass one — any
  // third-party plugin page — keeps the bare number and the label it had.
  it('renders the number alone when no unit is given', () => {
    const { container } = render(<TimeSeriesChart series={peakSeries} styles={styles} />)
    expect(axisText(container)).toContain('42')
    expect(container.querySelector('svg')).toHaveAttribute('aria-label', 'Time series chart')
  })

  // A rate arrives unrounded from Prometheus; the axis rounds it.
  it('rounds a fractional max instead of printing the raw float', () => {
    const { container } = render(
      <TimeSeriesChart
        series={[
          {
            name: 'a',
            points: [{ timestamp: '2026-05-20T00:00:00Z', value: 0.8333333333333334 }],
          },
        ]}
        styles={styles}
        unit="events / period"
      />,
    )
    expect(axisText(container)).toContain('0.83 events / period')
  })

  it('draws no axis label for an all-zero window', () => {
    const { container } = render(
      <TimeSeriesChart
        series={[{ name: 'a', points: [{ timestamp: '2026-05-20T00:00:00Z', value: 0 }] }]}
        styles={styles}
        unit="events / 10m"
      />,
    )
    expect(container.querySelectorAll('svg text')).toHaveLength(0)
  })
})
