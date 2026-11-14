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
// A hub that restarts must not cost the user the sign-in: a call is retried
// for about a minute (HUB_ATTEMPTS × HUB_RETRY_MS) while the connection is
// refused, and no call hangs past HUB_TIMEOUT_MS.
const HUB_ATTEMPTS = 20;
const HUB_RETRY_MS = 3000;
const HUB_TIMEOUT_MS = 10_000;

// Errors that prove the request never reached the hub, so repeating it
// cannot repeat its effect.
const NOT_SENT = new Set(["ECONNREFUSED", "ENOTFOUND", "EAI_AGAIN", "EHOSTUNREACH", "ENETUNREACH"]);

function notSent(error) {
	for (let e = error; e; e = e.cause) if (NOT_SENT.has(e.code)) return true;
	return false;
}

// SessionClosed: the hub ended the session (expired or cancelled).
class SessionClosed extends Error {}

// hubClient talks to the hub's internal sign-in routes for one session.
// podToken is this session's own token (AINSEL_SIGN_IN_TOKEN): only the
// sign-in pod has it, so the hub can tell it from other agent pods.
export function hubClient({ hubUrl, token, podToken, agent, session, sleep = delay, fetch = globalThis.fetch }) {
	const base = `${hubUrl.replace(/\/$/, "")}/api/internal/agents/${encodeURIComponent(agent)}/chatgpt-sign-in/${encodeURIComponent(session)}`;
	const headers = { "X-Internal-Token": token, "X-Sign-In-Token": podToken, "content-type": "application/json" };
	// call retries a request that never reached the hub. The auth-url post
	// and the redirect poll change the session (the pasted address is handed
	// out once), so a timeout or 5xx, which may come after the hub acted, is
	// returned or thrown, not repeated. Only a repeatable call (the result)
	// also retries those.
	async function call(method, verb, body, { repeatable = false, signal } = {}) {
		for (let attempt = 1; ; attempt++) {
			const timeout = AbortSignal.timeout(HUB_TIMEOUT_MS);
			let res, error;
			try {
				res = await fetch(`${base}/${verb}`, {
					method,
					headers,
					body: body ? JSON.stringify(body) : undefined,
					signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
				});
			} catch (e) {
				error = e;
			}
			if (res?.status === 410) throw new SessionClosed("the sign-in session ended (expired or cancelled)");
			const retry = res ? repeatable && res.status >= 500 : repeatable || notSent(error);
			if (!retry || attempt >= HUB_ATTEMPTS) {
				if (res) return res;
				throw new Error(`hub unreachable: ${error instanceof Error ? error.message : error}`, { cause: error });
			}
			await sleep(HUB_RETRY_MS, undefined, signal ? { signal } : undefined);
		}
	}
	return {
		async postAuthURL(url) {
			const res = await call("POST", "auth-url", { url });
			if (!res.ok) throw new Error(`hub refused the authorization URL (${res.status})`);
		},
		// takeRedirect returns the pasted address, or "" while none is pasted.
		async takeRedirect(signal) {
			const res = await call("GET", "redirect", undefined, { signal });
			if (res.status === 204) return "";
			if (!res.ok) throw new Error(`hub redirect poll failed (${res.status})`);
			return (await res.json()).redirectUrl ?? "";
		},
		async postResult(ok, error) {
			await call("POST", "result", ok ? { ok: true } : { ok: false, error }, { repeatable: true });
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

export async function runSignIn({ ModelRuntime, hub, agentName = "", credentialsDir = CREDENTIALS_DIR, log = console.error, sleep = delay }) {
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
					const url = await hub.takeRedirect(signal);
					if (url) return url;
					await sleep(POLL_MS, undefined, { signal });
				}
			},
		};
		try {
			// The name OpenAI shows for this sign-in in the user's ChatGPT
			// settings, so each agent can be recognised and revoked there.
			const name = agentName ? `AInsel ${agentName}` : "AInsel";
			await runtime.login("openai", "oauth", interaction, { getDeviceId: () => id, agentName: name });
		} catch (error) {
			throw failure ?? error;
		}
		log("chatgpt-signin: signed in; credential stored on the agent's volume");
		// The credential is on the volume now; that is the sign-in. Telling the
		// console is best effort, and the operator reads success from the exit
		// code.
		await hub.postResult(true).catch((error) => {
			log(`chatgpt-signin: could not report the result to the hub: ${error instanceof Error ? error.message : error}`);
		});
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
		podToken: process.env.AINSEL_SIGN_IN_TOKEN ?? "",
		agent: process.env.AGENT_NAME ?? "",
		session: process.env.AINSEL_SIGN_IN_SESSION ?? "",
	});
	process.exit(await runSignIn({ ModelRuntime, hub, agentName: process.env.AGENT_NAME ?? "" }));
}
