import type { TocEntry } from '../componentPages'

// Headings and search text for AtAGlance, kept out of the .tsx so the
// component file exports only components (react-refresh).

export const atAGlanceHeadings: TocEntry[] = [
  { level: 2, text: 'The problem', id: 'the-problem' },
  { level: 2, text: 'One loop', id: 'one-loop' },
  { level: 2, text: 'Three jobs, same loop', id: 'three-jobs-same-loop' },
  { level: 2, text: 'What each team chooses', id: 'what-each-team-chooses' },
  { level: 2, text: "What's already solved", id: 'whats-already-solved' },
  { level: 2, text: 'Run it from your terminal', id: 'run-it-from-your-terminal' },
  { level: 2, text: 'Grow it', id: 'grow-it' },
  { level: 2, text: 'Next', id: 'next' },
]

export const atAGlanceSearchText = [
  'AInsel at a Glance',
  ...atAGlanceHeadings.map((h) => h.text),
  'Good agents for hundreds of teams: each team picks its own LLM, agent image, persona, skills, tools, MCP servers, connectors and plugins.',
  'A trigger decides which agent wakes up for an event.',
  'Software development, security audit, home automation on the same loop.',
  'A requirement is a configuration, skill or tool change, not a code change.',
  'Queued tasks with retries, scale to zero, access control, network isolation, cost per run, self-hosted, Helm chart.',
  'Manage AInsel from your terminal through its MCP server: set up agents, check logs and costs, fix skills, without opening the console.',
  'Find the best way, write a plugin, roll it out to 100 teams.',
].join('\n')
