import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { capToolResultContent, truncateWithMarker } from "./caps.ts";

describe("truncateWithMarker", () => {
	it("returns input unchanged when at or below the cap", () => {
		assert.equal(truncateWithMarker("hello", 10), "hello");
		assert.equal(truncateWithMarker("hello", 5), "hello");
	});

	it("truncates with head+tail and marker when over the cap", () => {
		const input = "A".repeat(100);
		const result = truncateWithMarker(input, 40);
		// head = 30, tail = 10, omitted = 60
		assert.ok(result.startsWith("A".repeat(30)), "head must survive");
		assert.ok(result.endsWith("A".repeat(10)), "tail must survive");
		assert.ok(result.includes("... [truncated 60 chars] ..."), "marker must include omitted count");
	});

	it("handles cap+1 boundary", () => {
		const input = "B".repeat(41);
		const result = truncateWithMarker(input, 40);
		assert.ok(result.includes("[truncated"), "cap+1 must be truncated");
		// Exactly at cap is untouched
		assert.equal(truncateWithMarker("B".repeat(40), 40), "B".repeat(40));
	});
});

describe("capToolResultContent", () => {
	it("caps an oversized string", () => {
		const big = "X".repeat(200);
		const result = capToolResultContent(big, 100) as string;
		assert.ok(result.includes("[truncated"), "must contain truncation marker");
		assert.ok(result.length < big.length, "must be shorter than input");
		assert.ok(result.startsWith("X".repeat(75)), "head must survive");
		assert.ok(result.endsWith("X".repeat(25)), "tail must survive");
	});

	it("leaves a small string unchanged", () => {
		assert.equal(capToolResultContent("small", 100), "small");
	});

	it("caps text blocks in an array and reduces non-text blocks", () => {
		const content = [
			{ type: "text", text: "Y".repeat(200) },
			{ type: "image", url: "http://x/y.png" },
			{ type: "text", text: "small" },
		];
		const result = capToolResultContent(content, 100) as any[];
		assert.equal(result.length, 3);
		// First text block capped
		assert.ok(result[0].text.includes("[truncated"), "large text block must be capped");
		// Image block reduced to { type }
		assert.deepEqual(result[1], { type: "image" });
		// Small text block untouched
		assert.deepEqual(result[2], { type: "text", text: "small" });
	});

	it("passes arbitrary objects through unchanged", () => {
		const obj = { foo: "bar", nested: { a: 1 } };
		assert.deepEqual(capToolResultContent(obj, 10), obj);
	});

	it("respects explicit maxChars parameter", () => {
		const input = "Z".repeat(50);
		const result = capToolResultContent(input, 20) as string;
		assert.ok(result.includes("[truncated"), "must truncate at explicit cap");
	});

	it("reads TOOL_RESULT_MAX_CHARS env lazily", () => {
		const saved = process.env.TOOL_RESULT_MAX_CHARS;
		try {
			process.env.TOOL_RESULT_MAX_CHARS = "30";
			const input = "W".repeat(50);
			const result = capToolResultContent(input) as string;
			assert.ok(result.includes("[truncated"), "must respect env cap");
		} finally {
			if (saved === undefined) delete process.env.TOOL_RESULT_MAX_CHARS;
			else process.env.TOOL_RESULT_MAX_CHARS = saved;
		}
	});
});
