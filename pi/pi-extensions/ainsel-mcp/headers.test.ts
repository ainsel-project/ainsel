import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { pluginHeaders } from "./headers.ts";

describe("pluginHeaders", () => {
	it("returns agent + invocation headers for a hub plugin agent-mcp URL", () => {
		const h = pluginHeaders("http://hub:8080/api/internal/plugins/mem0/agent-mcp", {
			AGENT_NAME: "reviewer",
			AINSEL_INVOCATION_ID: "inv-1",
		});
		assert.deepStrictEqual(h, {
			"X-Ainsel-Agent": "reviewer",
			"X-Ainsel-Invocation-Id": "inv-1",
		});
	});

	it("omits the invocation header when the invocation id is not set", () => {
		const h = pluginHeaders("http://hub:8080/api/internal/plugins/mem0/agent-mcp", {
			AGENT_NAME: "reviewer",
		});
		assert.deepStrictEqual(h, { "X-Ainsel-Agent": "reviewer" });
	});

	it("omits empty agent and invocation values", () => {
		const h = pluginHeaders("http://hub:8080/api/internal/plugins/mem0/agent-mcp", {
			AGENT_NAME: "",
			AINSEL_INVOCATION_ID: "",
		});
		assert.deepStrictEqual(h, {});
	});

	it("returns no headers for a non-plugin MCP URL", () => {
		const h = pluginHeaders("https://mcp.example.com/mcp", {
			AGENT_NAME: "reviewer",
			AINSEL_INVOCATION_ID: "inv-1",
		});
		assert.deepStrictEqual(h, {});
	});

	it("returns no headers when the plugin prefix is not at the path start", () => {
		const h = pluginHeaders("http://hub:8080/x/api/internal/plugins/mem0/agent-mcp", {
			AGENT_NAME: "reviewer",
			AINSEL_INVOCATION_ID: "inv-1",
		});
		assert.deepStrictEqual(h, {});
	});

	it("returns no headers for an unparseable URL", () => {
		const h = pluginHeaders("not a url", { AGENT_NAME: "reviewer" });
		assert.deepStrictEqual(h, {});
	});
});
