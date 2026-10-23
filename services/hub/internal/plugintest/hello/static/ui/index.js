// The fixture's console module: plain ES modules, and only the two externals the
// console's import map resolves — react/jsx-runtime and @ainsel/plugin-sdk. No
// bundler features, no JSX syntax: this file is what a plugin's build output has
// to look like, so it is written by hand.
//
// Each component renders one element with one text node. Splitting a sentence
// across elements makes it unreadable to a screen reader and untranslatable.

import { jsx as _jsx } from 'react/jsx-runtime'
import { usePluginContext } from '@ainsel/plugin-sdk'

// Default export: the page the console renders at /p/hello.
export default function HelloPage() {
  const { pluginId } = usePluginContext()
  return _jsx('p', { children: 'Hello from ' + pluginId })
}

// AdminPage: named in the manifest with visibility "admin", so the console
// renders it at /p/hello/admin only for administrators.
export function AdminPage() {
  return _jsx('p', { children: 'Hello admin' })
}

// AgentPanel fills the agent.panel slot; the host passes the agent it renders
// the panel under.
export function AgentPanel({ agentName }) {
  return _jsx('p', { children: 'Hello agent ' + agentName })
}
