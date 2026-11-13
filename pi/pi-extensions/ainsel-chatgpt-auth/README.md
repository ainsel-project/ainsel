# ainsel-chatgpt-auth

Scripts for agents that authenticate to OpenAI with a ChatGPT subscription
(`spec.openAI.authMethod: ChatGPTSignIn`). This folder is **not** a pi
extension: it has no `index.ts`, so the entrypoint does not load it.

| Script | Run by | Does |
|---|---|---|
| `check.mjs` | `entrypoint.sh`, before pi starts | Reads the sign-in from the credential volume and refreshes the access token if it is about to expire. Exits `0` when usable, `78` when a new sign-in is needed, `1` when it could not tell (after retries). |

The credential lives on the agent's own volume at
`/home/agent/.pi/credentials/auth.json`, in Pi's format; the operator creates
the volume and links Pi's `auth.json` to it. The scripts use only Pi's public
`ModelRuntime` API, imported from the global npm root (`AINSEL_PI_ROOT`), and
are checked against the `PI_VERSION` pinned in `pi/Dockerfile`.

Exit `78` is a contract with the operator: the task Job fails without a retry,
the agent gets `SignInRequired=True`, and the queued task waits for a new
sign-in. Tokens are never printed.

```sh
npm test
```
