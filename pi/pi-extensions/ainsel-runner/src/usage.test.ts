import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { resolveModelEnv } from "./config.ts";

import {
	costOf,
	counterValue,
	finiteNumberOrUndefined,
	hasRecordableUsage,
	recordUsage,
	sessionModel,
	summaryConversationMessage,
	tokenComponents,
} from "./usage.ts";

describe("tokenComponents", () => {
	it("publishes all four components when the provider reports cache traffic", () => {
		// Pi's usage.input excludes cache traffic and totalTokens is the sum of
		// all four, so dropping the cache components would under-report the
		// 91% of this prompt that came from cache.
		const out = tokenComponents({
			input: 500,
			output: 200,
			cacheRead: 6000,
			cacheWrite: 300,
			totalTokens: 7000,
		});
		assert.deepEqual(out, [
			["input", 500],
			["output", 200],
			["cache_read", 6000],
			["cache_write", 300],
		]);
	});

	it("omits cache components for providers that never report them", () => {
		// No dead series for providers without prompt caching.
		const out = tokenComponents({ input: 10, output: 20, totalTokens: 30 });
		assert.deepEqual(out, [
			["input", 10],
			["output", 20],
		]);
	});

	it("keeps a pure cache hit, which has no billable input", () => {
		// Regression guard: this message has totalTokens > 0 but input 0, so it
		// must still contribute its cache read rather than vanish. The reported
		// zeros stay too (issue #75): they are measurements, not absences.
		const out = tokenComponents({ input: 0, output: 0, cacheRead: 4096, cacheWrite: 0, totalTokens: 4096 });
		assert.deepEqual(out, [
			["input", 0],
			["output", 0],
			["cache_read", 4096],
			["cache_write", 0],
		]);
	});

	it("publishes reported zeros and drops negative, non-numeric and non-finite values", () => {
		// Issue #75: a reported 0 is a measurement — dropping it would collapse
		// "the provider says zero" into "the provider says nothing". Negative
		// and non-numeric values stay garbage: they are dropped.
		assert.deepEqual(tokenComponents({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }), [
			["input", 0],
			["output", 0],
			["cache_read", 0],
			["cache_write", 0],
		]);
		assert.deepEqual(tokenComponents({ input: -5, output: 10 }), [["output", 10]]);
		assert.deepEqual(tokenComponents({ input: "10", output: 10 }), [["output", 10]]);
		assert.deepEqual(tokenComponents({ input: Number.NaN, output: Number.POSITIVE_INFINITY }), []);
	});

	it("tolerates a missing usage object", () => {
		assert.deepEqual(tokenComponents(undefined), []);
		assert.deepEqual(tokenComponents(null), []);
	});
});

describe("costOf", () => {
	it("reads pi's usage.cost.total", () => {
		assert.equal(costOf({ cost: { total: 0.0123 } }), 0.0123);
	});

	it("reads 0 as a measured free run", () => {
		// Issue #75: a priced model on a free run reports total 0 — that is a
		// measurement and must be raised, not dropped.
		assert.equal(costOf({ cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }), 0);
	});

	it("reads undefined when no figure is reported", () => {
		// An unpriced model reports no cost figure at all: undefined, so the
		// runner raises no series and the hub says "not reported" (issue #75).
		assert.equal(costOf({}), undefined);
		assert.equal(costOf(undefined), undefined);
		assert.equal(costOf(null), undefined);
		assert.equal(costOf({ cost: { total: "3" } }), undefined);
		assert.equal(costOf({ cost: { total: Number.NaN } }), undefined);
		assert.equal(costOf({ cost: { total: -1 } }), undefined);
	});
});

describe("finiteNumberOrUndefined", () => {
	it("keeps a reported zero as a measured zero", () => {
		// Issue #75: 0 is a measurement (real 0% cache hit, free run) and must
		// survive to the ingest payload, not be flattened into absence.
		assert.equal(finiteNumberOrUndefined(0), 0);
		assert.equal(finiteNumberOrUndefined(4096), 4096);
	});

	it("maps absent and non-numeric figures to undefined", () => {
		assert.equal(finiteNumberOrUndefined(undefined), undefined);
		assert.equal(finiteNumberOrUndefined(null), undefined);
		assert.equal(finiteNumberOrUndefined("10"), undefined);
		assert.equal(finiteNumberOrUndefined(Number.NaN), undefined);
		assert.equal(finiteNumberOrUndefined(Number.POSITIVE_INFINITY), undefined);
	});

	it("drops negatives like tokenComponents does", () => {
		// The twin claim must hold: a garbage negative forwarded to the hub
		// would be stored and flip COUNT(col) > 0 to "reported" (issue #75).
		assert.equal(finiteNumberOrUndefined(-5), undefined);
	});
});

