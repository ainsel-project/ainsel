# @ainsel/plugin-sdk

The AInsel plugin SDK for building console extensions.

## Overview

This package provides the types and utilities for developing plugins that extend the AInsel console. Plugins can add new pages, fill extension slots, and access the console's HTTP clients and user context.

## Version

The plugin API version supported by this SDK is `1.1`. Consult `PLUGIN_API_VERSION` to check it at runtime.

## Architecture

### React and the SDK

The `react` package is provided by the console at runtime and is not bundled. Configure your build tool to exclude `react`, `react-dom`, and `@ainsel/plugin-sdk` from your bundle and import them from the shared module registry at runtime. The console exposes them via an import map under the bare specifiers `react`, `react-dom`, and `@ainsel/plugin-sdk`.

### Exports

The SDK exports:
- **Constants**: `PLUGIN_API_VERSION`
- **Types**: `PluginManifest`, `PluginPage`, `SlotId`, `SlotProps`, `PluginUser`, `HttpClient`, `PluginContextValue`, `HostPrimitives`
- **Hooks**: `usePluginContext()`
- **Context**: `PluginContext` (for advanced use)
- **Registration**: `registerHost()`
- **Primitives**: `Button`, `Panel`, `Tabs`, `Tag`, `Notice`, `Field`, `Input`, `Select`, `Textarea`, `Dot`, `RegisterTable`, `Markdown`, `TimeSeriesChart`, `Sparkbar`

### Slots

Plugins can contribute to the following extension points (slots):

| Slot | Props |
|---|---|
| `observability.section` | `{ range: string }` |
| `invocation.panel` | `{ invocationId: string; agentName: string }` |
| `agent.panel` | `{ agentName: string }` |
| `persona.panel` | `{ personaId: string }` |

## Usage

### Accessing Context

Use the `usePluginContext()` hook to access the plugin's context, including the user, HTTP clients, and navigation:

```tsx
import { usePluginContext } from '@ainsel/plugin-sdk'

export function MyPage() {
  const { user, hub, api, navigate, theme } = usePluginContext()
  return <div>Hello, {user.username}</div>
}
```

### Using Primitives

The SDK provides delegating wrappers for the console's primitive components:

```tsx
import { Button, Panel, Notice } from '@ainsel/plugin-sdk'

export function MyComponent() {
  return (
    <Panel>
      <Notice variant="info">This is a notice</Notice>
      <Button onClick={() => alert('clicked')}>Click me</Button>
    </Panel>
  )
}
```

## Building and Publishing

This package is not yet published to npm. It will be built and published as part of the plugin system distribution.
