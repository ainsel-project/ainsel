import { test } from "node:test";
import assert from "node:assert/strict";
import { runCheck } from "./check.mjs";
import { EXIT_SIGN_IN_REQUIRED } from "./lib.mjs";

// A ModelRuntime stand-in: getAuth runs the given steps in order, one per call.
function stubRuntime(steps) {
	const calls = { create: [], getAuth: 0 };
	const ModelRuntime = {
		async create(options) {
			calls.create.push(options);
			return {
				async getAuth(provider) {
					assert.equal(provider, "openai");
					const step = steps[Math.min(calls.getAuth, steps.length - 1)];
					calls.getAuth++;
					return step();
				},
			};
		},
	};
	return { ModelRuntime, calls };
}

function chain(message) {
	// The shape pi-ai throws: ModelsError -> ModelsError -> token request error.
	const inner = new Error(message);
	const mid = new Error("OAuth refresh failed for openai", { cause: inner });
	return new Error("OAuth auth derivation failed for openai", { cause: mid });
}

function harness(steps) {
	const { ModelRuntime, calls } = stubRuntime(steps);
	const lines = [];
	const waits = [];
	const opts = {
		ModelRuntime,
		authPath: "/creds/auth.json",
		log: (l) => lines.push(l),
		sleep: async (ms) => waits.push(ms),
	};
	return { opts, calls, lines, waits };
}

const SECRET = "sk-access-token-must-not-leak";

test("exits 0 when getAuth resolves", async () => {
	const h = harness([async () => ({ auth: { apiKey: SECRET } })]);
	assert.equal(await runCheck(h.opts), 0);
	assert.equal(h.calls.create[0].authPath, "/creds/auth.json");
});

test("exits 78 on a 400 invalid_grant cause", async () => {
	const h = harness([
		async () => {
			throw chain('OpenAI OAuth token request failed (400): {"error":"invalid_grant"}');
		},
	]);
	assert.equal(await runCheck(h.opts), EXIT_SIGN_IN_REQUIRED);
	assert.equal(EXIT_SIGN_IN_REQUIRED, 78);
	assert.equal(h.calls.getAuth, 1, "a definite sign-in failure is not retried");
});

test("exits 78 when no credential is stored", async () => {
	const h = harness([async () => undefined]);
	assert.equal(await runCheck(h.opts), 78);
});

test("exits 78 when the stored credential has no client id", async () => {
	const h = harness([
		async () => {
			throw chain("Stored OpenAI OAuth credential does not contain an issued client ID; reconnect ChatGPT");
		},
	]);
	assert.equal(await runCheck(h.opts), 78);
});

test("retries a fetch failure 3 times then exits 1", async () => {
	const h = harness([
		async () => {
			throw chain("fetch failed");
		},
	]);
	assert.equal(await runCheck(h.opts), 1);
	assert.equal(h.calls.getAuth, 3);
	assert.deepEqual(h.waits, [2000, 5000]);
});

test("a server error is transient, not a dead sign-in", async () => {
	const h = harness([
		async () => {
			throw chain("OpenAI OAuth token request failed (503): Service Unavailable");
		},
		async () => ({ auth: { apiKey: SECRET } }),
	]);
	assert.equal(await runCheck(h.opts), 0);
	assert.equal(h.calls.getAuth, 2);
});

test("never prints the access token", async () => {
	const h = harness([async () => ({ auth: { apiKey: SECRET } })]);
	await runCheck(h.opts);
	assert.ok(h.lines.length > 0, "the check reports its outcome");
	for (const l of h.lines) assert.ok(!l.includes(SECRET), `leaked token in: ${l}`);
});

test("describe drops a message repeated down the cause chain", async () => {
	const { describe } = await import("./lib.mjs");
	const inner = new Error("OpenAI OAuth token request failed (400): invalid_client");
	const same = new Error(inner.message, { cause: inner });
	const top = new Error("OAuth refresh failed for openai", { cause: same });
	assert.equal(describe(top), "OAuth refresh failed for openai: OpenAI OAuth token request failed (400): invalid_client");
});
