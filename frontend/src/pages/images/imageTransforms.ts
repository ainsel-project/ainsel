import type { AgentImageRequest, EnvVar, Tool, ToolKind } from '../../api/agentImages'
import type { EnvVarFormValue, ImageFormValues, ToolFormValue } from './ImageDetail.types'

export function toFormTool(t: Tool): ToolFormValue {
  let mcpSource = t.mcpSource
  if (!mcpSource && t.kind === 'mcp') {
    const parts = t.name.split('__')
    if (parts.length >= 3 && parts[0] === 'mcp') mcpSource = parts[1]
  }
  return {
    name: t.name,
    kind: t.kind,
    isNew: t.isNew ?? false,
    description: t.description ?? '',
    mcpSource,
    examples: (t.examples ?? []).map((e) => ({ title: e.title, snippet: e.snippet })),
  }
}

function toApiTool(t: ToolFormValue): Tool {
  return {
    name: t.name,
    kind: t.kind as ToolKind,
    isNew: t.isNew,
    description: t.description || undefined,
    mcpSource: t.mcpSource || undefined,
    examples:
      t.examples.length > 0
        ? t.examples.map((e) => ({ title: e.title, snippet: e.snippet }))
        : undefined,
  }
}

export function buildRequestBody(values: ImageFormValues): AgentImageRequest {
  return {
    displayName: values.displayName || undefined,
    description: values.description || undefined,
    imageURL: values.imageURL,
    tools: values.tools.map(toApiTool),
    env: values.env.map((e): EnvVar => ({ name: e.name, value: e.value, secret: e.secret })),
    mcpServers: values.mcpServers,
  }
}

export const DEFAULT_SHELL_TOOLS: ToolFormValue[] = [
  {
    name: 'bash',
    kind: 'shell',
    description: 'Execute bash shell commands',
    isNew: false,
    examples: [],
  },
  {
    name: 'ls',
    kind: 'shell',
    description: 'List directory contents',
    isNew: false,
    examples: [],
  },
  {
    name: 'cat',
    kind: 'shell',
    description: 'Display file contents',
    isNew: false,
    examples: [],
  },
  {
    name: 'pwd',
    kind: 'shell',
    description: 'Print current working directory',
    isNew: false,
    examples: [],
  },
  {
    name: 'grep',
    kind: 'shell',
    description: 'Search for patterns in files',
    isNew: false,
    examples: [],
  },
  {
    name: 'find',
    kind: 'shell',
    description: 'Find files and directories',
    isNew: false,
    examples: [],
  },
  {
    name: 'mkdir',
    kind: 'shell',
    description: 'Create directories',
    isNew: false,
    examples: [],
  },
  {
    name: 'mv',
    kind: 'shell',
    description: 'Move or rename files',
    isNew: false,
    examples: [],
  },
  {
    name: 'cp',
    kind: 'shell',
    description: 'Copy files and directories',
    isNew: false,
    examples: [],
  },
  {
    name: 'rm',
    kind: 'shell',
    description: 'Remove files and directories',
    isNew: false,
    examples: [],
  },
  {
    name: 'echo',
    kind: 'shell',
    description: 'Print text to stdout',
    isNew: false,
    examples: [],
  },
  {
    name: 'curl',
    kind: 'shell',
    description: 'Transfer data with HTTP/HTTPS',
    isNew: false,
    examples: [],
  },
]

export function envVarToFormValue(e: {
  name: string
  value: string
  secret?: boolean
}): EnvVarFormValue {
  return { name: e.name, value: e.secret ? '' : e.value, secret: e.secret ?? false }
}
