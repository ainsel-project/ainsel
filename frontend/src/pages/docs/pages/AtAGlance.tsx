import { Link } from 'react-router-dom'
import { atAGlanceHeadings } from './atAGlanceMeta'
import {
  ChooseDiagram,
  GrowDiagram,
  LoopDiagram,
  ManageDiagram,
  ProblemDiagram,
  SolvedDiagram,
  ThreeJobsDiagram,
} from './glance/diagrams'
import './AtAGlance.css'

// "AInsel at a Glance": the one-page pitch dpinsel walks a (technical) manager
// through. Registered in componentPages as `at-a-glance`; it renders inside
// the normal docs layout, so it keeps markdown typography (.md-body) and adds
// only diagrams and a couple of tables. Colours come from design tokens.

function H2({ index }: { index: number }) {
  const { id, text } = atAGlanceHeadings[index]
  return <h2 id={id}>{text}</h2>
}

const DIAGRAMS: Record<string, () => React.JSX.Element> = {
  problem: ProblemDiagram,
  loop: LoopDiagram,
  'three-jobs': ThreeJobsDiagram,
  choose: ChooseDiagram,
  solved: SolvedDiagram,
  manage: ManageDiagram,
  grow: GrowDiagram,
}

function Figure({ name }: { name: string }) {
  const Diagram = DIAGRAMS[name]
  return (
    <figure className="glance-figure" data-diagram={name}>
      <Diagram />
    </figure>
  )
}

const SOLVED: { feature: string; detail: string; to: string }[] = [
  {
    feature: 'Queued tasks with retries',
    detail: 'Every event is stored; a failed run is retried, not lost.',
    to: '/docs/architecture',
  },
  {
    feature: 'Scale to zero',
    detail: 'Agents scale with their queue, down to zero when idle.',
    to: '/docs/crd-reference',
  },
  {
    feature: 'Group-based access control',
    detail: 'Every resource belongs to a team’s group; roles decide who may change it.',
    to: '/docs/access-control',
  },
  {
    feature: 'Default-deny network isolation',
    detail: 'The platform’s namespace starts closed; only allowed traffic gets through.',
    to: '/docs/network-policies',
  },
  {
    feature: 'Cost per run',
    detail: 'Token usage is recorded for every run and priced once you set model costs.',
    to: '/docs/administrator-guide',
  },
  {
    feature: 'Self-hosted, your models',
    detail: 'Runs in your cluster, with the LLMs you choose.',
    to: '/docs/deployment',
  },
  {
    feature: 'One Helm chart',
    detail: 'The whole platform installs and upgrades as one release.',
    to: '/docs/deployment',
  },
]

export function AtAGlance() {
  return (
    <div className="glance">
      <h1>AInsel at a Glance</h1>
      <p className="glance-lede">
        AInsel is the platform your organisation runs AI agents on. It handles the hard, repetitive
        platform work once, and leaves every team free to build the agent that fits how they
        actually work.
      </p>

      <H2 index={0} />
      <p>
        Agents that work through the night, unattended, need a <em>harness</em> around them: what
        they may touch, which model and tools they get, who watches what they do, and what a run may
        cost. They have to be tightly scoped and closely monitored. Getting that harness right, and
        secure, is the hard part of running agents.
      </p>
      <p>
        And one harness has to fit every team, every size and every requirement. A harness built for
        a single use case doesn&apos;t: the next team needs a second harness, the one after that a
        third, each with its own scoping and monitoring to get right. Or the first harness grows a
        feature for every request until it is too complex to understand, let alone secure.
      </p>
      <p>
        AInsel is that harness, and it covers the loop and nothing more, by design. Scoping,
        isolation, monitoring and cost are solved once, and everything team-specific plugs in. You
        never need a second harness, or a complicated one.
      </p>
      <Figure name="problem" />

      <H2 index={1} />
      <p>
        Every agent in AInsel, whatever it does, runs on the same four steps: something happens, a
        rule decides which agent cares (a <em>trigger</em>), the agent works on it, and the result
        shows up where people already work.
      </p>
      <Figure name="loop" />

      <H2 index={2} />
      <p>
        Three jobs that have nothing in common run on exactly the same loop. Only the parts plugged
        into it differ. That is the whole idea: one way to run agents, as many ways to use them as
        you have teams.
      </p>
      <Figure name="three-jobs" />

      <H2 index={3} />
      <p>
        Each team assembles its agent from building blocks: the LLM, the agent image it runs in, its
        persona, its skills, its tools and <em>MCP servers</em>, the <em>connectors</em> that bring
        events in, and <em>plugins</em> for anything more structured.
      </p>
      <Figure name="choose" />
      <p>
        A team&apos;s requirement becomes a configuration, skill or tool change, not a code change.
      </p>
      <table className="glance-table">
        <thead>
          <tr>
            <th scope="col">A team needs…</th>
            <th scope="col">With a single-purpose harness</th>
            <th scope="col">With AInsel</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">a different review step</th>
            <td>Change the tool&apos;s code, or live with it</td>
            <td>Change that team&apos;s skill or trigger</td>
          </tr>
          <tr>
            <th scope="row">another model</th>
            <td>Wait until the tool supports it</td>
            <td>Point the agent at it</td>
          </tr>
          <tr>
            <th scope="row">its own way of working</th>
            <td>One process for everyone</td>
            <td>Each team configures its own agents on one platform</td>
          </tr>
        </tbody>
      </table>

      <H2 index={4} />
      <p>
        Below the team&apos;s building blocks sits the platform. These are the parts every
        organisation would otherwise rebuild, and AInsel ships them today.
      </p>
      <Figure name="solved" />
      <ul className="glance-solved">
        {SOLVED.map((s) => (
          <li key={s.feature}>
            <Link to={s.to}>{s.feature}</Link>: {s.detail}
          </li>
        ))}
      </ul>

      <H2 index={5} />
      <p>
        AInsel ships its own <Link to="/docs/mcp">MCP server</Link>. Connect it to the coding agent
        your engineers already use (Claude Code, an IDE assistant, any MCP client) and they manage
        the whole platform in plain language: set up agents, wire triggers, check logs and costs,
        fix a skill. Nobody has to open the console, and the agent can only do what its user is
        allowed to do.
      </p>
      <Figure name="manage" />
      <pre className="glance-transcript" aria-label="Example terminal conversation">
        {`you    Why did the review agent fail last night?
agent  Reading its last invocations and logs…
       The "review-checklist" skill points at a file that was renamed.
you    Fix the skill.
agent  Updated "review-checklist". The next review uses it.`}
      </pre>

      <H2 index={6} />
      <p>
        Teams start by configuring an agent and trying it. Once one of them finds the way that
        works, it becomes a plugin, for example a state machine that walks an issue through
        planning, implementation and review, and every other team gets it with one install.
      </p>
      <Figure name="grow" />

      <H2 index={7} />
      <ul>
        <li>
          <Link to="/docs/why-ainsel">Why AInsel</Link>: the longer argument behind this page.
        </li>
        <li>
          <Link to="/docs/quickstart">Quick Start</Link>: run AInsel and your first agent.
        </li>
        <li>
          <Link to="/docs/architecture">Architecture</Link>: how the pieces fit together.
        </li>
      </ul>
    </div>
  )
}
