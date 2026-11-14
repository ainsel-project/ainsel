import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { SectionStatus } from '../primitives/SectionStatus'

type BoundaryProps = {
  queryClient: QueryClient
  children: ReactNode
}

type BoundaryState = {
  error: Error | null
}

// The class holds the catch lifecycle; the function wrapper below supplies the
// query client it needs (hooks are unavailable in class components).
class Boundary extends Component<BoundaryProps, BoundaryState> {
  state: BoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): BoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, _info: ErrorInfo) {
    // Keep the console trail operators used to being the only trace of a
    // blank page; the visible trail is the SectionStatus rendered below.
    console.error('Route render failed:', error)
  }

  private retry = () => {
    // Clear the captured error so the subtree re-renders, and invalidate
    // react-query so a stale or oddly-shaped payload is refetched rather than
    // replayed. No navigation, no window.location.reload() — the shell and
    // the spine stay mounted throughout.
    this.setState({ error: null })
    void this.props.queryClient.invalidateQueries()
  }

  render() {
    if (this.state.error) {
      return (
        <SectionStatus
          state="error"
          title="This page failed to render"
          detail={this.state.error.message}
          onRetry={this.retry}
        />
      )
    }
    return this.props.children
  }
}

/**
 * Route-level error boundary (issue #283). A page that throws — typically a
 * list component handed a response shape it did not expect — must not blank
 * the whole console: only the routed content is replaced by the standard
 * error state, so the operator keeps the spine and can still reach the
 * error log.
 *
 * Callers key this on `location.pathname` so navigating to any other route
 * always resets it. Retry clears the captured error and invalidates
 * react-query; it never navigates and never reloads the window.
 */
export function RouteErrorBoundary({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  return <Boundary queryClient={queryClient}>{children}</Boundary>
}
