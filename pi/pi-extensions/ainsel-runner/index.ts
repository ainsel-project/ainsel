/**
 * Ainsel Runner Extension for Pi
 *
 * This extension is the entire event loop of the pi-native ainsel agent
 * runtime. It runs inside a `pi --mode rpc` process that a Kubernetes Job
 * started for exactly one task, and:
 *
 *   1. On `session_start`, fetches the task named by AINSEL_TASK_ID from the
 *      hub. There is no poll loop and no claim: the task row stays 'pending'
 *      until this run acks it, so a pod that dies mid-task strands nothing —
 *      the Job restarts the same row.
 *   2. For that task:
 *        a. Builds an `<event>…</event>` system block and injects it
 *           as the user message body.
 *        b. Calls `pi.sendUserMessage("Handle …")` to start a turn.
 *        c. Awaits turn completion.
 *        d. ACKs or NACKs the task via the hub REST API and exits; the Job
 *           owns retries.
 *
 * Required env (operator already sets all of these):
 *   HUB_URL, AGENT_NAME, HUB_INTERNAL_VALIDATE_SECRET, AINSEL_LLM_MODEL,
 *   AINSEL_TASK_ID (the task this container exists to run; the runner exits
 *   if it is missing — a pod without a task is a misconfiguration).
 *   AINSEL_LLM_MODEL may also arrive under its legacy alias
 *   OLLAMA_CLOUD_MODEL (accepted for one release, issue #96).
 *
 * Optional env:
 *   NAK_DELAY_MS           default 60000 — base NACK backoff (ms); the hub
 *                          doubles it per attempt and caps it at 30 minutes
 *   TOOL_RESULT_MAX_CHARS  default 16000 — per-text-block cap for toolResult
 *                          transcript content (chars, UTF-16 code units)
 *   EVENT_DATA_MAX_CHARS   default 16000 — cap for the raw event payload
 *                          inlined into the prompt as <event-data>
 *   POST_TIMEOUT_MS        default 10000 — per-request timeout (ms) for the
 *                          conversation POSTs to the hub
 *   AINSEL_TASK_DIR        set by the operator on per-task Jobs: a directory
 *                          shared with the pod's sidecars, where this process
 *                          records its PID so they can end with the task
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

import { requiredEnv, requiredModel, resolveInternalToken } from "./src/config.ts";
import { logError, logInfo } from "./src/logging.ts";
import { startMetricsServer } from "./src/usage.ts";
import {
	createTurnTracker,
	endTurn,
	flushTurnMessages,
	markSettled,
} from "./src/turn-tracker.ts";
import { writeAgentPidFile } from "./src/pidfile.ts";
import { registerUsageEvents, runOneShot } from "./src/task-loop.ts";

export default function ainselRunnerExtension(pi: ExtensionAPI) {
	const tracker = createTurnTracker();
	registerUsageEvents(pi, tracker);

	// turn_end fires after EACH LLM response in the tool-use loop — too early
	// to resolve the task, but the turn's assistant message and tool results
	// are complete by then, so it is the right point to post the transcript
	// incrementally for live conversation tracking.
	pi.on("turn_end", (event) => {
		logInfo("turn ended", { turn_index: event.turnIndex });
		flushTurnMessages(tracker);
	});

	// agent_settled fires once when the entire agent run is complete (all
	// turns, retries, compaction). This is the correct signal to resolve
	// the task promise and ACK/NACK.
	pi.on("agent_settled", () => {
		endTurn(tracker);
		markSettled(tracker);
	});

	// Strip accumulated conversation history between events.
	pi.on("context", (event) => {
		const msgs = event.messages;
		let startIdx = 0;
		for (let i = msgs.length - 1; i >= 0; i--) {
			const msg = msgs[i] as { role?: string; content?: unknown };
			if (msg.role === "user") {
				const text =
					typeof msg.content === "string"
						? msg.content
						: Array.isArray(msg.content)
							? ((msg.content as Array<{ type?: string; text?: string }>).find(
									(c) => c.type === "text",
								)?.text ?? "")
							: "";
				if (text.startsWith("<event>")) {
					startIdx = i;
					break;
				}
			}
		}
		if (startIdx > 0) {
			return { messages: msgs.slice(startIdx) };
		}
		return {};
	});

	let started = false;
	pi.on("session_start", async (_event, ctx) => {
		if (started) return;
		started = true;

		// First act: tell the pod's sidecars that a runtime is alive in here. A
		// turn that never ends and a runtime that never started look identical to
		// a sidecar unless it is told which is which.
		writeAgentPidFile();

		let agentName: string;
		let hubUrl: string;
		let token: string;
		let model: string;
		try {
			agentName = requiredEnv("AGENT_NAME");
			hubUrl = requiredEnv("HUB_URL");
			token = resolveInternalToken();
			model = requiredModel();
		} catch {
			return;
		}

		logInfo("ainsel-runner starting", {
			agent: agentName,
			hub_url: hubUrl,
			model,
		});

		startMetricsServer();

		// The operator names the task this container exists to run. A pod
		// without one is a misconfiguration — refuse to sit idle pretending to
		// be a worker, and exit nonzero so the mistake is loud.
		const oneShotTaskId = process.env.AINSEL_TASK_ID;
		if (!oneShotTaskId) {
			logError("AINSEL_TASK_ID is not set; this runtime only runs as a per-task Job");
			process.exit(1);
			return;
		}
		runOneShot(pi, ctx, agentName, model, hubUrl, token, tracker, Number(oneShotTaskId)).catch((err) => {
			logError("one-shot run crashed", {
				err: err instanceof Error ? err.message : String(err),
			});
			process.exit(1);
		});
	});
}
