export const PLUGIN_API_VERSION = '1.1'

// Manifest types
export type { PluginManifest, PluginPage } from './manifest.js'

// Slots
export type { SlotId, SlotProps } from './slots.js'
export { SLOT_IDS } from './slots.js'

// Context and hooks
export type { PluginUser, HttpClient, PluginContextValue } from './context.js'
export { PluginContext, usePluginContext } from './context.js'

// Host registration
export type { HostPrimitives } from './host.js'
export { registerHost } from './host.js'

// Primitives
export {
  Button,
  Panel,
  Tabs,
  Tag,
  Notice,
  Field,
  Input,
  Select,
  Textarea,
  Dot,
  RegisterTable,
  Markdown,
  TimeSeriesChart,
  Sparkbar,
} from './primitives.js'
