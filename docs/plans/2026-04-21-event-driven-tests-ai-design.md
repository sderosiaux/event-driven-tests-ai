# event-driven-tests-ai — Design Spec v1

Date: 2026-04-21
Status: Draft for approval
Author: Stéphane Derosiaux (with Claude)
License target: Apache 2.0
Working name: `event-driven-tests-ai` (binary: `edt`)

---

## 1. One-line pitch

**A Postman for streaming: write a scenario once, run it in CI as a pass/fail test or let it run forever as a synthetic probe — across Kafka, HTTP, gRPC, WebSocket, SSE — and test AI agents sitting in the flow.**

## 2. Problem

Streaming systems have no Postman-equivalent. Teams either:

- Skip end-to-end tests and "watch prod logs" — admits the testing environment doesn't work (see blog: *Kafka Testing: Beyond Production*).
- Wire together kcat scripts, kafka-producer-perf-test, ad-hoc consumers, JMeter plugins, home-grown Python harnesses. Zero reuse across CI and prod monitoring.
- Buy a heavyweight observability suite that monitors infra but doesn't validate **business intent** (did an `order.placed` reach `warehouse.events` within 2s with the expected payload shape?).

On top of that, 2026 sees the rise of **agentic systems** plugged into Kafka — LLM-driven consumers that decide, classify, trigger side-effects. Nobody has a serious harness for testing the correctness and stability of these agents over time.

## 3. Positioning

A single OSS tool that covers, with one scenario DSL and one engine:

| Mode | Shape | Output |
|---|---|---|
| `run` | One-shot scenario, CI-friendly | Terminal PASS/FAIL + exit code + report pushed to control plane |
| `watch` | Infinite synthetic probe, prod-adjacent | Continuous SLO evaluation over sliding windows, pushed to control plane, re-exposed as Prometheus/OTel |
| `eval` | Agent-under-test harness | Black-box evaluation of an LLM agent with LLM-as-judge scoring over N runs |

All three use the **same YAML scenario**. The execution mode is a flag, not a fork.

