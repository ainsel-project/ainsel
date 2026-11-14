import { describe, it } from "node:test";

import assert from "node:assert/strict";

import {
	envInt,
	resolveEventDataMaxChars,
	resolveInternalToken,
	resolveNakDelayMs,
	resolvePostTimeoutMs,
	resolveToolResultMaxChars,
	resolveTurnSettleTimeoutMs,
	resolveTurnTimeoutMs,
} from "./config.ts";

describe("resolveEventDataMaxChars", () => {
	it("returns the default when unset", () => {
		const prev = process.env.EVENT_DATA_MAX_CHARS;
		delete process.env.EVENT_DATA_MAX_CHARS;
		try {
			assert.equal(resolveEventDataMaxChars(), 16_000);
		} finally {
			if (prev !== undefined) process.env.EVENT_DATA_MAX_CHARS = prev;
		}
	});

	it("ignores invalid values", () => {
		const prev = process.env.EVENT_DATA_MAX_CHARS;
		try {
			process.env.EVENT_DATA_MAX_CHARS = "banana";
			assert.equal(resolveEventDataMaxChars(), 16_000);
			process.env.EVENT_DATA_MAX_CHARS = "-5";
			assert.equal(resolveEventDataMaxChars(), 16_000);
			process.env.EVENT_DATA_MAX_CHARS = "0";
			assert.equal(resolveEventDataMaxChars(), 16_000);
		} finally {
			if (prev === undefined) delete process.env.EVENT_DATA_MAX_CHARS;
			else process.env.EVENT_DATA_MAX_CHARS = prev;
		}
	});

	it("honours a valid override", () => {
		const prev = process.env.EVENT_DATA_MAX_CHARS;
		try {
			process.env.EVENT_DATA_MAX_CHARS = "512";
			assert.equal(resolveEventDataMaxChars(), 512);
		} finally {
			if (prev === undefined) delete process.env.EVENT_DATA_MAX_CHARS;
			else process.env.EVENT_DATA_MAX_CHARS = prev;
		}
	});
});

describe("resolveToolResultMaxChars", () => {
	it("returns default when env is unset", () => {
		const saved = process.env.TOOL_RESULT_MAX_CHARS;
		try {
			delete process.env.TOOL_RESULT_MAX_CHARS;
			assert.equal(resolveToolResultMaxChars(), 16_000);
		} finally {
			if (saved !== undefined) process.env.TOOL_RESULT_MAX_CHARS = saved;
		}
	});

	it("returns default for invalid env values", () => {
		const saved = process.env.TOOL_RESULT_MAX_CHARS;
		try {
			process.env.TOOL_RESULT_MAX_CHARS = "not-a-number";
			assert.equal(resolveToolResultMaxChars(), 16_000);
			process.env.TOOL_RESULT_MAX_CHARS = "-5";
			assert.equal(resolveToolResultMaxChars(), 16_000);
			process.env.TOOL_RESULT_MAX_CHARS = "0";
			assert.equal(resolveToolResultMaxChars(), 16_000);
		} finally {
			if (saved === undefined) delete process.env.TOOL_RESULT_MAX_CHARS;
			else process.env.TOOL_RESULT_MAX_CHARS = saved;
		}
	});

	it("returns the parsed env value when valid", () => {
		const saved = process.env.TOOL_RESULT_MAX_CHARS;
		try {
			process.env.TOOL_RESULT_MAX_CHARS = "5000";
			assert.equal(resolveToolResultMaxChars(), 5000);
		} finally {
			if (saved === undefined) delete process.env.TOOL_RESULT_MAX_CHARS;
			else process.env.TOOL_RESULT_MAX_CHARS = saved;
		}
	});
});

describe("resolveInternalToken", () => {
	it("returns the platform-managed HUB_INTERNAL_VALIDATE_SECRET", () => {
		const token = resolveInternalToken({
			HUB_INTERNAL_VALIDATE_SECRET: "platform-token",
		});
		assert.equal(token, "platform-token");
	});

	it("ignores a legacy AGENT_TOKEN value", () => {
		const token = resolveInternalToken({
			HUB_INTERNAL_VALIDATE_SECRET: "platform-token",
			AGENT_TOKEN: "per-agent-token",
		});
		assert.equal(token, "platform-token");
		assert.throws(() => resolveInternalToken({ AGENT_TOKEN: "per-agent-token" }), /HUB_INTERNAL_VALIDATE_SECRET is not set/);
	});

	it("throws when the platform secret is missing or empty", () => {
		assert.throws(() => resolveInternalToken({}), /HUB_INTERNAL_VALIDATE_SECRET is not set/);
		assert.throws(() => resolveInternalToken({ HUB_INTERNAL_VALIDATE_SECRET: "" }), /HUB_INTERNAL_VALIDATE_SECRET is not set/);
	});
});

describe("envInt", () => {
	// Every tunable goes through envInt, so the whole env surface shares these
	// cases: only a parseInt-readable, positive value wins; everything else
	// (unset, empty, garbage, zero, negative) falls back.
	const KEY = "AINSEL_TEST_INT";

	function withEnv(value: string | undefined, fn: () => void) {
		const prev = process.env[KEY];
		try {
			if (value === undefined) delete process.env[KEY];
			else process.env[KEY] = value;
			fn();
		} finally {
			if (prev === undefined) delete process.env[KEY];
			else process.env[KEY] = prev;
		}
	}

	it("falls back when unset or empty", () => {
		withEnv(undefined, () => assert.equal(envInt(KEY, 42), 42));
		withEnv("", () => assert.equal(envInt(KEY, 42), 42));
	});

	it("falls back for non-numeric, zero and negative values", () => {
		for (const bad of ["abc", "0", "-5"]) {
			withEnv(bad, () => assert.equal(envInt(KEY, 42), 42));
		}
	});

	it("accepts a trailing suffix the way parseInt does", () => {
		// "12abc" parsed to 12 before this helper existed; keep that.
		withEnv("12abc", () => assert.equal(envInt(KEY, 42), 12));
	});

	it("returns the parsed value", () => {
		withEnv("7", () => assert.equal(envInt(KEY, 42), 7));
	});
});

describe("millisecond tunables", () => {
	// The four tunables that used to be four copies of the same IIFE. Each is
	// read lazily, so an operator can change one and the next call sees it.
	const cases: [string, () => number, number][] = [
		["NAK_DELAY_MS", resolveNakDelayMs, 60 * 1000],
		["POST_TIMEOUT_MS", resolvePostTimeoutMs, 10_000],
		["TURN_TIMEOUT_MS", resolveTurnTimeoutMs, 10 * 60 * 1000],
		["TURN_SETTLE_TIMEOUT_MS", resolveTurnSettleTimeoutMs, 10 * 1000],
	];

	for (const [name, read, fallback] of cases) {
		it(`${name} falls back for unset/invalid and honours a valid override`, () => {
			const prev = process.env[name];
			try {
				for (const bad of [undefined, "", "abc", "0", "-5"]) {
					if (bad === undefined) delete process.env[name];
					else process.env[name] = bad;
					assert.equal(read(), fallback, `${name}=${bad}`);
				}
				process.env[name] = "1234";
				assert.equal(read(), 1234);
			} finally {
				if (prev === undefined) delete process.env[name];
				else process.env[name] = prev;
			}
		});
	}
});
