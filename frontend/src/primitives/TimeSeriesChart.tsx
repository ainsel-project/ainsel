import { formatChartValue, type TimeseriesSeries } from '../api/observability'

export type SeriesStyle = {
  name: string
  color: string
  label: string
  dashed?: boolean
}

type TimeSeriesChartProps = {
  series: TimeseriesSeries[]
  styles: readonly SeriesStyle[]
  height?: number
  width?: number
  ariaLabel?: string
  // Names what one point holds (see chartUnit). Optional so existing callers —
  // plugin pages included — render exactly as before.
  unit?: string
}

const PADDING = { top: 16, bottom: 28, left: 6, right: 6 }

export function TimeSeriesChart({
  series,
  styles,
  height = 200,
  width = 800,
  ariaLabel = 'Time series chart',
  unit,
}: TimeSeriesChartProps) {
  const allPoints = series.flatMap((s) => s.points)
  const maxValue = allPoints.reduce((m, p) => (p.value > m ? p.value : m), 0)
  const innerW = width - PADDING.left - PADDING.right
  const innerH = height - PADDING.top - PADDING.bottom

  const labeled = series
    .map((s) => {
      const style = styles.find((st) => st.name === s.name)
      return style ? { series: s, style } : null
    })
    .filter((x): x is { series: TimeseriesSeries; style: SeriesStyle } => x !== null)

  const pathFor = (points: typeof allPoints): string => {
    if (points.length === 0) return ''
    const n = points.length
    return points
      .map((p, i) => {
        const x = PADDING.left + (n === 1 ? innerW / 2 : (i / (n - 1)) * innerW)
        const y = PADDING.top + innerH - (maxValue > 0 ? (p.value / maxValue) * innerH : 0)
        return `${i === 0 ? 'M' : 'L'} ${x.toFixed(2)} ${y.toFixed(2)}`
      })
      .join(' ')
  }

  const firstTs = allPoints[0]?.timestamp
  const lastTs = allPoints[allPoints.length - 1]?.timestamp
  const midTs = allPoints[Math.floor(allPoints.length / 2)]?.timestamp
  const hh = (ts: string | undefined) => ts?.slice(11, 16) ?? '—'

  // The max is the one number on the axis; it carries the unit whenever the
  // caller has one, so a count per bucket never reads as a rate.
  const axisLabel = unit ? `${formatChartValue(maxValue)} ${unit}` : formatChartValue(maxValue)
  // Only a chart that states a unit extends its accessible name: an existing
  // plugin page keeps the label it had.
  const label = unit && maxValue > 0 ? `${ariaLabel}, max ${axisLabel}` : ariaLabel

  return (
    <div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        style={{ display: 'block', width: '100%', height }}
        role="img"
        aria-label={label}
      >
        {/* Grid */}
        {[0, 0.25, 0.5, 0.75, 1].map((p, i) => (
          <line
            key={i}
            x1={0}
            y1={PADDING.top + innerH * p}
            x2={width}
            y2={PADDING.top + innerH * p}
            stroke="rgba(20,17,13,0.08)"
            strokeWidth={1}
          />
        ))}
        {/* X axis */}
        <line
          x1={0}
          y1={PADDING.top + innerH}
          x2={width}
          y2={PADDING.top + innerH}
          stroke="var(--ink)"
          strokeWidth={1}
        />
        {/* Series */}
        {labeled.map(({ series: s, style }) => (
          <path
            key={s.name}
            d={pathFor(s.points)}
            fill="none"
            stroke={style.color}
            strokeWidth={1.5}
            strokeDasharray={style.dashed ? '4 3' : undefined}
          />
        ))}
        {/* Y axis max label */}
        {maxValue > 0 ? (
          <text x={6} y={PADDING.top - 4} fontFamily="var(--mono)" fontSize={9} fill="var(--ink-4)">
            {axisLabel}
          </text>
        ) : null}
      </svg>
      <div
        className="label"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          padding: '8px 14px',
        }}
      >
        <span>{hh(firstTs)}</span>
        <span>{hh(midTs)}</span>
        <span>{hh(lastTs)}</span>
      </div>
      <div className="chart-legend">
        {styles.map((s) => (
          <span key={s.name} className="series">
            <span
              className="swatch"
              style={{
                background: s.dashed
                  ? `repeating-linear-gradient(to right, ${s.color}, ${s.color} 4px, transparent 4px, transparent 7px)`
                  : s.color,
              }}
            />
            {s.label}
          </span>
        ))}
      </div>
    </div>
  )
}
