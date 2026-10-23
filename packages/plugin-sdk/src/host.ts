import React from 'react'

export interface HostPrimitives {
  Button: React.ComponentType<any>
  Panel: React.ComponentType<any>
  Tabs: React.ComponentType<any>
  Tag: React.ComponentType<any>
  Notice: React.ComponentType<any>
  Field: React.ComponentType<any>
  Input: React.ComponentType<any>
  Select: React.ComponentType<any>
  Textarea: React.ComponentType<any>
  Dot: React.ComponentType<any>
  RegisterTable: React.ComponentType<any>
  Markdown: React.ComponentType<any>
  TimeSeriesChart: React.ComponentType<any>
  Sparkbar: React.ComponentType<any>
}

let registry: { primitives: HostPrimitives } | null = null

export function registerHost(h: { primitives: HostPrimitives }): void {
  registry = h
}

export function getHostPrimitives(): HostPrimitives {
  if (!registry) {
    throw new Error(
      '@ainsel/plugin-sdk: host primitives not registered',
    )
  }
  return registry.primitives
}
