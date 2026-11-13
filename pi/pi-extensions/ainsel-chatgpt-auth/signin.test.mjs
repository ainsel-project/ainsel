import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtemp, readFile, writeFile, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { hubClient, runSignIn } from "./signin.mjs";

const AUTH_URL = "https://auth.openai.com/api/accounts/authorize?client_id=dynamic_agent_client";
const PASTED = "http://127.0.0.1:1455/auth/callback?code=c&state=s";

// stubHub answers the sign-in pod's three internal calls. redirectAfter is
// how many polls return 204 before the pasted URL appears; gone makes every
// redirect poll answer 410.
async function stubHub({ redirectAfter = 1, gone = false } = {}) {
	const seen = { authUrl: null, result: null, polls: 0, tokens: [] };
	const server = createServer(async (req, res) => {
		seen.tokens.push(req.headers["x-internal-token"]);
		let body = "";
		for await (const chunk of req) body += chunk;
		const base = "/api/internal/agents/worker/chatgpt-sign-in/S1/";
		if (req.method === "POST" && req.url === base + "auth-url") {
			seen.authUrl = JSON.parse(body).url;
			res.writeHead(204).end();
		} else if (req.method === "GET" && req.url === base + "redirect") {
			seen.polls++;
			if (gone) return res.writeHead(410).end();
			if (seen.polls <= redirectAfter) return res.writeHead(204).end();
			res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ redirectUrl: PASTED }));
		} else if (req.method === "POST" && req.url === base + "result") {
			seen.result = JSON.parse(body);
			res.writeHead(204).end();
		} else {
			res.writeHead(404).end();
		}
	});
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const url = `http://127.0.0.1:${server.address().port}`;
	return { url, seen, close: () => new Promise((r) => { server.close(r); server.closeAllConnections(); }) };
}

// A ModelRuntime stand-in whose login plays pi-ai's official ChatGPT flow:
// notify the auth URL, prompt for the manual code, then return a credential.
function stubRuntime(behaviour = {}) {
	const calls = { create: [], login: [] };
	return {
		calls,
		ModelRuntime: {
			async create(options) {
				calls.create.push(options);
				return {
					async login(provider, type, interaction, options) {
						calls.login.push({ provider, type, deviceId: options.getDeviceId(), agentName: options.agentName });
						if (behaviour.fail) throw new Error(behaviour.fail);
						interaction.notify({ type: "auth_url", url: AUTH_URL });
						const pasted = await interaction.prompt({ type: "manual_code", message: "paste", signal: new AbortController().signal });
						calls.pasted = pasted;
						return { type: "oauth", access: "sk-secret-access", refresh: "rt-secret" };
					},
				};
			},
		},
	};
}

async function setup(hubOpts, runtimeOpts) {
	const hub = await stubHub(hubOpts);
	const dir = await mkdtemp(join(tmpdir(), "signin-"));
	const rt = stubRuntime(runtimeOpts);
	const lines = [];
	const opts = {
		ModelRuntime: rt.ModelRuntime,
		hub: hubClient({ hubUrl: hub.url, token: "tok", agent: "worker", session: "S1" }),
		credentialsDir: dir,
		log: (l) => lines.push(l),
		sleep: async () => {},
	};
	return { hub, dir, rt, lines, opts };
}

test("posts the auth url and returns the pasted redirect", async () => {
	const { hub, rt, opts } = await setup({ redirectAfter: 2 });
	try {
		assert.equal(await runSignIn(opts), 0);
		assert.equal(hub.seen.authUrl, AUTH_URL);
		assert.equal(rt.calls.pasted, PASTED);
		assert.equal(hub.seen.polls, 3);
		assert.deepEqual(hub.seen.result, { ok: true });
		assert.equal(rt.calls.login[0].provider, "openai");
		assert.equal(rt.calls.login[0].type, "oauth");
		assert.equal(rt.calls.create[0].authPath, join(opts.credentialsDir, "auth.json"));
		assert.ok(hub.seen.tokens.every((t) => t === "tok"), "every hub call carries the internal token");
	} finally {
		await hub.close();
	}
});

test("creates and then reuses device-id", async () => {
	const { hub, dir, rt, opts } = await setup();
	try {
		await runSignIn(opts);
		const id = (await readFile(join(dir, "device-id"), "utf8")).trim();
		assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
		assert.equal(rt.calls.login[0].deviceId, id);
		assert.equal((await stat(join(dir, "device-id"))).mode & 0o777, 0o600);
	} finally {
		await hub.close();
	}
	const again = await setup();
	try {
		again.opts.credentialsDir = dir;
		await runSignIn(again.opts);
		assert.equal(again.rt.calls.login[0].deviceId, (await readFile(join(dir, "device-id"), "utf8")).trim());
	} finally {
		await again.hub.close();
	}
});

test("replaces a device-id file that is not a UUID", async () => {
	const { hub, dir, rt, opts } = await setup();
	try {
		await writeFile(join(dir, "device-id"), "garbage\n");
		await runSignIn(opts);
		assert.match(rt.calls.login[0].deviceId, /^[0-9a-f-]{36}$/);
	} finally {
		await hub.close();
	}
});

test("reports failure text without tokens", async () => {
	const { hub, lines, opts } = await setup({}, { fail: "OAuth state mismatch" });
	try {
		assert.equal(await runSignIn(opts), 1);
		assert.deepEqual(hub.seen.result, { ok: false, error: "OAuth state mismatch" });
		for (const l of lines) assert.ok(!l.includes("sk-secret") && !l.includes("rt-secret"), `leaked in: ${l}`);
	} finally {
		await hub.close();
	}
});

test("never logs or posts the credential on success", async () => {
	const { hub, lines, opts } = await setup();
	try {
		await runSignIn(opts);
		const all = lines.join("\n") + JSON.stringify(hub.seen);
		assert.ok(!all.includes("sk-secret") && !all.includes("rt-secret"));
	} finally {
		await hub.close();
	}
});

test("stops on 410", async () => {
	const { hub, opts } = await setup({ gone: true });
	try {
		assert.equal(await runSignIn(opts), 1);
		assert.equal(hub.seen.polls, 1, "a closed session is not polled again");
	} finally {
		await hub.close();
	}
});

test("refuses any prompt other than the redirect paste", async () => {
	const hub = await stubHub();
	const dir = await mkdtemp(join(tmpdir(), "signin-"));
	try {
		const ModelRuntime = {
			async create() {
				return {
					async login(_p, _t, interaction) {
						return interaction.prompt({ type: "select", message: "method", options: [{ id: "x", label: "x" }] });
					},
				};
			},
		};
		const code = await runSignIn({
			ModelRuntime,
			hub: hubClient({ hubUrl: hub.url, token: "tok", agent: "worker", session: "S1" }),
			credentialsDir: dir,
			log: () => {},
			sleep: async () => {},
		});
		assert.equal(code, 1);
		assert.equal(hub.seen.result.ok, false);
	} finally {
		await hub.close();
	}
});

test("introduces itself to OpenAI as AInsel and the agent", async () => {
	const { hub, rt, opts } = await setup();
	try {
		await runSignIn({ ...opts, agentName: "worker" });
		assert.equal(rt.calls.login[0].agentName, "AInsel worker");
	} finally {
		await hub.close();
	}
});
