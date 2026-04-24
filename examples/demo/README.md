# Demo stack

One command to see `edt` actually do something:

```bash
docker compose -f examples/demo/docker-compose.yaml up --build
```

Three containers come up:

| Service    | What it is                          | Where                  |
|------------|-------------------------------------|------------------------|
| `redpanda` | Kafka-compatible broker (single node) | localhost:19092 (host) |
| `edt-cp`   | Control plane API + UI              | http://localhost:8080  |
| `edt-run`  | One-shot scenario run               | exits 0 on success     |

The scenario at [`scenario.yaml`](scenario.yaml) produces 50 synthetic orders onto the `orders` topic and runs three checks. `edt-run` reports to the control plane, which persists the run and exposes it at:

- `GET http://localhost:8080/api/v1/runs`
- `GET http://localhost:8080/api/v1/scenarios/demo-order-flow/slo`

## Re-run without restart

```bash
docker compose -f examples/demo/docker-compose.yaml run --rm edt-run
```

## Tear down

```bash
docker compose -f examples/demo/docker-compose.yaml down -v
```

## Point a local `edt` at the dockerised broker

The broker is also exposed on `localhost:19092` so you can drive it from a host-installed `edt`:

```bash
edt run --file examples/demo/scenario.yaml \
  --override spec.connectors.kafka.bootstrap_servers=localhost:19092
```

*(the `--override` CLI flag lands with the watch-mode runner; until then, edit the file or template it.)*
