import type { ReactNode } from 'react'

/**
 * The spine's nav glyphs (issue #279).
 *
 * In rail mode the spine is 48px wide and the entry name is not rendered, so
 * the icon is what makes the navigation readable. Every nav entry therefore
 * carries one of these, and the rail shows it while the expanded spine keeps
 * the ledger number (see `.nav-icon` / `.spine.rail` in Spine.css).
 *
 * They are inline `currentColor` SVGs — the same approach as the edit glyph in
 * `pages/agents/workspace/OverviewSection.tsx` — rather than an icon package:
 * the frontend has no icon dependency, and fifteen 16px glyphs do not justify
 * adding one.
 *
 * Shared grid: 16x16, stroke-only, ~1.5px inset from the edge, no fill. Colour
 * comes from `currentColor`, so the active state (white on --signal) is free.
 */
export type NavIconName =
  | 'dashboard'
  | 'channels'
  | 'activity'
  | 'observability'
  | 'agents'
  | 'images'
  | 'personas'
  | 'skills'
  | 'mcps'
  | 'docs'
  | 'users'
  | 'groups'
  | 'connectors'
  | 'audit'
  | 'plugins'

const GLYPHS: Record<NavIconName, ReactNode> = {
  dashboard: (
    <>
      <path d="M2.5 12.5a5.5 5.5 0 0 1 11 0" />
      <path d="M8 12.5 10.5 8.6" />
      <circle cx="8" cy="12.5" r="1" />
    </>
  ),
  channels: <path d="M2 3h12v7H7l-3 2.5V10H2z" />,
  activity: <path d="M1.5 8h3l2-4.5 3 9 2-4.5h3" />,
  observability: (
    <>
      <path d="M1.8 8S4.2 4.2 8 4.2 14.2 8 14.2 8 11.8 11.8 8 11.8 1.8 8 1.8 8z" />
      <circle cx="8" cy="8" r="1.9" />
    </>
  ),
  agents: (
    <>
      <rect x="3" y="5.5" width="10" height="7.5" rx="2" />
      <path d="M8 3.2v2.3" />
      <circle cx="8" cy="2.3" r="0.9" />
      <path d="M6 9h.01" />
      <path d="M10 9h.01" />
      <path d="M6 11.2h4" />
    </>
  ),
  images: (
    <>
      <rect x="2" y="3" width="12" height="10" rx="1.5" />
      <path d="M2.5 11l3.5-3.5 3 3 2-2 2.5 2.5" />
      <circle cx="11" cy="6" r="1" />
    </>
  ),
  personas: (
    <>
      <circle cx="8" cy="5.5" r="2.6" />
      <path d="M3 13.5c0-2.9 2.2-4.6 5-4.6s5 1.7 5 4.6" />
    </>
  ),
  skills: <path d="M8 2l1.7 4.3L14 8l-4.3 1.7L8 14l-1.7-4.3L2 8l4.3-1.7z" />,
  mcps: (
    <>
      <path d="M6 2v3" />
      <path d="M10 2v3" />
      <path d="M4 5h8v2.8a4 4 0 0 1-8 0z" />
      <path d="M8 11.8v2.4" />
    </>
  ),
  docs: (
    <>
      <path d="M4 2h5l3.2 3.2V14H4z" />
      <path d="M9 2v3.2h3.2" />
      <path d="M6.2 8.5h4" />
      <path d="M6.2 11h4" />
    </>
  ),
  users: (
    <>
      <circle cx="6" cy="5.6" r="2.3" />
      <path d="M1.8 13.4c0-2.5 1.9-4 4.2-4s4.2 1.5 4.2 4" />
      <path d="M10.6 3.7a2.3 2.3 0 0 1 0 3.9" />
      <path d="M11.6 9.7c1.6.5 2.6 1.8 2.6 3.7" />
    </>
  ),
  groups: (
    <>
      <rect x="2.5" y="2.5" width="4.8" height="4.8" rx="1.2" />
      <rect x="8.7" y="2.5" width="4.8" height="4.8" rx="1.2" />
      <rect x="2.5" y="8.7" width="4.8" height="4.8" rx="1.2" />
      <rect x="8.7" y="8.7" width="4.8" height="4.8" rx="1.2" />
    </>
  ),
  connectors: (
    <>
      <path d="M6.6 9.4 9.4 6.6" />
      <path d="M7.6 5.2 8.9 3.9a2.6 2.6 0 0 1 3.7 3.7l-1.3 1.3" />
      <path d="M8.4 10.8 7.1 12.1a2.6 2.6 0 0 1-3.7-3.7l1.3-1.3" />
    </>
  ),
  audit: (
    <>
      <path d="M5 3H4v11h8V3h-1" />
      <path d="M6 2h4v2H6z" />
      <path d="M6 9.2 7.6 10.8 10.4 8" />
    </>
  ),
  plugins: (
    <>
      <path d="M8 2 14 5 8 8 2 5z" />
      <path d="M2 8.2 8 11.2 14 8.2" />
      <path d="M2 11.2 8 14.2 14 11.2" />
    </>
  ),
}

export function NavIcon({ name }: { name: NavIconName }) {
  return (
    <svg
      className="nav-icon"
      width={16}
      height={16}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.4}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {GLYPHS[name]}
    </svg>
  )
}
