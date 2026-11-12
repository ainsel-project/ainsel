import { Link } from 'react-router-dom'

/**
 * A tab. `value` identifies it and is compared against the tabs' current
 * `value`. When `to` is set the tab is a route: it renders as a link-backed
 * `role="tab"` (deep-linkable, openable in a new tab) and navigation *is*
 * the selection — `onChange` is not used for such tabs.
 */
export type Tab = { value: string; label: string; to?: string }

type TabsProps = {
  value: string
  onChange?: (next: string) => void
  tabs: readonly Tab[]
  'aria-label'?: string
}

export function Tabs({ value, onChange, tabs, ...rest }: TabsProps) {
  return (
    <div className="tabs" role="tablist" aria-label={rest['aria-label']}>
      {tabs.map((t) => {
        const className = t.value === value ? 'tab active' : 'tab'
        if (t.to !== undefined) {
          return (
            <Link
              key={t.value}
              to={t.to}
              role="tab"
              aria-selected={t.value === value}
              className={className}
            >
              {t.label}
            </Link>
          )
        }
        return (
          <button
            key={t.value}
            type="button"
            role="tab"
            aria-selected={t.value === value}
            className={className}
            onClick={() => onChange?.(t.value)}
          >
            {t.label}
          </button>
        )
      })}
    </div>
  )
}