**Not** in scope: a replacement for Chaos Mesh, Litmus, Great Expectations (data quality at rest), JMeter (HTTP-only load), Gatling (HTTP-only load), Kafka monitoring dashboards (we emit metrics for them, we don't visualize infra).

## 4. Primary users

| Persona | Pain | Value |
|---|---|---|
| Platform / SRE | "Is our streaming stack meeting business SLOs right now?" | Synthetic scenarios running 24/7, historized, alertable |
| Backend developer | "Does my PR break the order flow?" | `edt run` in CI, fast feedback, scenario lives next to code |
| QA / tester | "I can't test streaming like I test HTTP APIs" | Declarative YAML, no Kafka internals required |
| AI engineer | "My triage agent drifts silently over time" | `edt eval` with LLM-as-judge rubric, regression detection |

## 5. Architecture

### 5.1 Topology

```
┌──────────────────────────────────────────────────────────┐
│                      control plane                        │
│   API (REST + MCP)  │  Web UI  │  Postgres  │  /metrics   │
└────────▲──────────────────────────────────────────────────┘
         │ push (scenarios, runs, checks, findings)
         │ pull (scenario assignments, config)
    ┌────┴─────────┐         ┌──────────────┐
    │   edt CLI    │         │   edt worker │
    │ (run/eval)   │         │   (watch)    │
    │  ephemeral   │         │  long-lived  │
    └────┬─────────┘         └────┬─────────┘
         │                         │
         ▼                         ▼
   ┌──────────────────────────────────┐
   │  Kafka  │  HTTP  │  gRPC  │ WS/SSE │
   │  Schema Registry (CC + Apicurio)   │
   │  System Under Test (SUT)           │
   └──────────────────────────────────┘
```

- Single Go binary: `edt`. Subcommands `run`, `watch`, `eval`, `worker`, `serve`, `capture`.
- `edt serve` = control plane.
- `edt worker --mode=monitor` = long-lived agent consuming scenario assignments.
- `edt run` = CI-style one-shot, pushes report on completion.
- `edt eval` = agent-under-test loop.

### 5.2 Data flow (reporting)

Workers and CLI runs **always** push to control plane (when `--report-to` or `EDT_CONTROL_PLANE` configured). Control plane is the sole source of truth for history. Prometheus/OTel exposition happens **only** at the control plane level. Workers are never scraped directly (cardinality, network, auth).

CI runs without a control plane: writes JSON report to stdout/file. No remote state.

### 5.3 Zero-friction default path

```
brew install edt
edt run --file examples/order-flow.yaml --bootstrap-servers localhost:9092
```

No control plane required. No server. Result on stdout, exit code on fail.

## 6. Scenario DSL

### 6.1 Canonical form: YAML

YAML is the **wire format**. The TypeScript SDK (v1) and future Python SDK (v1.1) compile to this YAML. Control plane stores, diffs, replays, and re-runs YAML. Git is the source of truth for scenarios.

### 6.2 Full example

```yaml
apiVersion: edt.io/v1
kind: Scenario
metadata:
  name: order-flow-e2e
  labels:
    team: commerce
    env: staging

spec:
  connectors:
    kafka:
      bootstrap_servers: ${KAFKA_BOOTSTRAP}
      auth:
        type: sasl_scram_sha_512
        username: ${KAFKA_USER}
        password: ${KAFKA_PASS}
      schema_registry:
        url: ${SR_URL}
        flavor: confluent   # or: apicurio

    http:
      base_url: https://api.staging.acme.io
      auth: { type: bearer, token: ${API_TOKEN} }

  data:
    orders:
      schema:
        source: schema_registry
        subject: orders-value
      generator:
        strategy: faker
        seed: 42
        overrides:
          orderId: "${uuid()}"
          customerId: "${faker.person.id}"
          amount: "${faker.number.float(10, 500)}"

  steps:
    - name: place-order
      produce:
        topic: orders
        payload: ${data.orders}
        rate: 50/s
        fail_rate: 2%         # accepts "2%" or 0.02; simulate flaky producer
        fail_mode: schema_violation   # timeout | broker_not_available | schema_violation

    - name: wait-ack
      consume:
        topic: orders.ack
        group: edt-${run.id}
        match:
          - key: payload.orderId == ${previous.orderId}
        timeout: 5s
        slow_mode:            # lag injection
          pause_every: 100
          pause_for: 500ms

    - name: check-warehouse-api
      http:
        method: GET
        path: /warehouse/orders/${payload.orderId}
        expect:
          status: 200
          body:
            status:
              in: [received, picking, shipped]

  checks:
    - name: order_ack_p99
      expr: |
        percentile(
          latency('orders', 'orders.ack'),
          99
        ) < duration('200ms')
      window: 5m              # ignored in run mode, applied in watch mode
      severity: critical

    - name: no_orphan_cancellations
      expr: |
        stream('cancellations').all(c,
          stream('orders').exists(o,
            o.payload.orderId == c.payload.orderId
              && before(o.ts, c.ts)
          )
        )
      severity: warning

    - name: warehouse_http_availability
      expr: |
        rate(http('/warehouse/orders/*').map(e, int(e.payload.status) == 200)) > 0.995
      window: 1h
      severity: critical
```

### 6.3 DSL sections

| Section | Purpose | Required |
|---|---|---|
| `connectors` | Endpoints + auth for Kafka, HTTP, gRPC, WS, SSE, Schema Registry | yes |
| `data` | Data generators (faker + schema-driven) | when producing |
| `steps` | Ordered orchestration: produce, consume, http, grpc, ws, sse, sleep, assert-inline | yes |
| `checks` | Deterministic assertions, with optional `window` + `severity` | recommended |
| `agent_under_test` | Only for `edt eval` mode — see §10 | eval mode only |
| `evals` | LLM-as-judge scoring, only for `edt eval` | eval mode only |

No `observers` section (no passive LLM observer — explicitly out of v1 scope).

## 7. Check expression language — CEL + streaming operators

CEL (Google Common Expression Language) chosen for: known semantics, proven sandbox, familiar to K8s/Envoy users, fast eval.

### 7.1 Added operators (on top of standard CEL)

The check language is CEL plus a small set of streaming-aware functions.
Universal/existential quantification uses CEL's standard `.all(e, P)` and
`.exists(e, P)` macros — there is no special `forall`/`exists` keyword.

| Operator | Semantics | Example |
|---|---|---|
| `stream(name)` | List of events observed in the named stream | `stream('orders')` |
| `latency(from, to)` | Per-key durations from `from` events to the next matching `to` event; each `to` event is consumed at most once | `latency('orders', 'orders.ack')` |
| `percentile(values, p)` | p-th percentile (Type 7 / linear interpolation); empty input is an error | `percentile(latency(...), 99)` |
| `rate(list<bool>)` | Fraction of `true` values; empty list returns 0 | `rate(http('/api').map(e, int(e.payload.status) == 200))` |
| `before(t1, t2)` | True when timestamp t1 is strictly before t2 | `before(o.ts, c.ts)` |
| `duration(s)` | Standard CEL duration literal | `duration('200ms')` |
| `http(pathPattern)` | Events for HTTP calls whose path matches a `*`-glob pattern | `http('/warehouse/*')` |

Event shape exposed to CEL: `{stream, key, ts, headers, payload, direction}`.
Payload fields are accessed through `.payload`, e.g. `o.payload.orderId`.

### 7.2 Window semantics

- `window: <duration>` on a check.
- Mode `run`: window is **ignored**, check evaluated over the entire run.
- Mode `watch`: window is a sliding window. Check is re-evaluated on each event ingress at most once per second.
- Mode `eval`: window typically expressed over N runs (`over 50 runs`) rather than duration.

### 7.3 Severity → reporting

| Severity | `run` behavior | `watch` behavior |
|---|---|---|
| `critical` | exit code 1, PR blocked | alert fires, SLO metric `critical_burn_rate` ticks |
| `warning` | exit code 0, annotated report | alert in secondary channel, no page |
| `info` | never fails | counter only, no alert |

## 8. Connectors — v1 matrix

### 8.1 Kafka

- Wire protocol client: **franz-go** (2026 state-of-the-art, zero cgo, clean API).
- Covers by design: Apache Kafka, Redpanda, WarpStream, Confluent Cloud, Aiven, MSK, StreamNative (Kafka-on-Pulsar).
- Auth modes v1: `sasl_plain`, `sasl_scram_sha_256`, `sasl_scram_sha_512`, `mtls`, `sasl_oauthbearer` (OIDC), `aws_iam` (MSK IAM).
- Features: produce, consume (group or assign), commit modes, header propagation, transactional producer (exactly-once semantics tests), consumer group membership + rebalance observation.

### 8.2 Schema Registry

- Flavors: Confluent-compatible (CC, Apicurio in CC mode, Redpanda SR) **and** Apicurio native.
- Formats: Avro, Protobuf, JSON Schema.
- Features: resolve subject → schema, validate payloads, **enforce compatibility checks** as optional assertions in scenarios.

### 8.3 HTTP + gRPC + WebSocket + SSE

- **HTTP**: full REST client. Method, headers, body (JSON / XML / text / binary), TLS, retries, follow-redirects toggle.
- **gRPC**: proto descriptor loading (file or reflection), unary + client-streaming + server-streaming + bidi.
- **WebSocket**: connect, send, receive-until (pattern), timeout, close-code assertion.
- **SSE**: subscribe, pattern match on events, close-on timeout.
- All four share the same `expect` block: `status`, `body`, `headers`, CEL expression on payload.

## 9. Failure injection — in-scenario only

Scope in v1 (validated):

| Type | Location in DSL | Example |
|---|---|---|
| Producer failures | `steps[].produce.fail_rate` + `fail_mode` | 5% schema_violation, 2% timeout, 1% broker_not_available |
| Slow consumer / lag | `steps[].consume.slow_mode` | pause_every 100, pause_for 500ms, or cumulative lag budget |
| HTTP flakiness | `steps[].http.fail_rate` | 3% 5xx, 1% timeout |

**Out of v1**: broker-level chaos (kill broker, partition network, reduce RF). Documented pattern: deploy Toxiproxy / Conduktor Gateway / Envoy in front of Kafka to inject network chaos externally. `edt` observes; it does not kill infra.

## 10. Agent-under-test (`edt eval`)

### 10.1 Model — black box + LLM-as-judge

The agent being tested is **external**: it consumes and/or produces topics, and/or exposes HTTP endpoints. `edt` does not call the agent directly; it produces inputs into its source topics, waits for outputs on its sink topics, and evaluates them.

### 10.2 DSL extension

```yaml
spec:
  agent_under_test:
    name: order-triage-agent
    consumes: [orders.new]
    produces: [orders.triaged]
    http_endpoints: []            # optional: if agent also exposes HTTP

  evals:
    - name: triage_correctness
      judge:
        model: claude-opus-4-7
        rubric: |
          Score 1-5 whether the triaged
          category matches the order
          content and severity field.
        rubric_version: v1.2      # pinned, historized
      threshold:
        aggregate: avg
        value: ">= 4.2"
        over: 50 runs

    - name: triage_schema_strict
      expr: |
        forall e in stream('orders.triaged'):
          e.category in ['refund', 'ship', 'hold']
      severity: critical

    - name: triage_latency_p95
      expr: |
        percentile(
          latency(orders.new -> orders.triaged),
          95
        ) < duration('3s')
      severity: warning
```

### 10.3 Eval pipeline

1. For each input event seeded by `edt`, record input.
2. Capture corresponding output event (matched by correlation key).
3. Two parallel evaluators:
   - **Deterministic**: schema + CEL checks (same as §7).
   - **Semantic**: LLM judge scores the (input, output) pair against the rubric.
4. Aggregate scores per eval. Compare to threshold.
5. Full trace (input, output, judge prompt, judge response, score) stored in control plane.

### 10.4 Cost control

- `budget.tokens_per_run: N`, `budget.tokens_per_window: N` (for watch mode).
- `sampling.strategy: all | every_nth | reservoir(k)`.
- When budget exceeded: eval emits `skipped` with reason; never fails.

## 11. Control plane

### 11.1 Responsibilities

1. Store scenarios (YAML, versioned, git-like history of changes).
2. Assign scenarios to registered workers (label selector).
3. Ingest runs, checks, evals, findings, events sampled from workers.
4. Serve API (REST + MCP).
5. Serve web UI (read-first, write via forms).
6. Expose `/metrics` Prometheus endpoint (aggregated from all workers).
7. Authenticate users (local + OIDC) and service tokens (worker auth).

### 11.2 Storage — Postgres only (v1)

- One Postgres database.
- TimescaleDB extension optional (activated → hypertables for runs, events, checks).
- Tables (logical model):

| Table | Purpose |
|---|---|
| `scenarios` | YAML + version + metadata |
| `workers` | Registered workers + heartbeat + labels |
| `scenario_assignments` | Which scenario runs on which worker (watch mode) |
| `runs` | One row per run (CI `run`, or one continuous watch segment) |
| `check_results` | Per-check result, timestamp, window, value observed |
| `eval_results` | Per-eval score + judge trace reference |
| `findings` | Failures, anomalies, annotations (human + system) |
| `events_sample` | Capped sample of raw events observed (audit trail, JSONB) |
| `tokens` | Service auth tokens for workers + API clients |

Retention policies per table, defaults: 90 days runs, 30 days event samples, infinite scenarios.

### 11.3 API

- REST API (OpenAPI 3.1 spec published).
- Versioned under `/api/v1/`.
- All mutations via tokens; read endpoints support viewer tokens.
- WebSocket endpoint `/api/v1/stream` for live run tailing.
- Core endpoints (non-exhaustive):

```
POST   /api/v1/scenarios
GET    /api/v1/scenarios
GET    /api/v1/scenarios/{name}
PUT    /api/v1/scenarios/{name}           # version bumps automatically
POST   /api/v1/scenarios/{name}/run       # trigger run, returns run_id
GET    /api/v1/runs/{run_id}
GET    /api/v1/runs/{run_id}/checks
GET    /api/v1/runs/{run_id}/events       # sampled
POST   /api/v1/workers/register
POST   /api/v1/workers/{id}/heartbeat
GET    /api/v1/slo?scenario=&window=
GET    /metrics                            # Prometheus
```

### 11.4 MCP server — v1 first-class

Control plane exposes an MCP server at `/mcp`. Tools (subset):

- `list_scenarios(labels?)`
- `get_scenario(name, version?)`
- `run_scenario(name, overrides?)` — triggers a `run`, streams result
- `get_run(run_id)`
- `list_sla_breaches(scenario?, window?)`
- `annotate_finding(finding_id, note)`
- `propose_check(scenario_name, cel_expr, rationale)` — authenticated write

LLM agents (Claude Code, Cursor, Claude Desktop) drive the product natively. This is the "AI-native" signal of the product.

### 11.5 Web UI (v1 MVP)

- Scenarios list, scenario detail (YAML + recent runs + SLO trend).
- Run detail (steps timeline, check results, event sample, findings).
- SLO dashboard (per scenario, per window).
- Eval dashboard (score trend, judge traces, pass rate).
- Worker inventory (heartbeat, assigned scenarios, health).

No "advanced RBAC" in v1 — only admin / editor / viewer roles. Multi-tenant is out of v1 scope.

## 12. Metrics & observability

Control plane exposes on `/metrics`:

```
edt_run_total{scenario,mode,result}
edt_run_duration_seconds{scenario,mode}
edt_check_result_total{scenario,check,severity,result}
edt_check_value{scenario,check}              # gauge (e.g., current p99 latency)
edt_slo_window_pass_rate{scenario,check,window}
edt_eval_score{scenario,eval}                # gauge, avg over window
edt_worker_up{worker_id,labels}
edt_worker_last_heartbeat_seconds
```

Cardinality policy: `scenario` and `check` labels are bounded by scenario config (user's responsibility). Payload values never become labels. Event-level data lives in Postgres, not Prometheus.

Workers also emit OTel traces on each run (step-level spans). Traces pushed to control plane, forwarded to a user-configured OTLP endpoint.

## 13. Failure modes of the product itself

- Control plane down → workers buffer up to 1 hour of check results on local disk, resume on reconnection. CI runs without control plane still run (just lose remote history).
- Worker crash → scenario is reassigned to another worker matching the label selector within 30s. Scenario state (cumulative windows) rebuilt from control plane history on restart.
- Postgres down → control plane returns 503 on writes, read cache serves last-known scenarios for assignment.

## 14. Packaging & distribution

- **Binary releases** on GitHub: `darwin-{arm64,amd64}`, `linux-{arm64,amd64}`, `windows-amd64`. Signed with Sigstore/cosign.
- **Homebrew tap**: `brew install event-driven-tests-ai/tap/edt`.
- **Docker image**: `ghcr.io/event-driven-tests-ai/edt:v1.0.0`. Single image, entrypoint dispatches on subcommand.
- **Helm chart**: `helm install edt oci://ghcr.io/event-driven-tests-ai/charts/edt` for control plane + workers in K8s.
- **Out of v1**: Terraform module, marketplaces (AWS/GCP).

## 15. License

**Apache 2.0** pure. Everything OSS: engine, CLI, workers, control plane (UI + API + storage). No BSL, no AGPL, no "open core" games. Conduktor can later offer a managed SaaS on top with operational value-adds (SSO enterprise, multi-tenant, long-term retention, on-call integration) without restricting the OSS project.

## 16. Milestones (ordering, no time estimates)

### M1 — skeleton
- Go repo, CI, release pipeline, Docker image.
- YAML scenario parser + JSON Schema.
- CEL + streaming operators evaluator.
- Kafka produce/consume (SASL_PLAIN, SCRAM, mTLS).
- HTTP connector.
- `edt run` with stdout report. No control plane.

### M2 — control plane + watch mode
- Postgres-backed control plane.
- Worker registration + assignment protocol.
- `edt worker --mode=monitor`.
- Prometheus `/metrics`.
- Minimal web UI (scenarios, runs, SLOs).

### M3 — Schema Registry + auth expansion
- Confluent SR + Apicurio.
- Avro / Protobuf / JSON Schema.
- OAUTHBEARER, AWS IAM.

### M4 — TS SDK
- `@edt/sdk` on npm. Compiles to canonical YAML.

### M5 — Eval mode + MCP
- `edt eval`.
- LLM-as-judge with Claude + OpenAI + Bedrock routes.
- MCP server on control plane.

### M6 — Data generators v2 + failure injection polish
- Advanced Faker presets.
- HTTP/gRPC/WS/SSE connectors feature-complete.
- Helm chart.

## 17. Non-goals (explicit)

- Not a Kafka monitoring UI (Conduktor Console, Kowl/Redpanda Console, Kafdrop cover that).
- Not a chaos engineering platform (Chaos Mesh, Litmus).
- Not a data-quality-at-rest tool (Great Expectations, Soda).
- Not a load generator optimized for raw throughput (Gatling, k6 — we don't compete on 1M RPS benchmarks).
- Not multi-tenant SaaS in v1.
- Not a visual scenario builder (v1 is YAML + SDK).
- Not a passive semantic LLM observer (explicitly descoped).

## 18. Open questions / flagged for later

- Retention policy for raw event samples — default 30 days too short? too long?
- Authentication between worker and control plane: short-lived JWT vs long-lived tokens vs mTLS — default?
- Scenario versioning semantics: semver vs content-hash vs monotonic integer — pick in M2.
- Concurrency limits: how many parallel watch scenarios per worker? Default guardrail.
- i18n of the web UI (v1 English-only is fine; flagged for later).

---

## Appendix A — CLI surface sketch

```
edt run       --file <scenario.yaml> [--report-to <url>]
edt watch     --file <scenario.yaml>     # local watch, not a worker
edt eval      --file <scenario.yaml>
edt worker    --control-plane <url> --labels <k=v,...>
edt serve     --config <config.yaml>     # control plane
edt capture   --topic <t> --output <f>   # capture-replay: DEFERRED to post-v1 per decision
edt validate  --file <scenario.yaml>     # JSON Schema validation, no run
edt scenarios list|get|apply              # talk to control plane
```

## Appendix B — Decisions record (pointers to Q&A)

| # | Question | Decision |
|---|---|---|
| Q1 | Product nature | Test (run) + Monitoring (watch) + Eval (agent-under-test), all first-class, same DSL |
| Q2 | LLM agent role | Only agent-under-test harness (d). No passive observer (a). |
| Q3 | DSL shape | YAML canonical + TS SDK v1 (Python v1.1) |
| Q4 | Topology | CLI + optional self-hosted control plane, path to worker-registration protocol |
| Q5 | Check language | CEL + ~10 streaming operators |
| Q6 | Engine language | Go |
| Q7 | Protocol scope | Kafka + Schema Registry (CC + Apicurio) + HTTP/gRPC/WS/SSE |
| Q8 | Data sourcing | Faker + schema-driven only in v1 |
| Q9 | Agent-under-test | Black box + LLM-as-judge |
| Q10 | License | Apache 2.0 pure |
| Q11 | Storage | Postgres only |
| Q12 | Kafka auth | SASL/PLAIN, SCRAM-256/512, mTLS, OAUTHBEARER/OIDC, AWS IAM |
| Q13 | Chaos scope | Producer fail_rate, consumer lag injection. Broker chaos out, delegated to Gateway-style proxies. |
| Q14 | MCP | V1, first-class |
| Q15 | Packaging | Go binary + Docker image v1 |
| Q16 | Name | event-driven-tests-ai (binary: `edt`) |
