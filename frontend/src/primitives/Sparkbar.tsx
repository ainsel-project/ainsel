/** Bars a spark draws at most. Longer series are downsampled so the spark can
 *  never be wider than its card (issue #340). */
const DEFAULT_MAX_BARS = 24

/**
 * Reduce `values` to at most `maxBars` buckets, each the max of the source
 * points it covers. Buckets are contiguous, non-empty, and cover the whole
 * series in order, so the last point always lands in the last bar.
 */
function downsample(values: number[], maxBars: number): number[] {
  const n = values.length
  const buckets = Math.max(1, maxBars)
  if (n <= buckets) return values
  const out: number[] = []
  for (let b = 0; b < buckets; b++) {
    const from = Math.floor((b * n) / buckets)
    const to = Math.floor(((b + 1) * n) / buckets)
    let peak = values[from]
    for (let i = from + 1; i < to; i++) if (values[i] > peak) peak = values[i]
    out.push(peak)
  }
  return out
}

type SparkbarProps = {
  values: number[]
  alert?: boolean
  /** Cap on the number of bars drawn (default 24). Series longer than this are
   *  downsampled to one bar per bucket, each bar the max of its bucket. */
  maxBars?: number
}

export function Sparkbar({ values, alert, maxBars = DEFAULT_MAX_BARS }: SparkbarProps) {
  const series = downsample(values, maxBars)
  const max = series.reduce((m, v) => (v > m ? v : m), 0)
  const cls = alert ? 'spark alert' : 'spark'
  return (
    <div className={cls}>
      {series.map((v, i) => {
        const pct = max === 0 ? 0 : Math.round((v / max) * 100)
        return <span key={i} style={{ height: `${pct}%` }} />
      })}
    </div>
  )
}
