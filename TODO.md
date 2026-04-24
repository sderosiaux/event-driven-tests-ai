# TODO

Open work. Pick any item; open an issue before large refactors.

## Ship-blockers for v0.1.0

- [ ] **Tag v0.1.0 and release binaries.** GoReleaser workflow exists. `git tag v0.1.0 && git push --tags` publishes multi-arch binaries + `ghcr.io/sderosiaux/event-driven-tests-ai:0.1.0`.
- [ ] **Tag sdk-ts/v0.1.0 and publish the SDK to npm.** Workflow wired; requires `NPM_TOKEN` repo secret and the `@event-driven-tests-ai` scope linked for npm provenance.
- [ ] **Watch-mode smoke test end to end.** Only `edt run` one-shot is covered in the docker-compose demo. Bring up a worker pinned to the control plane, assign a scenario via MCP or `/api/v1/workers/{id}/assignments`, and confirm the loop produces reports.
- [ ] **Token rotation playbook.** `EDT_ADMIN_TOKEN` / `EDT_WORKER_TOKEN` can rotate via a rolling restart, but the order matters (issue new token, deploy, revoke old). Document it in `charts/edt/README.md`.

## Protobuf + Schema Registry

- [ ] **SR references for Protobuf.** `pkg/schemaregistry/proto.go` and `pkg/grpcc/client.go` both reject `.proto` imports. Real Confluent SR Protobuf users rely on references. Resolver design: fetch referenced subjects recursively from SR, feed them to `protocompile.WithStandardImports` via a per-scenario `SourceResolver`.
- [ ] **SR reference support for the inline scenario form.** Same shape but resolved from a scenario-local `map[string]string`.

## Connectors

- [ ] **gRPC streaming RPCs.** Unary-only today. Client/server/bidi streaming need a step type that streams into the events store; match rules evaluate per inbound frame (same as `consume`).
- [ ] **gRPC metadata expectations.** `GRPCExpect` covers code + body; trailers and initial metadata aren't checked.
- [ ] **SSE `Last-Event-ID` resume.** The parser reads IDs but never replays on reconnect.
- [ ] **WebSocket ping/pong configuration.** Default `coder/websocket` settings are fine; expose keep-alive interval for long-running watch-mode subscribers.

## Control plane

- [ ] **Rate limiting.** Bursty agents pointing at `/mcp` can hammer Postgres. Chi-middleware token bucket per role is enough.
- [ ] **Eval-run `GET /api/v1/eval-runs/{id}`: attach per-iteration raw samples.** Today the endpoint returns only aggregates; per-sample judge traces land with Codex #54 follow-up.
- [ ] **Postgres backup/restore guide.** Document `pg_dump` cadence + replay.
- [ ] **Worker HPA.** Chart exposes `worker.replicaCount` only.

## SDK

- [ ] **Release automation: release-please or similar.** Manual CHANGELOG + version bump is fine for v0.1; automate before v0.3.
- [ ] **gRPC builder method.** `grpcStep()` doesn't exist yet; mirrors `wsStep` + proto inline.
- [ ] **Scenario validation at build time.** Run `edt validate --stdin` against `emit(scenario)` during CLI compile; fail fast before the user gets a runtime error.

## UI

- [ ] **Eval-run detail view.** `/ui/evals/{id}` lists per-eval results (samples, threshold, pass/fail, errors).
- [ ] **SLO dashboard.** `/api/v1/scenarios/{name}/slo` is exposed but not rendered.
- [ ] **Scenario diff.** Versions are persisted; no way to compare v3 vs v4 in the UI.

## Tests + CI

- [ ] **Fuzz test the CEL parser.** `pkg/checks/cel.go` eval handles user expressions; a malformed expression should never panic.
- [ ] **Integration test with a real Kafka broker** (testcontainers). Current orchestrator tests use the in-memory store.
- [ ] **Dependabot / Renovate.** No automated dependency PRs yet.

## Observability

- [ ] **Tracing.** Control plane + worker have no spans. OTLP export would slot in via chi middleware.
- [ ] **Structured logging.** `cfg.Logger` is `func(format string, args ...any)`; plumb slog through the control plane and worker.

## Known technical debt

- [ ] **`pkg/orchestrator` port shapes.** WS/SSE return `Session` with `Next` / `Read`; gRPC returns `*Invocation` directly. A fourth connector should drive a unifying refactor, not reinforce the drift.
- [ ] **`pkg/grpcc` adapter caches one connection per scenario run.** If a scenario targets two gRPC servers, the cache redials correctly but leaks timers during the switch. Not a problem for typical scenarios; worth revisiting when streaming lands.
