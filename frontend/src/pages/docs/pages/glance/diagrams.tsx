import { useId, type ReactNode } from 'react'

// Diagrams for "AInsel at a Glance". Every colour comes from a `gl-*` class
// in AtAGlance.css (design tokens), so light and dark themes both work and
// no fill/stroke colour is ever an SVG attribute. Each SVG scales with its
// container via viewBox.

type BoxProps = {
  x: number
  y: number
  w: number
  h: number
  lines: string[]
  sub?: string
  accent?: boolean
}

function Box({ x, y, w, h, lines, sub, accent }: BoxProps) {
  const lineHeight = 18
  const textHeight = lines.length * lineHeight + (sub ? 16 : 0)
  const top = y + (h - textHeight) / 2 + 13
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={h}
        rx={6}
        className={accent ? 'gl-box gl-box-accent' : 'gl-box'}
      />
      <text className="gl-text" textAnchor="middle">
        {lines.map((line, i) => (
          <tspan key={line} x={x + w / 2} y={top + i * lineHeight}>
            {line}
          </tspan>
        ))}
      </text>
      {sub && (
        <text
          className="gl-sub"
          textAnchor="middle"
          x={x + w / 2}
          y={top + lines.length * lineHeight + 2}
        >
          {sub}
        </text>
      )}
    </g>
  )
}

// One arrowhead marker per SVG; ids come from useId so several diagrams on
// the page never share a marker id.
function Svg({
  viewBox,
  title,
  children,
}: {
  viewBox: string
  title: string
  children: (arrow: string) => ReactNode
}) {
  const id = useId()
  const titleId = `${id}-title`
  const arrowId = `${id}-arrow`
  return (
    <svg className="gl-svg" viewBox={viewBox} role="img" aria-labelledby={titleId}>
      <title id={titleId}>{title}</title>
      <defs>
        <marker
          id={arrowId}
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          orient="auto-start-reverse"
        >
          <path d="M0,0 L10,5 L0,10 z" className="gl-arrowhead" />
        </marker>
      </defs>
      {children(`url(#${arrowId})`)}
    </svg>
  )
}

function Arrow({ d, marker, quiet }: { d: string; marker: string; quiet?: boolean }) {
  return <path d={d} className={quiet ? 'gl-line' : 'gl-arrow'} markerEnd={marker} />
}

export function ProblemDiagram() {
  const teams = [
    { w: 92, rx: 2 },
    { w: 136, rx: 18 },
    { w: 70, rx: 6 },
    { w: 118, rx: 0 },
    { w: 104, rx: 12 },
  ]
  return (
    <Svg
      viewBox="0 0 600 260"
      title="Five differently shaped teams squeezed into one built-in workflow; every mismatch becomes a code change."
    >
      {(arrow) => (
        <>
          {teams.map((t, i) => {
            const y = 14 + i * 48
            return (
              <g key={i}>
                <rect x={20} y={y} width={t.w} height={34} rx={t.rx} className="gl-box" />
                <text className="gl-text" x={20 + t.w / 2} y={y + 22} textAnchor="middle">
                  Team {String.fromCharCode(65 + i)}
                </text>
                <Arrow
                  d={`M${28 + t.w},${y + 17} C260,${y + 17} 260,130 336,130`}
                  marker={arrow}
                  quiet
                />
              </g>
            )
          })}
          <Box
            x={340}
            y={80}
            w={240}
            h={100}
            lines={['One built-in', 'workflow']}
            sub="same for every team"
          />
          <text className="gl-warn" x={460} y={212} textAnchor="middle">
            Every mismatch is a
          </text>
          <text className="gl-warn" x={460} y={232} textAnchor="middle">
            code change in the tool
          </text>
        </>
      )}
    </Svg>
  )
}

export function LoopDiagram() {
  return (
    <Svg
      viewBox="0 0 600 310"
      title="The AInsel loop: something happens, a trigger decides which agent cares, the agent works on it, and the result shows up where people already work."
    >
      {(arrow) => (
        <>
          <Box x={20} y={20} w={230} h={96} lines={['Something', 'happens']} sub="event" />
          <Box
            x={350}
            y={20}
            w={230}
            h={96}
            lines={['A rule decides', 'which agent cares']}
            sub="trigger"
            accent
          />
          <Box x={350} y={194} w={230} h={96} lines={['The agent', 'works on it']} sub="agent" />
          <Box
            x={20}
            y={194}
            w={230}
            h={96}
            lines={['The result shows up', 'where people work']}
            sub="acts back"
          />
          <Arrow d="M256,68 L344,68" marker={arrow} />
          <Arrow d="M465,122 L465,188" marker={arrow} />
          <Arrow d="M344,242 L256,242" marker={arrow} />
          <Arrow d="M135,188 L135,122" marker={arrow} quiet />
          <text className="gl-sub" x={145} y={160}>
            next event
          </text>
        </>
      )}
    </Svg>
  )
}

const STAGES = ['event', 'trigger', 'agent', 'acts back']

