import { Component, ErrorInfo, ReactNode } from 'react'
import { Notice } from '../primitives/Notice'
import type { ActivePlugin } from './registry'

interface Props {
  plugin: ActivePlugin
  children: ReactNode
}

interface State {
  error: Error | null
}

/**
 * Error boundary for plugin components.
 * Catches errors during render and displays a failure notice.
 */
export class PluginBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { error: null }
  }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, _errorInfo: ErrorInfo) {
    // Error is already captured in state; log if needed
    console.error(`Plugin ${this.props.plugin.id} failed:`, error)
  }

  render() {
    if (this.state.error) {
      return (
        <Notice variant="err">
          Plugin {this.props.plugin.displayName} failed to load: {this.state.error.message}
        </Notice>
      )
    }

    return this.props.children
  }
}
