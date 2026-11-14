/**
 * Usage accounting: the Prometheus counters, the "is this reportable" rules,
 * and the synthetic transcript row for the LLM calls pi never emits as
 * messages.
 *
 * The two counters are constructed at module scope on purpose: `prom.Counter`
 * registers against the process-global registry and throws on a duplicate
 * name, so moving construction into a function (or duplicating the module)
 * breaks container start. `counterValue` reads that same registry, which is
 * why it lives here too.
 */

import * as http from "http";
import * as prom from "prom-client";
import { logError, logInfo } from "./logging.ts";
import { resolveModelEnv } from "./config.ts";
import type { PiMessage, PiUsage } from "./pi-types.ts";



// Prometheus metrics — exported by the agent runtime on :9090/metrics.
//
// token_type is one of: input, output, cache_read, cache_write. Pi reports
// prompt-cache traffic separately from billable input (usage.totalTokens is
// input + output + cacheRead + cacheWrite), so each component needs its own
// series — summing only input + output under-reports what the model actually
// processed by the whole cached volume.
const tokenCounter = new prom.Counter({
	name: "agent_tokens_used_total",
	help: "Total tokens consumed by the agent, by token_type (input, output, cache_read, cache_write)",
	labelNames: ["agent", "repo", "org", "event_type", "token_type", "model"],
});

// The USD counterpart of agent_tokens_used_total, raised from pi's
// usage.cost.total — which pi computes from the model's cost block in the
// generated models.json (spec.llm.cost, issue #281). Presence is meaningful
// (issue #75): an agent whose model carries no cost block reports no figure
// and raises nothing — "not reported" — while a priced model that costs
// nothing raises 0, a measured free run. The hub reads the two apart.
const costCounter = new prom.Counter({
	name: "agent_cost_usd_total",
	help: "Total USD cost incurred by the agent, from pi's usage.cost.total",
	labelNames: ["agent", "repo", "org", "event_type", "model"],
});

/** Maps a pi usage object onto the [token_type, value] pairs to publish.
 *
 *  Pi's usage.input excludes prompt-cache traffic and usage.totalTokens is
 *  input + output + cacheRead + cacheWrite, so each component needs its own
 *  series — publishing only input + output under-reports what the model
 *  processed by the whole cached volume, which dominates an agent loop.
 *
 *  A component is published whenever pi reports it as a finite non-negative
 *  number — including 0 — and dropped only when it is absent, negative or
 *  non-numeric (issue #75). A reported `cached_tokens: 0` is a measured zero hit rate and must
 *  reach Prometheus as a 0-valued series; dropping it would collapse "the
 *  provider says zero" into "the provider says nothing", which is the bug
 *  this guards against. The cost is two zero-valued series per label
 *  combination for un-caching providers — the accepted price of keeping the
 *  distinction.
 *
 *  Pure and exported for unit testing. */
export function tokenComponents(usage: PiUsage | undefined): Array<[string, number]> {
	const candidates: Array<[string, number | undefined]> = [
		["input", usage?.input],
		["output", usage?.output],
		["cache_read", usage?.cacheRead],
		["cache_write", usage?.cacheWrite],
	];
	// The predicate is the filter the original had; it only has to be spelled
	// out for tsc now that the fields are optional instead of `any`.
	return candidates.filter(
		(entry): entry is [string, number] => {
			const value = entry[1];
			return typeof value === "number" && Number.isFinite(value) && value >= 0;
		},
	);
}

/** Extracts the USD cost of a pi usage object (usage.cost.total), or
 *  undefined when it is absent, negative or non-numeric — the shape an
 *  unpriced model reports (issue #75). A reported 0 is returned as 0: it is a
 *  measured free run, not a missing figure. Pure and exported for unit
 *  testing. */
