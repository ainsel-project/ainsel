// Manifest types mirror the Go manifest with camelCase JSON names.

export interface PluginManifest {
  id: string
  displayName: string
  version: string
  description?: string
  pluginApi: string
  ui?: {
    entry: string
    pages?: PluginPage[]
    slots?: Partial<Record<string, string>>
  }
  api?: {
    basePath: string
  }
  mcp?: {
    path: string
    /**
     * Per-tool audience. Tools not listed here are available to both
     * audiences. `agent` tools are only reachable through the agent runtime,
     * `operator` tools only through AInsel's own MCP server.
     */
    audience?: Record<string, 'agent' | 'operator' | 'both'>
  }
  /** Skills this plugin contributes to the platform skill registry. */
  skills?: {
    name: string
    description: string
    path: string
  }[]
  permissions?: string[]
}

export interface PluginPage {
  path: string
  label: string
  section?: string
  icon?: string
  component?: string
  visibility?: '' | 'admin'
}
