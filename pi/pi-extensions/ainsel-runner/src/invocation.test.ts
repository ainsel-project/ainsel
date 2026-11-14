import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { withInvocationEnv } from "./invocation.ts";

describe("withInvocationEnv", () => {
	// The task pipeline uses this to expose the running task's invocation id
	// on process.env so the in-process MCP catalog can tag plugin calls.
	function withCleanEnv(fn: () => Promise<void>): Promise<void> {
		const saved = process.env.AINSEL_INVOCATION_ID;
		delete process.env.AINSEL_INVOCATION_ID;
		return fn().finally(() => {
			if (saved === undefined) delete process.env.AINSEL_INVOCATION_ID;
			else process.env.AINSEL_INVOCATION_ID = saved;
		});
	}

	it("sets AINSEL_INVOCATION_ID while fn runs and clears it after resolve", async () => {
		await withCleanEnv(async () => {
			let seenDuringFn: string | undefined = "untouched";
			const result = await withInvocationEnv("inv-42", async () => {
				seenDuringFn = process.env.AINSEL_INVOCATION_ID;
				return "ok";
			});
			assert.equal(result, "ok");
			assert.equal(seenDuringFn, "inv-42");
			assert.equal(process.env.AINSEL_INVOCATION_ID, undefined);
		});
	});

	it("clears AINSEL_INVOCATION_ID after fn rejects", async () => {
		await withCleanEnv(async () => {
			let seenDuringFn: string | undefined = "untouched";
			await assert.rejects(
				withInvocationEnv("inv-err", async () => {
					seenDuringFn = process.env.AINSEL_INVOCATION_ID;
					throw new Error("boom");
				}),
				(err: unknown) => err instanceof Error && err.message === "boom",
			);
			assert.equal(seenDuringFn, "inv-err");
			assert.equal(process.env.AINSEL_INVOCATION_ID, undefined);
		});
	});

	it("restores the previous value after fn resolves", async () => {
		await withCleanEnv(async () => {
			process.env.AINSEL_INVOCATION_ID = "outer-inv";
			const result = await withInvocationEnv("inner-inv", async () => {
				assert.equal(process.env.AINSEL_INVOCATION_ID, "inner-inv");
				return 7;
			});
			assert.equal(result, 7);
			assert.equal(process.env.AINSEL_INVOCATION_ID, "outer-inv");
		});
	});

	it("an empty invocation id clears a previously set value for fn", async () => {
		await withCleanEnv(async () => {
			process.env.AINSEL_INVOCATION_ID = "stale-inv";
			let seenDuringFn: string | undefined = "untouched";
			await withInvocationEnv("", async () => {
				seenDuringFn = process.env.AINSEL_INVOCATION_ID;
			});
			assert.equal(seenDuringFn, undefined);
			// The pre-existing value is restored once fn completes.
			assert.equal(process.env.AINSEL_INVOCATION_ID, "stale-inv");
		});
	});
});
