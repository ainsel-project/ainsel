export const PLUGIN_API_VERSION = '1.1'

// Manifest types
export type { PluginManifest, PluginPage } from './manifest'

// Slots
export type { SlotId, SlotProps } from './slots'
export { SLOT_IDS } from './slots'

// Context and hooks
export type { PluginUser, HttpClient, PluginContextValue } from './context'
export { PluginContext, usePluginContext } from './context'

// Host registration
export type { HostPrimitives } from './host'
export { registerHost } from './host'

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
} from './primitives'
