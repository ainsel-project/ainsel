# Ainsel Chart Architecture

## Overview

The ainsel-chart is a Helm chart that deploys all Ainsel components into a single Kubernetes namespace. It creates the CRDs, operators, hub, UI, PostgreSQL, and optional bootstrap resources.

## Component names

Each component has exactly one name, used for its template directory, its
Kubernetes resources (`Deployment`, `Service`, `ServiceAccount`, `Ingress`),
its `app.kubernetes.io/component` label, and its top-level values key. The
name matches the component's directory in the code tree:

| Component | Code | Chart templates | Resource name | Values key |
|---|---|---|---|---|
| Agent operator | `operators/agent` | `templates/agent-operator/` | `agent-operator` | `agentOperator` |
| Event gateway operator | `operators/event-gateway` | `templates/event-gateway-operator/` | `event-gateway-operator` | `eventGatewayOperator` |
| Hub | `services/hub` | `templates/hub/` | `hub` | `hub` |
| Console | `frontend/` | `templates/frontend/` | `frontend` | `frontend` |

Deliberate exceptions: the MCP service stays `ainsel-mcp`, Postgres stays
`postgres`, and container image repositories keep their historical names
(e.g. `dpinsel/ainsel-hub-backend`). The operators label the resources they
create with `app.kubernetes.io/managed-by: agent-operator` /
`connector-operator`; those values are set in operator code and are not
chart resource names.

## Deployment Topology

```mermaid
graph TD
    subgraph "Kubernetes Cluster"
        subgraph "ainsel namespace"
            AO[ainsel-agent-operator<br/>1 replica]
            CO[ainsel-event-gateway-operator<br/>1 replica]
            HUB[ainsel-hub<br/>1 replica]
            UI[ainsel-frontend<br/>1 replica]
            PG[(postgres<br/>StatefulSet<br/>event queue + stores)]
        end

        subgraph "Ingress"
            ING[nginx Ingress Controller]
        end
    end

    ING -->|/ainsel/api| HUB
    ING -->|/ainsel| UI
    HUB -->|events, tasks, triggers, logs| PG
    AO -.->|watches Agent, AgentImage| CRDs[(ainsel.dev CRDs)]
    CO -.->|watches WebhookConnector| CRDs
```

## Template Structure

```mermaid
graph LR
    subgraph templates/
        NS[namespace.yaml]
        subgraph agent-operator/
            AO_D[deployment.yaml]
            AO_R[role.yaml]
            AO_RB[rolebinding.yaml]
            AO_SA[serviceaccount.yaml]
        end
        subgraph event-gateway-operator/
            CO_D[deployment.yaml]
            CO_R[role.yaml]
            CO_RB[rolebinding.yaml]
            CO_SA[serviceaccount.yaml]
        end
        subgraph hub/
            H_D[deployment.yaml]
            H_S[service.yaml]
            H_I[ingress.yaml]
            H_R[role.yaml]
            H_RB[rolebinding.yaml]
            H_SA[serviceaccount.yaml]
        end
        subgraph ui/
            U_D[deployment.yaml]
            U_S[service.yaml]
        end
    end

    subgraph crds/
        CRD_A[agent.yaml]
        CRD_AI[agentimage.yaml]
        CRD_F[webhookconnector.yaml]
    end
```

## CRDs

The chart includes three CRDs installed in the `crds/` directory (applied before templates):

| CRD | File | API Group |
|-----|------|-----------|
| Agent | `crds/agent.yaml` | `ainsel.dev/v1alpha1` |
| AgentImage | `crds/agentimage.yaml` | `ainsel.dev/v1alpha1` |
| WebhookConnector | `crds/webhookconnector.yaml` | `ainsel.dev/v1alpha1` |