const JOBS: { title: string; steps: string[][] }[] = [
  {
    title: 'Software development',
    steps: [
      ['Issue labelled', '“implement”'],
      ['Label matches'],
      ['Agent writes', 'the code'],
      ['Pull request', 'opened'],
    ],
  },
  {
    title: 'Security audit',
    steps: [
      ['Nightly', 'schedule'],
      ['Cron fires'],
      ['Agent audits', 'the repos'],
      ['Findings filed', 'as issues'],
    ],
  },
  {
    title: 'Home automation',
    steps: [
      ['Motion sensor', 'webhook'],
      ['Only after', 'sunset'],
      ['Agent decides', 'what to do'],
      ['Hallway', 'lights on'],
    ],
  },
]

export function ThreeJobsDiagram() {
  return (
    <div className="gl-lanes">
      {JOBS.map((job) => (
        <Svg
          key={job.title}
          viewBox="0 0 220 372"
          title={`${job.title}: ${job.steps.map((s) => s.join(' ')).join(', then ')}.`}
        >
          {(arrow) => (
            <>
              <text className="gl-lane-title" x={110} y={20} textAnchor="middle">
                {job.title}
              </text>
              {job.steps.map((lines, i) => {
                const y = 36 + i * 86
                return (
                  <g key={STAGES[i]}>
                    <Box
                      x={10}
                      y={y}
                      w={200}
                      h={66}
                      lines={lines}
                      sub={STAGES[i]}
                      accent={i === 1}
                    />
                    {i < 3 && <Arrow d={`M110,${y + 68} L110,${y + 84}`} marker={arrow} />}
                  </g>
                )
              })}
            </>
          )}
        </Svg>
      ))}
    </div>
  )
}

const BLOCKS = [
  'LLM',
  'Agent image',
  'Persona',
  'Skills',
  'Tools',
  'MCP servers',
  'Connectors',
  'Plugins',
]

export function ChooseDiagram() {
  return (
    <Svg viewBox="0 0 600 176" title={`Building blocks each team chooses: ${BLOCKS.join(', ')}.`}>
      {() => (
        <>
          <text className="gl-sub" x={300} y={16} textAnchor="middle">
            chosen by each team
          </text>
          {BLOCKS.map((label, i) => (
            <Box
              key={label}
              x={12 + (i % 4) * 146}
              y={30 + Math.floor(i / 4) * 74}
              w={134}
              h={60}
              lines={[label]}
            />
          ))}
        </>
      )}
    </Svg>
  )
}

const PLATFORM = [
  'Queue & retries',
  'Scale to zero',
  'Access control',
  'Network isolation',
  'Cost per run',
  'Your own models',
  'One Helm chart',
]

export function SolvedDiagram() {
  const teams = ['Team A agent', 'Team B agent', 'Team C agent', '…']
  return (
    <Svg
      viewBox="0 0 600 282"
      title={`Team agents on top of the AInsel platform, which provides: ${PLATFORM.join(', ')}.`}
    >
      {() => (
        <>
          <text className="gl-sub" x={300} y={16} textAnchor="middle">
            owned by each team
          </text>
          {teams.map((label, i) => (
            <Box key={label} x={12 + i * 146} y={28} w={134} h={60} lines={[label]} />
          ))}
          <rect x={12} y={108} width={576} height={162} rx={8} className="gl-box gl-box-accent" />
          <text className="gl-lane-title" x={300} y={136} textAnchor="middle">
            AInsel platform — already solved
          </text>
          {PLATFORM.map((label, i) => {
            const row = i < 4 ? 0 : 1
            const col = row === 0 ? i : i - 4
            const perRow = row === 0 ? 4 : 3
            const w = 128
            const gap = 12
            const start = 300 - (perRow * w + (perRow - 1) * gap) / 2
            return (
              <Box
                key={label}
                x={start + col * (w + gap)}
                y={152 + row * 56}
                w={w}
                h={44}
                lines={[label]}
              />
            )
          })}
        </>
      )}
    </Svg>
  )
}

export function GrowDiagram() {
  const steps = [
    { lines: ['Try it'], sub: 'configure' },
    { lines: ['Find the', 'best way'], sub: 'iterate' },
    { lines: ['Write a', 'plugin'], sub: 'package' },
    { lines: ['Roll out to', '100 teams'], sub: 'install' },
  ]
  return (
    <Svg
      viewBox="0 0 600 120"
      title="Grow it: try it, find the best way, write a plugin, roll it out to 100 teams."
    >
      {(arrow) => (
        <>
          {steps.map((s, i) => (
            <g key={s.sub}>
              <Box
                x={8 + i * 150}
                y={14}
                w={124}
                h={92}
                lines={s.lines}
                sub={s.sub}
                accent={i === 3}
              />
              {i < 3 && <Arrow d={`M${136 + i * 150},60 L${154 + i * 150},60`} marker={arrow} />}
            </g>
          ))}
        </>
      )}
    </Svg>
  )
}
