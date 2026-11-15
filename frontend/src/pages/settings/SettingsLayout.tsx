import { Outlet } from 'react-router-dom'

/**
 * Route group for /settings. It used to render a "Settings Console" title
 * block on top of each page's own; the pages are the title block now — one
 * title block per page, named like the Spine entry (see "Console UI
 * consistency" in docs/conventions.md).
 */
export function SettingsLayout() {
  return <Outlet />
}