export function costOf(usage: PiUsage | undefined): number | undefined {
	const value = usage?.cost?.total;
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

/** Returns the value when it is a finite non-negative number, undefined
 *  otherwise — the ingest-side twin of tokenComponents' filter: a reported 0
 *  stays 0, an absent or negative figure stays absent instead of being
 *  flattened into a measured zero (issue #75). Dropping negatives matters for
 *  the presence flags: a garbage negative would otherwise be stored and flip
 *  COUNT(col) > 0 to "reported". Pure and exported for unit testing. */
export function finiteNumberOrUndefined(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

/** Whether a pi usage object carries anything worth booking: at least one
 *  positive token component or a positive cost.
 *
 *  This is the predicate the runner records on — "does this carry usage", not
 *  "is it an assistant message". pi types usage on non-assistant messages too
 *  (`ToolResultEventBase.usage`), and compaction/branch-summary entries carry
 *  usage for a one-shot completion that never appears as a message at all, so
 *  a role check drops real consumption (issue #76). Pure and exported for
 *  unit testing. */
export function hasRecordableUsage(usage: PiUsage | undefined): boolean {
	// some(v > 0), not length > 0: tokenComponents keeps reported zeros (issue
	// #75), so length alone would call an all-zero report recordable. costOf
	// may be undefined (unpriced model) — a reported 0 is still not worth a
	// booking on its own; it rides along once any positive token books the row.
	return tokenComponents(usage).some(([, value]) => value > 0) || (costOf(usage) ?? 0) > 0;
}

/** Book one pi LLM call against the Prometheus counters.
 *
 *  Every LLM call the runner can see goes through here — the agent loop's
 *  assistant messages, tool results that report usage, and the one-shot
 *  completions behind compaction and branch summaries, which pi never emits
 *  as messages. `source` only labels the log line so an operator can tell
 *  which path a line came from; the counter labels are the same for all of
 *  them, which is the point: the totals have to add up.
 *
 *  Returns whether anything was booked, so callers can mirror the decision
 *  into the transcript and keep the counter and the row in step. */
export function recordUsage(usage: PiUsage | undefined, model: string, source = "message"): boolean {
	if (!hasRecordableUsage(usage)) return false;
	const agent = process.env.AINSEL_EVENT_AGENT ?? "";
	const repo = process.env.AINSEL_EVENT_REPO ?? "";
	const org = process.env.AINSEL_EVENT_ORG ?? "";
	const eventType = process.env.AINSEL_EVENT_TYPE ?? "";
	const modelLabel = model ?? "";
	for (const [tokenType, value] of tokenComponents(usage)) {
		tokenCounter.inc(
			{ agent, repo, org, event_type: eventType, token_type: tokenType, model: modelLabel },
			value,
		);
	}
	const cost = costOf(usage);
	// Raise the counter whenever pi reports a figure — including 0, which
	// creates the series without moving its value and is exactly the "measured
	// free run" signal the hub distinguishes from "never reported" (issue #75).
	if (cost !== undefined) {
		costCounter.inc({ agent, repo, org, event_type: eventType, model: modelLabel }, cost);
	}
	logInfo("token usage recorded", {
		source,
		agent,
		event_type: eventType,
		input: usage?.input,
		output: usage?.output,
		cache_read: usage?.cacheRead,
		cache_write: usage?.cacheWrite,
		cost_usd: cost,
		model: modelLabel,
	});
	return true;
}

/** Current value of a Prometheus counter series, or 0 when it has never been
 *  raised. Reads the same registry the /metrics endpoint serves, so a test (or
 *  an operator debugging a pod) can assert what the counter side of a usage
 *  report actually booked rather than only what the code path intended.
 *  prom-client's metric snapshot is async, so this is too. */
export async function counterValue(name: string, labels: Record<string, string>): Promise<number> {
	const metric = prom.register.getSingleMetric(name);
	const snapshot = (await metric?.get?.()) as
		| { values?: { labels?: Record<string, string | undefined>; value?: number }[] }
		| undefined;
	for (const series of snapshot?.values ?? []) {
		const seriesLabels = series.labels ?? {};
		const match = Object.entries(labels).every(
			([key, value]) => String(seriesLabels[key] ?? "") === String(value),
		);
		if (match) return series.value ?? 0;
	}
	return 0;
}

/** The model to label a usage report with when the source carries none.
 *
 *  Compaction and branch-summary entries store `usage` but no model — pi
 *  summarises with the model the run is configured for, so the operator's
 *  AINSEL_LLM_MODEL is the same one that produced the tokens. The legacy
 *  OLLAMA_CLOUD_MODEL name is read as a fallback for one release (issue #96)
 *  so a new runtime still works against an operator that only writes the
 *  old name. */
export function sessionModel(): string {
	return resolveModelEnv();
}

/** Build the transcript row for a compaction or branch-summary completion.
 *
 *  Those LLM calls never reach `message_end`, so without this row the hub has
 *  no record of them at all — and the hub prefers its Postgres conversation
 *  rows over the Prometheus counters whenever it has them, so a counter-only
 *  fix would stay invisible in the UI. Returns null when the entry carries no
 *  usage, so a summary pi did not price stays out of both destinations. */
export function summaryConversationMessage(
	entry: { usage?: PiUsage; summary?: string } | null | undefined,
	stopReason: string,
	model: string,
): PiMessage | null {
	if (!entry || !hasRecordableUsage(entry.usage)) return null;
	return {
		role: "system",
		model: model ?? "",
		usage: entry.usage,
		stopReason,
		content: [{ type: "text", text: entry.summary ?? "" }],
	};
}

export function startMetricsServer() {
	const server = http.createServer(async (req, res) => {
		if (req.url === "/metrics") {
			res.writeHead(200, { "Content-Type": "text/plain" });
			res.end(await prom.register.metrics());
		} else {
			res.writeHead(404);
			res.end("Not found");
		}
	});
	server.listen(9090, () => {
		logInfo("metrics server listening", { port: 9090 });
	});
	server.on("error", (err) => {
		logError("metrics server error", { err: err.message });
	});
}

