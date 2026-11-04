# Chat Sessions

AInsel's chat feature lets you talk directly to an agent from the hub UI — no webhook or trigger required. This is useful for ad-hoc questions, debugging an agent's behaviour, or testing a persona before wiring it to a trigger.

## Starting a chat

Chat lives inside the agent workspace, not in a sidebar page of its own. Open an
agent (**Fleet → Agents**, or any `/agents/:id` link) and select the **Chat**
section — `/agents/:id/chat`. Type a message and send it; the first message
creates the session.

Each agent keeps one conversation with you. **Reset** in the section header
starts a fresh conversation, giving the agent a clean context instead of the
current session's history.

The agent uses its configured persona, model, and tools — the same configuration it uses when responding to events. Messages you send are delivered to the agent runtime; the agent's reply is streamed back and rendered as markdown.

## The chat view

- **Message input** — Type a message and press **Enter** to send. Use **Shift+Enter** for a newline.
- **Message history** — Previous messages in the session are displayed with relative timestamps.
- **Status messages** — System events (session created, agent started, etc.) appear as centred status lines.

## Sessions

Sessions are per-user — each operator sees only their own chats. The section always opens your most recent session with that agent; after a reset, older sessions simply stay out of view. Old links keep working: `/chat` redirects to the agent list, and `/chat/<session-id>` opens that session's agent at its Chat section.

## When to use chat vs triggers

- **Chat** — interactive, on-demand conversations. Good for questions, testing, and debugging.
- **Triggers** — automated, event-driven. Good for production workflows like PR review and issue triage.

Chat does not invoke triggers; it's a separate interaction path that goes directly to the agent runtime.