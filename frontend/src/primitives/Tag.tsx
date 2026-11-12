import type { ReactNode } from 'react'

type Variant = 'default' | 'ok' | 'warn' | 'err' | 'stale'

type TagProps = {
  children: ReactNode
  variant?: Variant
  solid?: boolean
  className?: string
  /** Optional tooltip, e.g. to explain what the tag means. */
  title?: string
}

export function Tag({ children, variant = 'default', solid, className, title }: TagProps) {
  const cls = ['tag', variant !== 'default' && variant, solid && 'solid', className]
    .filter(Boolean)
    .join(' ')
  return (
    <span className={cls} title={title}>
      {children}
    </span>
  )
}
