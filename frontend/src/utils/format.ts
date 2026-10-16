export function abbreviateNumber(n: number, fractionDigits = 1): string {
  if (!Number.isFinite(n)) return String(n)
  const abs = Math.abs(n)
  const sign = n < 0 ? '-' : ''
  if (abs < 1000) return `${sign}${abs}`
  if (abs < 1_000_000)
    return `${sign}${(abs / 1000).toFixed(fractionDigits).replace(/\.0$/, '')}K`
  if (abs < 1_000_000_000)
    return `${sign}${(abs / 1_000_000).toFixed(fractionDigits).replace(/\.0$/, '')}M`
  return `${sign}${(abs / 1_000_000_000).toFixed(fractionDigits).replace(/\.0$/, '')}B`
}

export function formatPercent(p: number, fractionDigits = 1): string {
  if (!Number.isFinite(p)) return '—'
  return `${(p * 100).toFixed(fractionDigits)}%`
}

export function formatUSD(n: number): string {
  if (!Number.isFinite(n)) return '—'
  // Sub-cent consumption is the normal scale for a single agent turn, so an
  // amount below a dollar keeps four decimals instead of rounding to $0.00.
  const digits = Math.abs(n) >= 1 ? 2 : 4
  return `$${n.toFixed(digits)}`
}
