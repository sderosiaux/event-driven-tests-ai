# event-driven-tests-ai — M2 Control Plane Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: superpowers:executing-plans to implement this task-by-task.

**Goal:** Deliver M2 from the design spec. After M2 the OSS install can:
1. Run `edt serve` to expose a control plane (REST API + minimal web UI + Prometheus `/metrics`).
2. Run `edt worker --control-plane <url>` to enrol a worker and run assigned scenarios in `watch` mode (continuous).
3. `edt run --report-to <url>` pushes the run report to the control plane for historization.

**Architecture:** Single Go binary, new subcommands `serve` and `worker`. The control plane is an HTTP service backed by Postgres (single store as decided in spec §11.2). Workers register, heartbeat, and pull scenario assignments. `edt run` keeps working stand-alone; `--report-to` is the only new flag that opts into the control plane.

**Tech Stack additions:**
- HTTP router: `github.com/go-chi/chi/v5` (lightweight, idiomatic, no global state).
- Postgres driver: `github.com/jackc/pgx/v5` + `pgx/v5/pgxpool`.
- Migrations: `github.com/jackc/tern/v2/migrate` (or embedded SQL files run by a small migrator — pick during Task M2-T2 after Context7 lookup).
- Prometheus: `github.com/prometheus/client_golang`.
- Web UI: minimal HTML/JS shipped via `embed.FS` — no SPA framework in M2 (we keep it server-rendered + a few `fetch()` calls).
- Postgres in tests: Testcontainers Postgres module under `//go:build integration`.

**Context7 policy reminder:** before introducing pgx, chi, prom client, tern — call Context7 `resolve-library-id` then `query-docs` for the modern API.

---

## Task M2-T1 — `edt serve` skeleton + control-plane package layout

**Files:**
- Create: `cmd/edt/serve.go`
- Create: `pkg/controlplane/server.go`
- Create: `pkg/controlplane/server_test.go`
- Modify: `cmd/edt/main.go` (register serve)

**Step 1** — Test: `serve` returns 200 + JSON `{"status":"ok"}` on `GET /healthz`.
**Step 2** — `pkg/controlplane.Server` wraps `*http.Server` + chi router; `NewServer(cfg Config) *Server`; `Run(ctx) error`.
**Step 3** — `cmd/edt/serve.go`: cobra cmd; flags `--addr`, `--db-url`, `--config`. Build a server, run until ctx cancelled.
**Step 4** — Wire `serve` in `main.go`; verify `edt serve --help` shows.
**Step 5** — Commit: `feat(controlplane): edt serve skeleton with /healthz`.

## Task M2-T2 — Storage interface + Postgres impl + migrations

**Files:**
- Create: `pkg/controlplane/storage/storage.go`
- Create: `pkg/controlplane/storage/postgres.go`
- Create: `pkg/controlplane/storage/migrations/0001_init.sql`
- Create: `pkg/controlplane/storage/postgres_test.go` (`//go:build integration`)

**Step 1** — Define `type Storage interface` with: `UpsertScenario`, `GetScenario`, `ListScenarios`, `RecordRun`, `GetRun`, `ListRuns`, `RegisterWorker`, `Heartbeat`, `ListWorkers`, `AssignScenario`, `ListAssignments`.
**Step 2** — `0001_init.sql` creates: `scenarios`, `scenario_versions`, `workers`, `assignments`, `runs`, `check_results`, `findings`, `events_sample`, `tokens`. Use `BIGSERIAL` ids, `JSONB` for payloads, `TIMESTAMPTZ` for times.
**Step 3** — Tiny embedded migrator: walk `migrations/*.sql` in lexical order, apply inside a transaction, record applied versions in `schema_migrations`.
**Step 4** — `pgxpool.New` Postgres impl. One `Storage` method per query, parameterized.
**Step 5** — Integration test: spin Postgres via Testcontainers, run migrations, assert tables exist, round-trip a scenario + run.
**Step 6** — Commit: `feat(controlplane/storage): Postgres-backed storage with embedded migrations`.

## Task M2-T3 — REST API: scenarios CRUD

**Files:**
- Create: `pkg/controlplane/api/scenarios.go`
- Create: `pkg/controlplane/api/scenarios_test.go`
- Modify: `pkg/controlplane/server.go` (mount routes)

Endpoints (per spec §11.3):
- `POST /api/v1/scenarios` — body = YAML, parsed and validated.
- `GET /api/v1/scenarios` — list summaries.
- `GET /api/v1/scenarios/{name}` — return current version + metadata.
- `PUT /api/v1/scenarios/{name}` — bumps a new version.

**Steps:** httptest server bound to a fake Storage; assert 200/400/404 on each endpoint; exact JSON shape in fixtures.

**Commit:** `feat(controlplane/api): scenarios CRUD with versioning`.

## Task M2-T4 — REST API: runs ingestion + listing

**Files:**
- Create: `pkg/controlplane/api/runs.go`
- Create: `pkg/controlplane/api/runs_test.go`

Endpoints:
- `POST /api/v1/runs` — accepts a `report.Report` JSON. Persists run, check_results, sample events.
- `GET /api/v1/runs/{run_id}` — full run incl. checks.
- `GET /api/v1/runs?scenario=&limit=` — most recent N for a scenario.
- `GET /api/v1/scenarios/{name}/slo?window=1h` — pass-rate per check over a window.