describe("hasRecordableUsage", () => {
	it("is true for any positive token component", () => {
		assert.equal(hasRecordableUsage({ input: 0, output: 0, cacheRead: 4096, cacheWrite: 0 }), true);
		assert.equal(hasRecordableUsage({ input: 1, output: 0 }), true);
	});

	it("is true for priced tokens pi reports without any token counts", () => {
		assert.equal(hasRecordableUsage({ cost: { total: 0.01 } }), true);
	});

	it("is false for absent, empty and all-zero usage", () => {
		// The guard the old role check replaced: these must stay out of both
		// destinations rather than raising a zero series.
		assert.equal(hasRecordableUsage(undefined), false);
		assert.equal(hasRecordableUsage(null), false);
		assert.equal(hasRecordableUsage({}), false);
		assert.equal(hasRecordableUsage({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: { total: 0 } }), false);
	});
});

// The counter labels come from the env the runner sets per task, so a counter
// assertion has to pin them and put them back afterwards.
function withEventEnv(labels: Record<string, string>, fn: () => Promise<void> | void): Promise<void> {
	const keys = [
		"AINSEL_EVENT_AGENT",
		"AINSEL_EVENT_REPO",
		"AINSEL_EVENT_ORG",
		"AINSEL_EVENT_TYPE",
		"AINSEL_LLM_MODEL",
		"OLLAMA_CLOUD_MODEL",
	];
	const saved: Record<string, string | undefined> = {};
	for (const key of keys) {
		saved[key] = process.env[key];
		delete process.env[key];
	}
	for (const [key, value] of Object.entries(labels)) process.env[key] = value;
	const restore = () => {
		for (const key of keys) {
			if (saved[key] === undefined) delete process.env[key];
			else process.env[key] = saved[key] as string;
		}
	};
	try {
		return Promise.resolve(fn()).then(restore, (err) => {
			restore();
			throw err;
		});
	} catch (err) {
		restore();
		throw err;
	}
}

describe("recordUsage", () => {
	it("books every token component and the cost of a completion", () => {
		// A compaction prompt is the whole conversation: most of it comes back
		// as cache traffic, which only shows up if each component is raised.
		withEventEnv(
			{
				AINSEL_EVENT_AGENT: "olli",
				AINSEL_EVENT_REPO: "ainsel-project/ainsel",
				AINSEL_EVENT_ORG: "ainsel-project",
				AINSEL_EVENT_TYPE: "issues.opened",
			},
			async () => {
				const booked = recordUsage(
					{
						input: 30_000,
						output: 900,
						cacheRead: 12_000,
						cacheWrite: 0,
						totalTokens: 42_900,
						cost: { input: 0.1, output: 0.02, cacheRead: 0.01, cacheWrite: 0, total: 0.13 },
					},
					"compaction-model-a",
					"compaction",
				);
				assert.equal(booked, true);
				const labels = { model: "compaction-model-a" };
				assert.equal(await counterValue("agent_tokens_used_total", { ...labels, token_type: "input" }), 30_000);
				assert.equal(await counterValue("agent_tokens_used_total", { ...labels, token_type: "output" }), 900);
				assert.equal(await counterValue("agent_tokens_used_total", { ...labels, token_type: "cache_read" }), 12_000);
				// Issue #75: a reported 0 raises a 0-valued series — the value
				// assertion cannot tell that apart from a missing series, but the
				// series is real and the hub reads it as a measured zero.
				assert.equal(await counterValue("agent_tokens_used_total", { ...labels, token_type: "cache_write" }), 0);
				assert.equal(await counterValue("agent_cost_usd_total", labels), 0.13);
			},
		);
	});

	it("books nothing for absent or all-zero usage", () => {
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli" }, async () => {
			assert.equal(recordUsage(undefined, "zero-model"), false);
			assert.equal(recordUsage({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, "zero-model"), false);
			assert.equal(await counterValue("agent_tokens_used_total", { model: "zero-model", token_type: "input" }), 0);
		});
	});

	it("counts a compaction completion once, not again with the neighbouring assistant message", () => {
		// The compaction completion is its own LLM call: pi never emits it as a
		// message, so the assistant message before it does not already carry
		// these tokens. Booking it once here and once from message_end would
		// double the largest prompt in the session.
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: "once-model" }, async () => {
			const assistantUsage = { input: 100, output: 20, cacheRead: 0, cacheWrite: 0, totalTokens: 120, cost: { total: 0.001 } };
			const compactionUsage = { input: 30_000, output: 500, cacheRead: 0, cacheWrite: 0, totalTokens: 30_500, cost: { total: 0.05 } };
			// What the message_end handler does for the assistant message…
			recordUsage(assistantUsage, "once-model");
			// …and what the session_compact handler does for the summary.
			recordUsage(compactionUsage, "once-model", "compaction");
			assert.equal(await counterValue("agent_tokens_used_total", { model: "once-model", token_type: "input" }), 30_100);
			assert.equal(await counterValue("agent_tokens_used_total", { model: "once-model", token_type: "output" }), 520);
			// Float sum of two counter increments: compare with a tolerance.
			assert.ok(Math.abs((await counterValue("agent_cost_usd_total", { model: "once-model" })) - 0.051) < 1e-9);
		});
	});
});

