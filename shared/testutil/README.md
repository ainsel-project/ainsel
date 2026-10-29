# Shared Testutil

Go module with the kubebuilder-derived e2e test helpers shared by both
operator test trees ([`operators/agent/`](../../operators/agent/),
[`operators/event-gateway/`](../../operators/event-gateway/)): running
commands against a Kind cluster, installing/uninstalling the Cert Manager
bundle, loading images into Kind, and small output helpers.

Previously these helpers were duplicated byte-for-byte in each operator's
`test/utils/` package; they live here now so a fix is applied once.

## Role in the platform

Test-only module. It intentionally carries a Ginkgo dependency (the helpers
write to `GinkgoWriter`) — keeping it out of [`shared/api/`](../api/) stops
that dependency from leaking into every service's dependency graph.

## Importing from another package in the workspace

Inside the monorepo, `go.work` joins this module automatically. Outside the
workspace, consume it via `require` + a local `replace`:

```go
// go.mod: require github.com/ainsel-project/ainsel/shared/testutil v0.0.0-00010101000000-000000000000
// go.mod: replace github.com/ainsel-project/ainsel/shared/testutil => ../../shared/testutil
import "github.com/ainsel-project/ainsel/shared/testutil"
```

The helpers assume they are called from a project's `test/e2e/` directory
(`GetProjectDir` strips `/test/e2e` from the working directory) — keep the
callers in place.

## Testing

```bash
go vet -tags e2e ./...   # the helpers are compiled by callers under the e2e tag
```