**Commit:** `feat(controlplane/api): run ingestion + SLO queries`.

## Task M2-T5 — `edt run --report-to <url>` wiring

**Files:**
- Modify: `cmd/edt/run.go`
- Create: `pkg/reporter/client.go`
- Create: `pkg/reporter/client_test.go`

**Step 1** — `pkg/reporter.Client` POSTs `*report.Report` to `<url>/api/v1/runs` with optional `Authorization: Bearer <token>`.
**Step 2** — `cmd/edt/run.go`: when `--report-to` set, call `Client.PushReport(ctx, rep)` after Finalize. Network failure logs to stderr, never alters exit code (CI must not fail because the control plane is down).
**Step 3** — Tests: httptest server; assert correct path + body; failure path keeps exit code intact.
**Step 4** — Commit: `feat(cli): edt run --report-to pushes the report to the control plane`.

## Task M2-T6 — Worker protocol + `edt worker`

**Files:**
- Create: `cmd/edt/worker.go`
- Create: `pkg/controlplane/api/workers.go`
- Create: `pkg/worker/loop.go`
- Create: `pkg/worker/loop_test.go`

Endpoints:
- `POST /api/v1/workers/register` — body `{labels: {...}, version}`. Returns `{worker_id, token}`.
- `POST /api/v1/workers/{id}/heartbeat` — refreshes liveness, returns assigned scenarios diff.
- `GET /api/v1/workers` — list.

`pkg/worker.Loop`:
- registers, then in a goroutine: pull assignments every N seconds, run each as a `watch` Runner in its own goroutine, push `report.Report` snapshots on `--report-interval`.

**Commit:** `feat(controlplane,worker): registration + heartbeat + assignment fanout`.

## Task M2-T7 — `watch` execution mode in orchestrator

**Files:**
- Modify: `pkg/orchestrator/run.go`
- Modify: `pkg/checks/check.go`
- Create: `pkg/orchestrator/watch.go`
- Create: `pkg/orchestrator/watch_test.go`

`watch` semantics per spec §3 + §7.2:
- Scenario steps loop forever; one full pass = one "iteration".
- Checks evaluated against the events.Store **windowed** view (`Since(now - window)`).
- Each check tick produces `check_value` + `check_pass_rate{window}` metrics.
- Worker pushes a partial report every `--report-interval` (default 30s).

**Commit:** `feat(orchestrator): watch mode with sliding-window check evaluation`.

## Task M2-T8 — Prometheus `/metrics` aggregation

**Files:**
- Create: `pkg/controlplane/metrics/metrics.go`
- Modify: `pkg/controlplane/server.go`

Per spec §12 — control plane is the sole scrape target. Counters/gauges:
- `edt_run_total{scenario,mode,result}`
- `edt_run_duration_seconds{scenario,mode}`
- `edt_check_result_total{scenario,check,severity,result}`
- `edt_check_value{scenario,check}` (gauge)
- `edt_slo_window_pass_rate{scenario,check,window}`
- `edt_worker_up{worker_id}`
- `edt_worker_last_heartbeat_seconds`

Workers do **not** expose Prom directly.

**Commit:** `feat(controlplane/metrics): Prometheus /metrics exposed by control plane`.

## Task M2-T9 — Minimal web UI

**Files:**
- Create: `pkg/controlplane/ui/static/index.html`
- Create: `pkg/controlplane/ui/static/app.js`
- Create: `pkg/controlplane/ui/embed.go` (`//go:embed`)
- Modify: `pkg/controlplane/server.go`

Pages:
- `/` — list scenarios.
- `/scenarios/{name}` — scenario detail + recent runs + SLO trend.
- `/runs/{id}` — run detail + check results.
- `/workers` — worker inventory.

Server-rendered HTML; `app.js` only does `fetch()` polling on the runs page. No build step.

**Commit:** `feat(controlplane/ui): minimal embedded web UI`.

## Task M2-T10 — Auth tokens + role separation

**Files:**
- Create: `pkg/controlplane/auth/tokens.go`
- Modify: `pkg/controlplane/api/*.go` (middleware)

- Storage table `tokens` with role: `admin | editor | viewer | worker`.
- Middleware checks `Authorization: Bearer <token>` on mutating endpoints.
- Worker registration mints a one-shot worker token.

**Commit:** `feat(controlplane/auth): bearer-token role middleware`.

## Out of M2 (deferred)

- Schema Registry + Avro/Proto/JSON Schema (M3)
- SASL OAUTHBEARER + AWS IAM (M3)
- TS SDK (M4)
- `edt eval` + LLM-as-judge + MCP server (M5)
- Helm chart, Terraform module, marketplace listings (M6)

## Acceptance — M2 is done when

1. `go test ./... -race -count=1` passes; `go test -tags=integration ./...` passes against Docker.
2. `edt serve --db-url postgres://...` starts; `/healthz`, `/metrics`, `/`, `/api/v1/scenarios` all respond.
3. `edt run --file s.yaml --report-to http://cp:8080` writes a run to Postgres and the run is visible in the UI within 2s.
4. `edt worker --control-plane http://cp:8080 --labels env=staging` registers, heartbeats, picks up an assignment, and pushes check results that populate `/metrics`.
5. Killing the worker for 30s + restarting resumes the same scenario without losing per-stream state from the in-memory store (state is rebuilt by replaying the last window from control plane history — out of scope for M2 polish, document as known limitation).
