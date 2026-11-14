import fs from 'node:fs'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

// The in-app Docs page browses the repository's own `docs/` directory at
// runtime (docsify-style): _sidebar.md drives the navigation and each
// `<slug>.md` is fetched on demand. This plugin makes that directory
// available to the Vite dev server and copies it into the build output so
// nginx serves it in production — no per-page code changes required to add
// a new doc.
//
//   dev:   GET /docs/<file>      → served from <repo-root>/docs/
//   build: dist/docs/            ← recursive copy of <repo-root>/docs/
function repoDocsPlugin(): Plugin {
  const docsDir = path.resolve(process.cwd(), '../docs')

  return {
    name: 'ainsel-repo-docs',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const url = req.url || ''
        // Match /docs/... with or without the build-time base placeholder.
        const m = url.match(/^\/(?:__BASE_PATH__\/)?docs\/(.+)$/)
        if (!m) return next()
        const rel = decodeURIComponent(m[1].split('?')[0])
        const filePath = path.join(docsDir, rel)
        if (!filePath.startsWith(docsDir)) return next()
        fs.readFile(filePath, (err, data) => {
          if (err) return next()
          res.setHeader('Content-Type', 'text/markdown; charset=utf-8')
          res.end(data)
        })
      })
    },
    closeBundle() {
      const distDocs = path.resolve(process.cwd(), 'dist/docs')
      if (fs.existsSync(docsDir)) {
        fs.cpSync(docsDir, distDocs, { recursive: true })
      }
    },
  }
}

// Absolute `/__BASE_PATH__/...` URLs in `index.html` (the runtime-config
// script) and in the plugin import map (public/plugin-shared/*) are build-time
// placeholders. In dev `base` is `/`, so those paths are not real static files
// and would fall through to the SPA fallback — arriving as HTML parsed as JS.
// This middleware strips the placeholder and serves the file from `public/`, so
// dev resolves the same assets it would under the placeholder base, and both
// `/runtime-config.js` and `/__BASE_PATH__/runtime-config.js` work.
//
//   dev:   GET /__BASE_PATH__/<file> → public/<file>
function devPlaceholderPublicPlugin(): Plugin {
  const publicDir = path.resolve(process.cwd(), 'public')

  return {
    name: 'ainsel-dev-placeholder-public',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const url = (req.url || '').split('?')[0]
        const m = url.match(/^\/__BASE_PATH__\/(.+)$/)
        if (!m) return next()
        const filePath = path.join(publicDir, decodeURIComponent(m[1]))
        if (!filePath.startsWith(publicDir)) return next()
        fs.readFile(filePath, (err, data) => {
          if (err) return next()
          const type = filePath.endsWith('.js')
            ? 'text/javascript; charset=utf-8'
            : 'application/octet-stream'
          res.setHeader('Content-Type', type)
          res.end(data)
        })
      })
    },
  }
}

// Base path is a placeholder at build time and gets rewritten at container
// startup by /docker-entrypoint.d/15-inject-base-path.sh based on the
// BASE_PATH env var. This way a single built image is deployable at any
// subpath (or the root) without rebuilding.
//
// The literal `/__BASE_PATH__/` appears in:
// - asset URLs in dist/index.html
// - the Vite runtime const exposed to app code as import.meta.env.BASE_URL
// - any dynamic imports
//
// At runtime the placeholder is sed-replaced everywhere with the real prefix
// (e.g. `/ainsel-dev/`) or with `/` when no BASE_PATH is set.
//
// The placeholder belongs to the build output only. In dev there is no
// entrypoint to substitute it, so applying it there would make the dev server
// serve the app under `/__BASE_PATH__/` and derive every placeholder-based URL
// from it — including the API base in `src/api/client.ts`
// (`${import.meta.env.BASE_URL}api/v1`), which then never matches the `/api`
// proxy key below and falls through to the SPA fallback. Dev therefore uses
// `base: '/'`, which mirrors the production topology: the SPA is served at
// root, `/api/*` is proxied, and any mount prefix is stripped upstream (by the
// Ingress in cluster, by the proxy key here in dev). Build output is
// unaffected.
export default defineConfig(({ command }) => ({
  base: command === 'build' ? '/__BASE_PATH__/' : '/',
  plugins: [react(), repoDocsPlugin(), devPlaceholderPublicPlugin()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_TARGET ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
}))
