import React from 'react'
import ReactDOM from 'react-dom'
import ReactDOMClient from 'react-dom/client'
import * as jsxRuntime from 'react/jsx-runtime'
import * as sdk from '@ainsel/plugin-sdk'
import { Button } from '../primitives/Button'
import { Panel } from '../primitives/Panel'
import { Tabs } from '../primitives/Tabs'
import { Tag } from '../primitives/Tag'
import { Notice } from '../primitives/Notice'
import { Field } from '../primitives/Field'
import { Input } from '../primitives/Input'
import { Select } from '../primitives/Select'
import { Textarea } from '../primitives/Textarea'
import { Dot } from '../primitives/Dot'
import { RegisterTable } from '../primitives/RegisterTable'
import { Markdown } from '../primitives/Markdown'
import { TimeSeriesChart } from '../primitives/TimeSeriesChart'
import { Sparkbar } from '../primitives/Sparkbar'

declare global {
  interface Window {
    __AINSEL_SHARED__?: Record<string, unknown>
  }
}

export function installPluginHost(): void {
  if (window.__AINSEL_SHARED__) {
    return
  }

  window.__AINSEL_SHARED__ = {
    react: React,
    'react-dom': ReactDOM,
    'react-dom/client': ReactDOMClient,
    'react/jsx-runtime': jsxRuntime,
    '@ainsel/plugin-sdk': sdk,
  }

  const primitives = {
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
  }

  sdk.registerHost({ primitives })
}
