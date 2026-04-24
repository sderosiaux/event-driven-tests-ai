# event-driven-tests-ai

**Test your Kafka flows like Postman tests your REST APIs. Run the same scenario once in CI, or forever as a synthetic probe in prod.**

[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-pre--alpha-yellow.svg)](#status)

---

Streaming systems have two common testing patterns and neither is great.

One is "test in prod": ship, watch Grafana, refund the angry customers. The other is unit-testing the producer and hoping the consumer still works six deploys later. There's no `curl` for streams. No Postman collection you share with QA. No synthetic probe that tells you the order flow is still intact at 3pm.

With edt, you write one YAML scenario that describes what you expect to happen ("produce these orders, expect a triaged event within 2s, the warehouse API should return 200") and the engine runs it. In CI it exits 0 or 1. On a long-running worker it keeps running forever, and the same scenario becomes an SLO probe that feeds metrics to Grafana, alerts on degradation, and historises every run.

If your system has AI agents in the flow (LLM consumers that classify, decide, produce), `edt eval` scores their outputs against a rubric and a judge model. Agents drift silently. This catches it.

## What you can do with it

- **Pre-prod end-to-end tests.** One scenario covers produce + consume + HTTP callback verification. Exit codes, JSON reports, CI-native.
- **Synthetic monitoring.** `edt worker` runs scenarios forever, pushes snapshots to a control plane, exposes Prometheus metrics. Business SLO in one place.
- **Agent evaluation.** Your LLM agent consumes `orders.new` and produces `orders.triaged`. `edt eval` scores 50 pairs against a rubric and fails if the average drops below threshold.
- **Schema-aware payloads.** Avro and JSON Schema round-trip through Confluent or Apicurio Schema Registry. Your scenarios see typed data, not bytes.
- **AI-native control plane.** The control plane exposes an MCP server — Claude Desktop, Claude Code, Cursor can query scenarios, runs, SLO rates without a custom integration.

## Quickstart (five minutes)

Want to see it working before you install anything?

```bash
git clone https://github.com/sderosiaux/event-driven-tests-ai
cd event-driven-tests-ai
docker compose -f examples/demo/docker-compose.yaml up --build
```

Brings up Redpanda, the control plane on http://localhost:8080, and runs a scenario that produces 20 orders and checks them. Full walkthrough in [`examples/demo/`](examples/demo/).

Install locally and run against any Kafka you already have:

```bash
brew install event-driven-tests-ai/tap/edt  # or: curl -sSL install.edt.io | sh
edt run --file order-flow.yaml --bootstrap-servers localhost:9092
```

A scenario looks like this:

```yaml
apiVersion: edt.io/v1
kind: Scenario
metadata:
  name: order-flow
spec:
  connectors:
    kafka:
      bootstrap_servers: localhost:9092
    http:
      base_url: https://api.staging.example.com

  data:
    orders:
      generator:
        strategy: faker
        seed: 42
        overrides:
          orderId: "${uuid()}"
          amount: "${faker.number.float(10, 500)}"

  steps:
    - name: place-order
      produce:
        topic: orders
        payload: ${data.orders}
        count: 20
        rate: 10/s

    - name: wait-ack
      consume:
        topic: orders.ack
        timeout: 5s
        match:
          - key: payload.orderId == previous.orderId

    - name: check-warehouse
      http:
        method: GET
        path: /warehouse/orders/${previous.orderId}
        expect:
          status: 200
          body:
            status: { in: [received, picking, shipped] }

  checks:
    - name: ack_latency_p99
      expr: percentile(latency('orders', 'orders.ack'), 99) < duration('200ms')
      severity: critical
    - name: no_orphan_cancellations
      expr: |
        stream('cancellations').all(c,
          stream('orders').exists(o,
            o.payload.orderId == c.payload.orderId && before(o.ts, c.ts)
          )
        )
      severity: warning
```

You get a pass/fail result, a JSON report, and a non-zero exit code when a critical check fails. That's the CI mode. No server required.

## Three modes, one scenario

| Mode | Command | What you get |
|---|---|---|
| Run | `edt run --file s.yaml` | One-shot pass/fail. Exit code 0/1/2. JSON or console report. |
| Watch | `edt worker --control-plane https://cp.internal` | Same scenario runs forever. Snapshot pushed every 30s. Prometheus metrics. |
| Eval | `edt eval --file s.yaml --iterations 50` | Seeds inputs, waits for your AI agent's outputs, scores pairs with an LLM judge against your rubric. |

Same YAML. Same checks. Same assertions. What changes is how the engine interprets the `window:` field on your checks and when it decides to stop.

## Why not just write a Go consumer?

You could. Most teams do. It works for the first three scenarios. After that the test file becomes a second product, with its own bugs, its own flakiness, and its own "why isn't this running in CI" debugging. And it still doesn't give you synthetic monitoring.

The other obvious move is `kcat` + `bash`, which is fine until you want to assert something that spans two topics and an HTTP endpoint.

This tool exists because "test my order flow end-to-end with assertions on timing and payload shape" turns out to be a real language-shaped problem, and a YAML that can also become a Prometheus probe is more useful than a script that can't.

## Compared to other tools

- **k6 / Gatling / JMeter.** Load generators. Great at "can this API handle 10k req/s". Not aware of Kafka topics, consumer groups, or correlation keys across streams.
- **Postman.** HTTP-only. No streaming primitives, no temporal checks, no topic correlation.
- **Conduit / Kafka Streams tests / Testcontainers.** Unit-level. You're still writing Go or Java, still in the "tests are code" world. No synthetic monitoring path.
- **Chaos Mesh / Gremlin.** Complementary. They break infrastructure; edt observes whether your business flow still works. Put them in front of Kafka via Toxiproxy or Gateway and edt measures the impact.

## Install

| Method | Command |
|---|---|
| Homebrew | `brew install event-driven-tests-ai/tap/edt` |
| curl | `curl -sSL https://install.edt.io \| sh` |
| Docker | `docker run --rm -v $PWD:/s ghcr.io/event-driven-tests-ai/edt run --file /s/scenario.yaml` |
| Binary | [Releases](https://github.com/sderosiaux/event-driven-tests-ai/releases/latest) (darwin/linux/windows, amd64/arm64) |
| Go | `go install github.com/sderosiaux/event-driven-tests-ai/cmd/edt@latest` |

## Control plane (optional)

Push run reports to a control plane for historised SLOs, scenario versioning, and worker orchestration:

```bash
# Operator
edt serve --db-url postgres://edt@localhost/edt --require-auth --admin-token $(uuidgen)

# CI
edt run --file s.yaml --report-to https://cp.internal --report-token $EDT_TOKEN

# Continuous probe
edt worker --control-plane https://cp.internal --labels env=staging,team=commerce
```

The control plane exposes:

- `POST /api/v1/scenarios` — version-controlled scenario store
- `GET /api/v1/runs?scenario=…` — last N runs for any scenario
- `GET /api/v1/eval-runs?scenario=…` — LLM-as-judge eval results per run
- `GET /api/v1/scenarios/{name}/slo?window=1h` — pass-rate per check
- `GET /metrics` — Prometheus scrape target (workers push, control plane exposes)
- `POST /mcp` — JSON-RPC MCP server for Claude Desktop / Claude Code / Cursor, read-only by default with an opt-in write policy for `upsert_scenario` / `assign_scenario`
- Embedded web UI at `/` — scenarios, runs, evals, workers, no build step

## What's supported today

Connectors:

- **Kafka** (franz-go): SASL PLAIN, SCRAM-256/512, mTLS, OAUTHBEARER, AWS IAM (static keys or default SDK credential chain — IRSA, EC2 IMDS, ECS task role)
- **HTTP** with bearer/basic auth and matchers (`in`, `regex`, `gt`, `lt`)
- **WebSocket** (`connectors.websocket`, `step.websocket`): initial-send frame, CEL match rules, slow_mode, count bound — [example](examples/websocket.yaml)
- **SSE** (`step.sse` on the HTTP connector): event-stream parser with JSON + raw-text fallback — [example](examples/sse.yaml)
- **gRPC** unary (`connectors.grpc`, `step.grpc`): inline `.proto` source → dynamicpb invoke, no generated stubs needed — [example](examples/grpc.yaml)

Scenario authoring:

- Canonical YAML DSL (`apiVersion: edt.io/v1`) — [the full schema reflects through `edt validate`](cmd/edt/validate.go)
- TypeScript SDK (`@event-driven-tests-ai/sdk`) with fluent builder + `edt-ts compile` CLI
- Schema Registry (Confluent + Apicurio compat mode): Avro, JSON Schema, and Protobuf (inline `.proto` text; SR references land in M7)
- CEL-based checks with streaming operators: `stream`, `latency`, `percentile`, `rate`, `before`, `forall`/`exists`
- Faker-style data generation with seeded determinism
- Failure injection: `fail_rate`, `fail_mode` (timeout, schema violation, broker unavailable), slow consumers
- `${run.id}` / `${previous.*}` interpolation across topic, group, path, body, gRPC request

## Status

Pre-alpha. The YAML shape is stable enough that M1-M5 scenarios survive migrations, but we haven't cut a 1.0 tag. The Go API may move. Feedback shapes the roadmap.

## Roadmap

Near-term: TypeScript SDK (scenarios as typed code that compiles to YAML), Helm chart, gRPC + WebSocket + SSE connectors, Protobuf codec, GitHub repo write-access for MCP clients. Longer: multi-tenant SaaS managed by Conduktor, richer agent tooling (memory, tool-use rubrics), replay from production topics.

## Contributing

Issues and PRs welcome. If you have a streaming testing pain we don't cover yet, open an issue describing the scenario. That's the most useful signal. Drive-by fixes are great too; we don't require DCO or CLA for small changes.

Open work is tracked in [`TODO.md`](TODO.md); pick any item and open a PR.

## License

Apache 2.0. See [LICENSE](LICENSE).

---

Built by the [Conduktor](https://conduktor.io) team. No Conduktor account needed; edt is independent and works with any Kafka.
