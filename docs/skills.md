# Skills

Skills are reusable prompt fragments that agents can reference. A skill encapsulates a piece of expertise — how to review code, how to triage an issue, how to write a commit message — as a named, versioned block of text that any persona can include.

## Why skills?

Without skills, every persona has to contain all the instructions it needs inline. If two agents both need to know how to write a good PR review, you duplicate that knowledge in both personas. Skills solve this:

- **Reuse** — define a skill once, reference it from any persona.
- **Consistency** — update a skill and every persona that uses it picks up the change.
- **Separation** — keep personas focused on role and tone; move domain expertise into skills.

## Managing skills

Skills live at `/skills` in the UI.

- **List** (`/skills`) — shows all skills with name, description, and last-modified date.
- **Create** (`/skills/new`) — define a skill with a name, a short description, and a body (the prompt text).
- **Edit** (`/skills/:id/edit`) — modify the name, description, or body.
- **Detail** (`/skills/:id`) — view the full skill content.

A skill's **body** is free-form text — typically markdown instructions or a prompt template. How it's consumed depends on the agent runtime; the persona references the skill by name and the runtime injects the body into the prompt at invocation time.

## Who gets a skill

**Each agent picks its own skills.** The selection lives on the agent
(`Agent.spec.skills`, edited in the **Skills** section of an agent's workspace), not
on the runtime image — an image is a runtime profile and ships no skill defaults, so
two agents on the same image can run different skill sets, and an agent keeps its
skills whichever image it runs on.

Two ways to change a selection:

- **From the agent** — Skills section at `/agents/:name`: pick any skills in the library.
- **From the skill** — the *Assigned to agents* panel on `/skills/:id`: add or remove
  agents. This writes each agent's CR; it never touches the image.

Both write the same field, so they can't disagree. `PUT /api/v1/skills/{id}/assignments/{agentName}`
and its `DELETE` twin are the endpoints behind it (see
[API reference](api-reference.md#skill-assignments)).

At runtime the hub renders the skills an agent selects into that pod
(`/var/agent-skills`, copied into pi's skills directory at start), so they appear in
the agent's skill list without a catalog round-trip. Skills the agent was *not* given
stay reachable through the catalogue MCP endpoint, which costs context only when the
agent actually asks for one.

Deleting a skill that at least one agent still selects fails with `409 Conflict` and
lists the agents to fix first.

## Using skills in personas

When writing a persona, reference skills by name. The agent runtime resolves skill references at invocation time, so updating a skill immediately affects all personas that use it — no persona republish needed.