# Frontend

React + Vite + TypeScript operations console for the AInsel platform.

Built with React 18, React Router 6, TanStack Query 5, and React Hook
Form + Zod. The console lists and edits `Agent`, `Trigger`, and
`WebhookConnector` resources, streams agent invocations, and surfaces
the hub's observability endpoints.

## Role in the platform

See [`../docs/architecture.md`](../docs/architecture.md).

Talks to [`../services/hub/`](../services/hub/) over its REST API
(`/api/v1/*`) for all data access. The Vite dev server proxies `/api`
to the configured hub URL; in-cluster the `Ingress` serves the SPA at
`/ainsel` and the API at `/ainsel/api`.

## Local development

```bash
pnpm install                  # from repo root (workspace install)
pnpm --filter frontend dev    # Vite dev server on :5173
```

Pointing at a local hub backend:

```bash
# Proxy /api requests through Vite to a local hub
VITE_API_TARGET=http://localhost:8080 pnpm --filter frontend dev

# Or override the API base URL the SPA fetches directly
VITE_API_URL=http://localhost:8080/api/v1 pnpm --filter frontend dev
```

`VITE_API_URL` defaults to `/api/v1` (served through the Vite proxy in
dev, through the `Ingress` rewrite in cluster). `VITE_API_TARGET`
controls the dev-only proxy target (default `http://localhost:8080`).

## Testing

```bash
pnpm --filter frontend test         # vitest, with coverage thresholds
pnpm --filter frontend test:guard   # vitest + the environment-reuse assertion
pnpm --filter frontend lint         # eslint, zero warnings
```

### Pool

The suite runs on `pool: 'vmThreads'`: one jsdom per worker thread instead of one
per test file. Under the default `forks` pool every file got its own process and
rebuilt the whole DOM (~600 ms each), which vitest reported as ~50 % of tracked
time — 134 files, ~19 s of wall clock, roughly half of it building the room.

Files keep their own module registry, so module-level mutable state (`authToken`
in `src/api/client.ts`, `diagramSeq` in `ChannelDetailPage.tsx`) stays per-file.
`isolate: false` is the other way to share an environment and is deliberately not
used: it shares one module registry across files, which would turn that state
into cross-file pollution — a silent wrong answer instead of a visible flake.

Two specs stay pinned to `forks` in `vitest.config.ts > projects`:
`src/api/client.test.ts` and `src/pages/login/Login.test.tsx`. Both replace
`window.location` wholesale, and under `vmThreads` the environment is jsdom's own
VM context, where `location` is a non-configurable accessor — `Object.defineProperty`
and `vi.stubGlobal` both throw before the first assertion. Under `forks` vitest
copies the window keys onto the worker global as configurable accessors, which is
what makes the swap legal there. Cost: two fork workers.

`test:guard` (`scripts/check-test-env.mjs`) fails when the environment starts
being rebuilt per file again — vitest's own hint about it is reporter output, not
an exit code. See issue #23.

## Build

```bash
pnpm --filter frontend build        # tsc -b && vite build
```

Output goes to `frontend/dist/`. Production serving is done by the
nginx image in `Dockerfile`, which substitutes runtime config from
`docker-entrypoint-resolver.envsh` into `nginx.conf.template`.

## Deep-linkable URL state

List pages keep their state in the query string so filtered views survive a
refresh and can be shared:

| Route | Parameters |
| --- | --- |
| `/activity` | `q`, `page`, `pageSize`, `status`, `outcome`, `connector`, `agent` |
| `/observability` | `range`, `agent`, `level` |
| `/observability/errors` | `range`, `severity`, `source` |

Unknown enum values in the URL (e.g. `?status=bogus`) fall back to "any".

## Reference

- [Hub REST API](../docs/api-reference.md)
- [Architecture overview](../docs/architecture.md)
