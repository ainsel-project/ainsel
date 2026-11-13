// Shared helpers for the ChatGPT sign-in scripts. These scripts use only Pi's
// public API (ModelRuntime from @earendil-works/pi-coding-agent), never its
// internal modules, so a deliberate PI_VERSION bump keeps them working.
import { join } from "node:path";
import { pathToFileURL } from "node:url";

// EX_CONFIG: the stored sign-in is missing or definitely dead. The operator
// turns this exit code into SignInRequired instead of retrying the task.
export const EXIT_SIGN_IN_REQUIRED = 78;

// Where the operator mounts the agent's credential volume.
export const CREDENTIALS_DIR = "/home/agent/.pi/credentials";
export const AUTH_PATH = join(CREDENTIALS_DIR, "auth.json");

// loadPi imports pi-coding-agent from the global npm root. The image installs
// pi globally, and ESM resolution ignores NODE_PATH, so the entrypoint passes
// `npm root -g` in AINSEL_PI_ROOT.
export async function loadPi(root = process.env.AINSEL_PI_ROOT) {
	if (!root) throw new Error("AINSEL_PI_ROOT is not set (expected the output of `npm root -g`)");
	return import(pathToFileURL(join(root, "@earendil-works/pi-coding-agent", "dist", "index.js")).href);
}

// Messages pi-ai uses when the sign-in itself is unusable, as opposed to the
// network or OpenAI being briefly unavailable. A 400/401 from the token
// endpoint is a rejected refresh token (invalid_grant and friends).
const SIGN_IN_REQUIRED = [
	/OAuth token request failed \((400|401)\)/,
	/does not contain an issued client ID/,
	/invalid_grant/,
];

// classify reads the whole cause chain: pi-ai wraps the token-endpoint error
// in two ModelsErrors whose own messages say nothing about why.
export function classify(error) {
	for (let e = error; e; e = e.cause) {
		const message = e instanceof Error ? e.message : String(e);
		if (SIGN_IN_REQUIRED.some((re) => re.test(message))) return "signin-required";
	}
	return "transient";
}

// describe summarises an error for the log. Token-endpoint errors carry the
// response body, which holds error codes, never tokens.
export function describe(error) {
	const parts = [];
	for (let e = error; e; e = e.cause) {
		const message = e instanceof Error ? e.message : String(e);
		if (parts.at(-1) !== message) parts.push(message);
	}
	return parts.join(": ");
}
