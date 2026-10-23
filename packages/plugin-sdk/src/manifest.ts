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
  }
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
