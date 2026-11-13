// check.mjs verifies the agent's ChatGPT sign-in before a task runs, refreshing
// the access token if it is about to expire (Pi writes the refreshed credential
// back to the volume). Exit codes:
//   0   signed in, token usable
//   78  sign-in required: nothing stored, or OpenAI rejected the refresh
//   1   could not tell (network, OpenAI unavailable) after retries
// The token is never printed.
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { AUTH_PATH, EXIT_SIGN_IN_REQUIRED, classify, describe, loadPi } from "./lib.mjs";

// Waits between the three attempts: a blip should not cost a sign-in.
const RETRY_WAITS_MS = [2000, 5000];

export async function runCheck({ ModelRuntime, authPath = AUTH_PATH, log = console.error, sleep = delay }) {
	const runtime = await ModelRuntime.create({ authPath, allowModelNetwork: false });
	for (let attempt = 0; ; attempt++) {
		try {
			const auth = await runtime.getAuth("openai");
			if (!auth) {
				log("chatgpt-auth: no ChatGPT sign-in stored; sign in from the agent page");
				return EXIT_SIGN_IN_REQUIRED;
			}
			log("chatgpt-auth: ChatGPT sign-in ok");
			return 0;
		} catch (error) {
			if (classify(error) === "signin-required") {
				log(`chatgpt-auth: ChatGPT sign-in no longer valid: ${describe(error)}`);
				return EXIT_SIGN_IN_REQUIRED;
			}
			if (attempt >= RETRY_WAITS_MS.length) {
				log(`chatgpt-auth: could not check the ChatGPT sign-in: ${describe(error)}`);
				return 1;
			}
			log(`chatgpt-auth: check failed, retrying: ${describe(error)}`);
			await sleep(RETRY_WAITS_MS[attempt]);
		}
	}
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
	const { ModelRuntime } = await loadPi();
	process.exit(await runCheck({ ModelRuntime }));
}
