const inboxChannel = {
  id: 'ch-inbox-a1',
  kind: 'agent',
  name: 'doc-writer',
  description: 'The inbox agent doc-writer drains',
  entityRef: 'a1',
  counts: { events: 7, unmatched: 0, failed: 1 },
  bridges: 0,
  subscriptions: 1,
}

/**
 * Shared fetch stub for the agent workspace tests: answers the endpoints the
 * overview and section views read, with one doc-writer agent (a1). Tests wrap
 * it when they need a different agent payload.
 */
export function agentTestFetch(url: string, init?: RequestInit): Response {
  if (init?.method === 'DELETE') {
    return new Response(null, { status: 204 })
  }
  if (url.includes('/invocations')) {
    return new Response(
      JSON.stringify({
        invocations: [],
        total: 0,
        capacity: 0,
        page: 1,
        pageSize: 200,
        totalPages: 0,
      }),
      { status: 200 },
    )
  }
  if (url.includes('/chat/sessions')) {
    return new Response(
      JSON.stringify({ items: [], total: 0, page: 1, pageSize: 1, totalPages: 0 }),
      { status: 200 },
    )
  }
  if (url.includes('/cron-triggers')) {
    return new Response(
      JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
      { status: 200 },
    )
  }
  if (/\/api\/v1\/channels\/ch-inbox-a1\/events/.test(url)) {
    return new Response(JSON.stringify({ events: [], total: 0 }), { status: 200 })
  }
  if (/\/api\/v1\/channels\/ch-inbox-a1$/.test(url)) {
    return new Response(JSON.stringify({ ...inboxChannel, incoming: [], outgoing: [] }), {
      status: 200,
    })
  }
  if (/\/api\/v1\/channels(\?.*)?$/.test(url)) {
    return new Response(
      JSON.stringify({
        items: [inboxChannel],
        total: 1,
        page: 1,
        pageSize: 500,
        totalPages: 1,
        window: '24h0m0s',
      }),
      { status: 200 },
    )
  }
  if (url.includes('/channel-subscriptions')) {
    return new Response(
      JSON.stringify({
        items: [
          {
            source: 'trigger',
            refId: 't1',
            name: 'on-doc-issue',
            fromChannel: 'ch-conn-c',
            toChannel: 'ch-inbox-a1',
          },
        ],
        total: 1,
      }),
      { status: 200 },
    )
  }
  if (url.includes('/triggers')) {
    return new Response(
      JSON.stringify({
        items: [
          {
            id: 't1',
            name: 'on-doc-issue',
            agentRef: 'a1',
            connectorRef: 'c-a8bf5238',
            filters: [],
          },
        ],
        total: 1,
        page: 1,
        pageSize: 200,
        totalPages: 1,
      }),
      { status: 200 },
    )
  }
  if (url.includes('/connectors')) {
    return new Response(
      JSON.stringify({
        items: [],
        total: 0,
        page: 1,
        pageSize: 200,
        totalPages: 0,
      }),
      { status: 200 },
    )
  }
  const personaMatch = url.match(/\/api\/v1\/personas\/([^/?]+)$/)
  if (personaMatch) {
    const id = personaMatch[1]
    const alt = id.endsWith('1')
    return new Response(
      JSON.stringify({
        id,
        name: alt ? 'ops-triager' : 'docs-writer',
        description: 'docs persona',
        currentVersion: 1,
        text: alt ? '# Persona\n\nYou triage issues.' : '# Persona\n\nYou are a docs writer.',
        createdAt: '2026-05-01T00:00:00Z',
        updatedAt: '2026-05-01T00:00:00Z',
      }),
      { status: 200 },
    )
  }
  if (url.includes('/personas')) {
    return new Response(
      JSON.stringify({
        items: [
          {
            id: '01HXTEST00000000000000000',
            name: 'docs-writer',
            description: 'docs persona',
            currentVersion: 1,
            createdAt: '2026-05-01T00:00:00Z',
            updatedAt: '2026-05-01T00:00:00Z',
          },
          {
            id: '01HXTEST00000000000000001',
            name: 'ops-triager',
            description: 'triage persona',
            currentVersion: 1,
            createdAt: '2026-05-01T00:00:00Z',
            updatedAt: '2026-05-01T00:00:00Z',
          },
        ],
        total: 2,
        page: 1,
        pageSize: 200,
        totalPages: 1,
      }),
      { status: 200 },
    )
  }
  if (url.includes('/agent-images/claude-tooling-base')) {
    return new Response(
      JSON.stringify({
        id: 'claude-tooling-base:1.4',
        displayName: 'Claude Tooling Base',
        description: 'Base tooling image',
        imageURL: 'ghcr.io/ainsel/claude-tooling:1.4',
        tools: [
          { name: 'read_file', kind: 'shell' },
          { name: 'run_shell', kind: 'shell' },
        ],
        env: [],
        mcpServers: [],
        enabledSkills: [],
      }),
      { status: 200 },
    )
  }
  if (url.includes('/skills')) {
    return new Response(
      JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
      { status: 200 },
    )
  }
  if (url.includes('/mcp-servers')) {
    return new Response(JSON.stringify([]), { status: 200 })
  }
  if (url.includes('/agent-images')) {
    return new Response(
      JSON.stringify({
        items: [
          {
            id: 'claude-tooling-base:1.4',
            displayName: 'Claude Tooling Base',
            imageURL: 'ghcr.io/ainsel/claude-tooling:1.4',
            toolCount: 2,
            enabledSkills: [],
          },
          {
            id: 'minimal:1.0',
            displayName: 'Minimal',
            imageURL: 'ghcr.io/ainsel/minimal:1.0',
            toolCount: 0,
            enabledSkills: [],
          },
        ],
        total: 2,
        page: 1,
        pageSize: 200,
        totalPages: 1,
      }),
      { status: 200 },
    )
  }
  // Agent-scoped persona (M3): the agent detail Persona section reads and saves
  // through here, not through /personas/{id}.
  const agentPersonaMatch = url.match(/\/api\/v1\/agents\/([^/?]+)\/persona$/)
  if (agentPersonaMatch) {
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body ?? '{}')) as {
        name?: string
        description?: string
        text?: string
      }
      return new Response(
        JSON.stringify({
          owned: true,
          ref: 'p-own',
          persona: {
            id: 'p-own',
            name: body.name || 'doc-writer (own)',
            description: body.description ?? '',
            currentVersion: 1,
            text: body.text ?? '',
            ownerAgent: agentPersonaMatch[1],
            createdAt: '2026-05-01T00:00:00Z',
            updatedAt: '2026-05-01T00:00:00Z',
          },
        }),
        { status: 200 },
      )
    }
    return new Response(
      JSON.stringify({
        owned: false,
        ref: '01HXTEST00000000000000000',
        persona: {
          id: '01HXTEST00000000000000000',
          name: 'docs-writer',
          description: 'docs persona',
          currentVersion: 1,
          text: '# Persona\n\nYou are a docs writer.',
          createdAt: '2026-05-01T00:00:00Z',
          updatedAt: '2026-05-01T00:00:00Z',
        },
      }),
      { status: 200 },
    )
  }
  if (url.includes('/agents/a1')) {
    return new Response(
      JSON.stringify({
        id: 'a1',
        name: 'doc-writer',
        description: 'Writes documentation.',
        llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
        imageRef: { id: 'claude-tooling-base:1.4', name: 'claude-tooling-base:1.4' },
        enabledTools: ['read_file', 'run_shell'],
        persona: { id: '01HXTEST00000000000000000' },
        replicas: 3,
        status: { ready: true, replicas: 3 },
      }),
      { status: 200 },
    )
  }
  return new Response('{}', { status: 200 })
}
