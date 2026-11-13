// signin.mjs drives OpenAI's official "Sign in with ChatGPT" for one agent,
// inside the sign-in Job the operator runs on the agent's credential volume.
//
//   1. Pi starts the flow (PKCE, dynamic client registration) and hands us the
//      authorization URL; we post it to the hub, which shows it in the console.
//   2. The user signs in at OpenAI. The browser then lands on a 127.0.0.1 page
//      that does not load; the user pastes that address into the console.
//   3. We fetch the pasted address from the hub and give it to Pi, which
//      exchanges the code (the PKCE verifier never leaves this process) and
//      writes the credential to auth.json on the volume.
//
// Only the authorization URL and the pasted address cross the hub; the
// credential is never logged or sent anywhere. Exit 0 on success, 1 otherwise.
import { randomUUID } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { CREDENTIALS_DIR, loadPi } from "./lib.mjs";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const POLL_MS = 2000;

// SessionClosed: the hub ended the session (expired or cancelled).
class SessionClosed extends Error {}

// hubClient talks to the hub's internal sign-in routes for one session.
export function hubClient({ hubUrl, token, agent, session }) {
	const base = `${hubUrl.replace(/\/$/, "")}/api/internal/agents/${encodeURIComponent(agent)}/chatgpt-sign-in/${encodeURIComponent(session)}`;
	const headers = { "X-Internal-Token": token, "content-type": "application/json" };
	async function call(method, verb, body) {
		const res = await fetch(`${base}/${verb}`, { method, headers, body: body ? JSON.stringify(body) : undefined });
		if (res.status === 410) throw new SessionClosed("the sign-in session ended (expired or cancelled)");
		return res;
	}
	return {
		async postAuthURL(url) {
			const res = await call("POST", "auth-url", { url });
			if (!res.ok) throw new Error(`hub refused the authorization URL (${res.status})`);
		},
		// takeRedirect returns the pasted address, or "" while none is pasted.
		async takeRedirect() {
			const res = await call("GET", "redirect");
			if (res.status === 204) return "";
			if (!res.ok) throw new Error(`hub redirect poll failed (${res.status})`);
			return (await res.json()).redirectUrl ?? "";
		},
		async postResult(ok, error) {
			await call("POST", "result", ok ? { ok: true } : { ok: false, error });
		},
	};
}

// deviceId returns this agent's installation id, creating it on first use.
// It identifies the agent to OpenAI ("agent host") and persists on the
// volume, so a sign-in again appears as the same app in ChatGPT's settings.
async function deviceId(dir) {
	const path = join(dir, "device-id");
	const existing = await readFile(path, "utf8").then((s) => s.trim(), () => "");
	if (UUID.test(existing)) return existing;
	const id = randomUUID();
	await writeFile(path, `${id}\n`, { mode: 0o600 });
	return id;
}

export async function runSignIn({ ModelRuntime, hub, credentialsDir = CREDENTIALS_DIR, log = console.error, sleep = delay }) {
	try {
		const id = await deviceId(credentialsDir);
		const runtime = await ModelRuntime.create({
			authPath: join(credentialsDir, "auth.json"),
			modelsPath: null,
			allowModelNetwork: false,
		});
		const abort = new AbortController();
		let failure;
		const interaction = {
			signal: abort.signal,
			notify(event) {
				if (event.type !== "auth_url") return;
				hub.postAuthURL(event.url).then(
					() => log("chatgpt-signin: authorization link sent to the console"),
					(error) => {
						failure = error;
						abort.abort();
					},
				);
			},
			async prompt(prompt) {
				// The official flow asks for exactly one thing: the redirect
				// address. Anything else would be a flow we did not review.
				if (prompt.type !== "manual_code") throw new Error(`unexpected sign-in prompt: ${prompt.type}`);
				const signal = prompt.signal ? AbortSignal.any([abort.signal, prompt.signal]) : abort.signal;
				for (;;) {
					signal.throwIfAborted();
					const url = await hub.takeRedirect();
					if (url) return url;
					await sleep(POLL_MS, undefined, { signal });
				}
			},
		};
		try {
			await runtime.login("openai", "oauth", interaction, { getDeviceId: () => id });
		} catch (error) {
			throw failure ?? error;
		}
		log("chatgpt-signin: signed in; credential stored on the agent's volume");
		await hub.postResult(true);
		return 0;
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		log(`chatgpt-signin: sign-in failed: ${message}`);
		if (!(error instanceof SessionClosed)) {
			await hub.postResult(false, message).catch(() => {});
		}
		return 1;
	}
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
	const { ModelRuntime } = await loadPi();
	const hub = hubClient({
		hubUrl: process.env.HUB_URL ?? "",
		token: process.env.HUB_INTERNAL_VALIDATE_SECRET ?? "",
		agent: process.env.AGENT_NAME ?? "",
		session: process.env.AINSEL_SIGN_IN_SESSION ?? "",
	});
	process.exit(await runSignIn({ ModelRuntime, hub }));
}
