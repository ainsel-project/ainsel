# Event-Gateway Operator

Kubernetes operator for the `WebhookConnector` custom resource. Built with
Kubebuilder.

## Role in the platform

See [`docs/architecture.md`](../../docs/architecture.md).

For each `WebhookConnector` the operator creates a `Deployment` running
[`services/webhook-receiver/`](../../services/webhook-receiver/) and a matching
`Service`. It also registers the webhook with the configured Forgejo
instance via the Forgejo admin API and manages its lifecycle on update
and delete.

The operator also reconciles the `Plugin` custom resource: it fetches each
plugin's manifest from its in-cluster Service at the well-known path
(`/ainsel-plugin.json`), validates it (schema, identity, plugin API
compatibility, permission approvals), and records the result — including the
last good manifest — on the Plugin's status. It does not deploy plugin
workloads; it only observes and reports.

## Local development

```bash
make install         # install CRDs into the cluster
make run             # run the controller against the active kubeconfig
```

After CRD edits, regenerate code and manifests:

```bash
make generate manifests
```

## Testing

```bash
make test            # envtest-based controller tests + unit tests
```

## Reference

- [WebhookConnector CRD spec](../../docs/crd-reference.md)