describe("summaryConversationMessage", () => {
	it("turns a compaction entry into a system row carrying its usage", () => {
		const usage = { input: 40_000, output: 800, cacheRead: 512, cacheWrite: 0, totalTokens: 41_312, cost: { total: 0.2 } };
		const row = summaryConversationMessage(
			{ type: "compaction", summary: "the conversation so far", usage, firstKeptEntryId: "e1", tokensBefore: 40_000 },
			"compaction",
			"gpt-x",
		);
		assert.ok(row);
		assert.equal(row.role, "system");
		assert.equal(row.stopReason, "compaction");
		assert.equal(row.model, "gpt-x");
		assert.equal(row.usage, usage);
		assert.deepEqual(row.content, [{ type: "text", text: "the conversation so far" }]);
	});

	it("returns null when the entry carries no usage", () => {
		// Nothing to record: the row would be noise in the transcript and the
		// hub's Postgres totals are preferred over the counters.
		assert.equal(summaryConversationMessage({ summary: "s" }, "compaction", "m"), null);
		assert.equal(
			summaryConversationMessage({ summary: "s", usage: { input: 0, output: 0, cost: { total: 0 } } }, "branch_summary", "m"),
			null,
		);
		assert.equal(summaryConversationMessage(undefined, "compaction", "m"), null);
	});

	it("keeps a summary pi left empty", () => {
		const row = summaryConversationMessage({ usage: { input: 5, output: 1 } }, "branch_summary", "m");
		assert.deepEqual(row.content, [{ type: "text", text: "" }]);
	});
});

describe("resolveModelEnv / sessionModel (issue #96 model contract)", () => {
	function withModelEnv(labels: Record<string, string | undefined>, fn: () => void): void {
		const keys = ["AINSEL_LLM_MODEL", "OLLAMA_CLOUD_MODEL"];
		const saved: Record<string, string | undefined> = {};
		for (const key of keys) {
			saved[key] = process.env[key];
			delete process.env[key];
		}
		for (const [key, value] of Object.entries(labels)) {
			if (value !== undefined) process.env[key] = value;
		}
		try {
			fn();
		} finally {
			for (const key of keys) {
				if (saved[key] === undefined) delete process.env[key];
				else process.env[key] = saved[key] as string;
			}
		}
	}

	it("prefers AINSEL_LLM_MODEL over the legacy alias", () => {
		withModelEnv({ AINSEL_LLM_MODEL: "canonical-model", OLLAMA_CLOUD_MODEL: "legacy-model" }, () => {
			assert.equal(resolveModelEnv(), "canonical-model");
			assert.equal(sessionModel(), "canonical-model");
		});
	});

	it("falls back to OLLAMA_CLOUD_MODEL (one-release alias)", () => {
		withModelEnv({ OLLAMA_CLOUD_MODEL: "legacy-model" }, () => {
			assert.equal(resolveModelEnv(), "legacy-model");
			assert.equal(sessionModel(), "legacy-model");
		});
	});

	it("returns the empty string when neither name is set (no default model)", () => {
		withModelEnv({}, () => {
			assert.equal(resolveModelEnv(), "");
			assert.equal(sessionModel(), "");
		});
	});

	it("treats a blank AINSEL_LLM_MODEL as unset and falls through to the alias", () => {
		withModelEnv({ AINSEL_LLM_MODEL: "  ", OLLAMA_CLOUD_MODEL: "legacy-model" }, () => {
			assert.equal(resolveModelEnv(), "legacy-model");
		});
	});
});
