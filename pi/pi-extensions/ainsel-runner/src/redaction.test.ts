import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { toConversationPayloads } from "./conversation.ts";

import { redactSecrets, resetRedactionCache } from "./redaction.ts";

describe("redactSecrets", () => {
	const savedEnv = { ...process.env };

	function setEnv(vars: Record<string, string>) {
		// Clear all AINSEL/test vars, then set the given ones.
		for (const key of Object.keys(process.env)) {
			if (key.startsWith("TEST_") || key.startsWith("AINSEL_")) delete process.env[key];
		}
		for (const [k, v] of Object.entries(vars)) process.env[k] = v;
		resetRedactionCache();
	}

	function restoreEnv() {
		for (const key of Object.keys(process.env)) {
			if (key.startsWith("TEST_") || key.startsWith("AINSEL_")) delete process.env[key];
		}
		Object.assign(process.env, savedEnv);
		resetRedactionCache();
	}

	it("redacts a secret in assistant text content", () => {
		setEnv({ TEST_SECRET_TOKEN: "super-secret-value-12345" });
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "text", text: "The token is super-secret-value-12345 ok" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.equal(out.length, 1);
			assert.ok(!out[0].content.includes("super-secret-value-12345"), "secret must be redacted");
			assert.ok(out[0].content.includes("***"), "replacement marker must be present");
		} finally {
			restoreEnv();
		}
	});

	it("redacts a secret in thinking blocks", () => {
		setEnv({ TEST_SECRET_TOKEN: "thinking-secret-99999" });
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "thinking", thinking: "I see thinking-secret-99999 here" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(!out[0].content.includes("thinking-secret-99999"));
			assert.ok(out[0].content.includes("***"));
		} finally {
			restoreEnv();
		}
	});

	it("redacts a secret in toolCall arguments", () => {
		setEnv({ TEST_API_KEY: "toolcall-secret-abcdef" });
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "toolCall", id: "tc-1", name: "bash", arguments: { cmd: "echo toolcall-secret-abcdef" } }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(!out[0].content.includes("toolcall-secret-abcdef"));
			assert.ok(out[0].content.includes("***"));
		} finally {
			restoreEnv();
		}
	});

	it("redacts a secret in toolResult content", () => {
		setEnv({ TEST_API_KEY: "toolresult-secret-xyz" });
		try {
			const messages = [
				{ role: "toolResult", toolCallId: "tc-1", isError: false, content: "output: toolresult-secret-xyz" },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(!out[0].content.includes("toolresult-secret-xyz"));
			assert.ok(out[0].content.includes("***"));
		} finally {
			restoreEnv();
		}
	});

	it("redacts a secret containing JSON-escapable characters", () => {
		setEnv({ TEST_JSON_SECRET: 'has"quote/and\\slash' });
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "text", text: 'value is has"quote/and\\slash done' }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			// The raw secret and its JSON-escaped form must both be gone.
			assert.ok(!out[0].content.includes('has"quote'), "raw secret must be redacted");
			assert.ok(!out[0].content.includes('has\\"quote'), "JSON-escaped secret must be redacted");
			assert.ok(out[0].content.includes("***"));
		} finally {
			restoreEnv();
		}
	});

	it("does not redact denylisted non-secret values", () => {
		setEnv({
			AGENT_NAME: "my-agent-name-long",
			HUB_URL: "http://hub.example.com:8080",
			AINSEL_LLM_MODEL: "canonical-model-xyz",
			OLLAMA_CLOUD_MODEL: "llama-3.1-70b-instruct",
		});
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "text", text: "agent my-agent-name-long at http://hub.example.com:8080 using llama-3.1-70b-instruct and canonical-model-xyz" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(out[0].content.includes("my-agent-name-long"), "AGENT_NAME must not be redacted");
			assert.ok(out[0].content.includes("http://hub.example.com:8080"), "HUB_URL must not be redacted");
			assert.ok(out[0].content.includes("llama-3.1-70b-instruct"), "model alias must not be redacted");
			assert.ok(out[0].content.includes("canonical-model-xyz"), "AINSEL_LLM_MODEL must not be redacted");
		} finally {
			restoreEnv();
		}
	});

	it("does not redact AINSEL_EVENT_ prefixed values", () => {
		setEnv({ AINSEL_EVENT_REPO: "my-org/my-repo-long" });
		try {
			const messages = [
				{ role: "user", content: [{ type: "text", text: "working on my-org/my-repo-long" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(out[0].content.includes("my-org/my-repo-long"), "AINSEL_EVENT_ values must not be redacted");
		} finally {
			restoreEnv();
		}
	});

	it("ignores empty and sub-min-length env values", () => {
		setEnv({ TEST_EMPTY: "", TEST_SHORT: "abc" });
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "text", text: "abc is short and should stay" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(out[0].content.includes("abc"), "short values must not be redacted");
		} finally {
			restoreEnv();
		}
	});

	it("redacts multiple distinct secrets in one transcript", () => {
		setEnv({
			TEST_SECRET_A: "first-secret-value-aaa",
			TEST_SECRET_B: "second-secret-value-bbb",
		});
		try {
			const messages = [
				{ role: "assistant", content: [{ type: "text", text: "A=first-secret-value-aaa B=second-secret-value-bbb" }] },
			];
			const meta = { agentName: "a", invocationId: "i", correlationId: "c" };
			const out = toConversationPayloads(messages, meta);
			assert.ok(!out[0].content.includes("first-secret-value-aaa"));
			assert.ok(!out[0].content.includes("second-secret-value-bbb"));
			assert.ok(out[0].content.includes("***"));
		} finally {
			restoreEnv();
		}
	});

	it("redactSecrets returns input unchanged when no secrets match", () => {
		setEnv({ TEST_SECRET_TOKEN: "super-secret-value-12345" });
		try {
			const result = redactSecrets("no secrets here at all");
			assert.equal(result, "no secrets here at all");
		} finally {
			restoreEnv();
		}
	});
});
