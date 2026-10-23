import React from 'react'
import { getHostPrimitives } from './host.js'

// Delegating wrappers for host primitives. Each wrapper forwards all props
// to the registered host component.

export function Button(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Button {...props} />
}

export function Panel(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Panel {...props} />
}

export function Tabs(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Tabs {...props} />
}

export function Tag(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Tag {...props} />
}

export function Notice(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Notice {...props} />
}

export function Field(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Field {...props} />
}

export function Input(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Input {...props} />
}

export function Select(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Select {...props} />
}

export function Textarea(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Textarea {...props} />
}

export function Dot(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Dot {...props} />
}

export function RegisterTable(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.RegisterTable {...props} />
}

export function Markdown(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Markdown {...props} />
}

export function TimeSeriesChart(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.TimeSeriesChart {...props} />
}

export function Sparkbar(props: React.ComponentProps<any>) {
  const primitives = getHostPrimitives()
  return <primitives.Sparkbar {...props} />
}
