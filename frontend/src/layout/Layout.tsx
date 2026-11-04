import { useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Spine } from './Spine'
import { useAuth } from '../auth/AuthProvider'
import './Layout.css'

export function Layout() {
  const { user, mode } = useAuth()
  const location = useLocation()
  const [menuOpen, setMenuOpen] = useState(false)
  // In the agent and plugins workspaces the spine collapses to an icon rail;
  // the shell carries the same class so its grid track narrows to match (see
  // .shell.rail in Layout.css) instead of leaving a blank 236px column.
  const rail =
    location.pathname === '/agents' ||
    location.pathname.startsWith('/agents/') ||
    location.pathname === '/plugins' ||
    location.pathname.startsWith('/plugins/') ||
    location.pathname.startsWith('/p/')

  // Close the drawer whenever the route changes (mobile nav selection).
  useEffect(() => {
    setMenuOpen(false)
  }, [location.pathname])

  // Close the drawer on Escape and lock body scroll while it is open.
  useEffect(() => {
    if (!menuOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenuOpen(false)
    }
    window.addEventListener('keydown', onKey)
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      window.removeEventListener('keydown', onKey)
      document.body.style.overflow = prevOverflow
    }
  }, [menuOpen])

  const shellClasses = ['shell', menuOpen ? 'menu-open' : null, rail ? 'rail' : null]
    .filter(Boolean)
    .join(' ')

  return (
    <div className={shellClasses}>
      <a className="skip-link" href="#content">
        Skip to content
      </a>

      <header className="mobilebar">
        <button
          className="menu-btn"
          aria-label="Menu"
          aria-expanded={menuOpen}
          aria-controls="spine"
          onClick={() => setMenuOpen((v) => !v)}
        >
          <span className="menu-icon" aria-hidden="true" />
        </button>
        <span className="mobilebar-title">
          AInsel <em>Hub</em>
        </span>
      </header>

      <Spine
        operator={mode === 'none' ? 'no auth' : (user?.username ?? 'anon')}
        open={menuOpen}
        onClose={() => setMenuOpen(false)}
        rail={rail}
      />
      <main id="content" className="canvas">
        <Outlet />
      </main>
    </div>
  )
}
