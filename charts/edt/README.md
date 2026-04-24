# edt Helm chart

Deploys the [event-driven-tests-ai](https://github.com/sderosiaux/event-driven-tests-ai) control plane and one or more watch-mode workers.

## Install

```bash
helm upgrade --install edt ./charts/edt \
  --namespace edt --create-namespace \
  --set controlPlane.adminToken=$(openssl rand -hex 32) \
  --set controlPlane.dbURL='postgres://edt:pass@postgres/edt?sslmode=disable'
```

Port-forward to test:

```bash
kubectl -n edt port-forward svc/edt-controlplane 8080:8080
curl http://localhost:8080/healthz
```

## What gets deployed

| Resource          | Purpose                                              |
|-------------------|------------------------------------------------------|
| `ServiceAccount`  | Identity shared by CP and workers                    |
| `Secret`          | Admin bearer token (only when `controlPlane.adminToken` is set) |
| `Deployment`      | Control plane (API + UI + MCP + `/metrics`)          |
| `Service`         | ClusterIP fronting the CP on port 8080               |
| `Deployment`      | Workers (default: 2 replicas)                        |
| `ServiceMonitor`  | Prometheus scrape config (when `serviceMonitor.enabled=true`) |

Workers register with the CP over its Service DNS name, heartbeat every 10s, and run whatever scenarios the CP has assigned them.

## Bring your own Postgres

The chart does not ship a Postgres. Point `controlPlane.dbURL` at any reachable instance (Bitnami chart, CloudNativePG, RDS, Cloud SQL, etc.). When `dbURL` is empty the CP runs with an in-memory store, which is fine for demo but never for production.

## Values

See [`values.yaml`](values.yaml) for the full list. Commonly overridden:

| Key                            | Default                                             | Why                                      |
|--------------------------------|-----------------------------------------------------|------------------------------------------|
| `image.tag`                    | `{{ .Chart.AppVersion }}`                           | Pin to a specific release                |
| `controlPlane.adminToken`      | `""`                                                | Enable bearer auth                       |
| `controlPlane.dbURL`           | `""` (in-memory)                                    | Point at Postgres                        |
| `worker.replicaCount`          | `2`                                                 | Horizontal scaling                       |
| `worker.labels`                | `{env: default}`                                    | Scenario-to-worker affinity              |
| `worker.extraEnv`              | `[]`                                                | Inject `ANTHROPIC_API_KEY`, AWS creds… |
| `serviceMonitor.enabled`       | `false`                                             | Prom Operator integration                |

## Running eval scenarios

Workers need `ANTHROPIC_API_KEY` when assigned scenarios with LLM-as-judge evals. Stage it as a Secret and inject via `worker.extraEnv`:

```yaml
worker:
  extraEnv:
    - name: ANTHROPIC_API_KEY
      valueFrom:
        secretKeyRef:
          name: edt-anthropic
          key: api-key
```

## Upgrades

```bash
helm upgrade edt ./charts/edt --reuse-values --set image.tag=0.2.0
```

The chart uses rolling updates with `readinessProbe` on `/healthz`, so there is no downtime for stateless upgrades. Schema migrations on the CP's Postgres run at startup.
