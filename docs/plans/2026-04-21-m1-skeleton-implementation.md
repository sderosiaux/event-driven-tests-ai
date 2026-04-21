# event-driven-tests-ai — M1 Skeleton Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver M1 from the design spec — a working `edt run` CLI that parses a YAML scenario, produces/consumes Kafka with SASL/mTLS auth, calls HTTP endpoints, evaluates CEL+streaming-operator checks, and emits a JSON/console report. Zero control plane.

**Architecture:** Single Go module `github.com/event-driven-tests-ai/edt`. Cobra CLI dispatching to subcommands. Modular packages (scenario, checks, kafka, http, data, events, orchestrator, report). TDD throughout — unit tests for pure code, Testcontainers for Kafka integration.

**Tech Stack:**
- Go 1.23+
- YAML: `gopkg.in/yaml.v3`
- JSON Schema: `github.com/invopop/jsonschema` (struct → schema)
- CLI: `github.com/spf13/cobra`
- CEL: `github.com/google/cel-go`
- Kafka: `github.com/twmb/franz-go` (+ `franz-go/pkg/sasl/scram`, `franz-go/pkg/sasl/oauth`, `franz-go/pkg/sasl/aws`)
- HTTP: stdlib `net/http`
- Faker: `github.com/go-faker/faker/v4`
- Testing: stdlib `testing` + `github.com/stretchr/testify` + `github.com/testcontainers/testcontainers-go` (Kafka module)
- Release: GoReleaser + GitHub Actions

**Context7 policy:** Before coding with any of the libraries above, resolve their current docs via Context7 MCP (`resolve-library-id` then `query-docs`). Do not trust training-data API knowledge.

---

## Task 1 — Initialize Go module + CI skeleton

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `.github/workflows/ci.yml`
- Create: `LICENSE` (Apache 2.0)
- Create: `README.md` (stub)
- Create: `cmd/edt/main.go`

**Step 1** — `go mod init github.com/event-driven-tests-ai/edt` (Go 1.23).

**Step 2** — Write `cmd/edt/main.go` with a bare cobra root command printing `edt — event-driven-tests-ai`. Minimal.

**Step 3** — `.gitignore`: `dist/`, `*.test`, `edt`, `.idea/`, `.vscode/`.

**Step 4** — `Makefile` targets: `build`, `test`, `lint`, `fmt`. `test` runs `go test ./... -race -count=1`.

**Step 5** — `.github/workflows/ci.yml`: Go 1.23, checkout, `go build ./...`, `go test ./... -race`. Run on PR and push to main.

**Step 6** — `git init`, first commit: `chore: bootstrap Go module and CI`.

---

## Task 2 — Scenario types + YAML parser

**Files:**
- Create: `pkg/scenario/types.go`
- Create: `pkg/scenario/parse.go`
- Test: `pkg/scenario/parse_test.go`
- Create: `pkg/scenario/testdata/minimal.yaml`
- Create: `pkg/scenario/testdata/full.yaml`
- Create: `pkg/scenario/testdata/invalid.yaml`

**Step 1** — Test first (`parse_test.go`):
```go
func TestParseMinimal(t *testing.T) {
    data, _ := os.ReadFile("testdata/minimal.yaml")
    s, err := scenario.Parse(data)
    require.NoError(t, err)
    require.Equal(t, "demo", s.Metadata.Name)
}
```

**Step 2** — `testdata/minimal.yaml`:
```yaml
apiVersion: edt.io/v1
kind: Scenario
metadata: { name: demo }
spec:
  connectors:
    kafka: { bootstrap_servers: localhost:9092 }
  steps: []
```

**Step 3** — Run test, expect failure (no types yet).

**Step 4** — `types.go`: structs `Scenario`, `Metadata`, `Spec`, `Connectors`, `KafkaConnector`, `HTTPConnector`, `Step`, `ProduceStep`, `ConsumeStep`, `HTTPStep`, `Check`, `Data`, `Generator`, `AgentUnderTest`, `Eval`. Tag fields with `yaml:"..."` and `json:"..."` (for schema). Use pointer types where optional to distinguish zero-value from absent.

**Step 5** — `parse.go`: `func Parse(b []byte) (*Scenario, error)` using `yaml.Unmarshal`. Wrap errors with context.

**Step 6** — Run test — PASS.

**Step 7** — Add `TestParseFull` with `testdata/full.yaml` reproducing the full example from the design spec §6.2.

**Step 8** — Add `TestParseInvalidYAML` — malformed YAML returns error.

**Step 9** — Commit: `feat(scenario): YAML parser and types`.

---

## Task 3 — JSON Schema generation + validation

**Files:**
- Create: `pkg/scenario/schema.go`
- Test: `pkg/scenario/schema_test.go`
- Create: `cmd/edt/validate.go`
- Create: `pkg/scenario/schema/edt.io-v1.json` (generated)

**Step 1** — Test: `TestGenerateSchema` — `schema.Generate()` returns a JSON Schema with required fields `apiVersion`, `kind`, `metadata`, `spec`.

**Step 2** — `schema.go`: use `invopop/jsonschema` reflector over `Scenario{}`. Write helper `WriteSchemaFile(path string) error`.

**Step 3** — `TestValidateAgainstSchema` — invalid scenario (missing `kind`) returns validation error via `santhosh-tekuri/jsonschema`.

**Step 4** — `cmd/edt/validate.go`: `edt validate --file <path>` reads YAML, parses, validates against schema, exits 0 or 1 with details.

**Step 5** — Wire the subcommand into `main.go`.

**Step 6** — Commit: `feat(scenario): JSON Schema generation + edt validate`.

---

## Task 4 — Events store (in-memory)

**Files:**
- Create: `pkg/events/event.go`
- Create: `pkg/events/store.go`
- Test: `pkg/events/store_test.go`

**Step 1** — Test: `TestStoreAppendAndQuery` — append events with `(stream, key, ts, payload)`, query by stream name, get ordered slice.

**Step 2** — `event.go`: type `Event { Stream string; Key string; Ts time.Time; Headers map[string]string; Payload any; Direction string /* produced|consumed|http|agent_out */ }`.

**Step 3** — `store.go`: `type Store interface { Append(Event); Query(stream string) []Event; All() []Event }`. In-memory impl backed by `sync.Mutex` + slices indexed by stream. Keep last N per stream to cap memory (configurable, default 100k per stream).

**Step 4** — Test: eviction when N exceeded (FIFO).

**Step 5** — Commit: `feat(events): in-memory event store`.

---

## Task 5 — CEL evaluator + streaming operators (phase 1: duration, rate, percentile)

**Files:**
- Create: `pkg/checks/cel.go`
- Create: `pkg/checks/operators.go`
- Test: `pkg/checks/cel_test.go`
- Test: `pkg/checks/operators_test.go`

**Step 1** — Context7 `resolve-library-id github.com/google/cel-go` and `query-docs` for custom function registration API. Note findings at top of `operators.go` as doc comment.

**Step 2** — Test: `TestDurationLiteral` — CEL expression `duration('200ms') < duration('1s')` evaluates to `true`.

**Step 3** — `cel.go`: `type Evaluator struct { env *cel.Env; store events.Store }`. Constructor takes store. Method `Evaluate(expr string) (any, error)` compiles, checks, evaluates.

**Step 4** — `operators.go`: register custom functions on the CEL env:
  - `duration(string) -> google.protobuf.Duration`
  - `rate(list) -> double` (pass-rate of bool list)
  - `percentile(list, int) -> double`
  - Store-bound: `stream(string) -> list<Event>` closures over the evaluator's store.

**Step 5** — Tests for each operator. Table-driven.

**Step 6** — Commit: `feat(checks): CEL evaluator with duration, rate, percentile`.

---

## Task 6 — Streaming operators (phase 2: latency, forall, exists, before)

**Files:**
- Modify: `pkg/checks/operators.go`
- Test: `pkg/checks/operators_test.go`

**Step 1** — Test: `TestLatency` — seed store with paired events on `orders` and `orders.ack` with known timestamps; `latency('orders', 'orders.ack')` returns slice of durations matched by key.

**Step 2** — Implement `latency(fromStream, toStream string) -> list<Duration>`. Match by event key; time diff; skip unmatched.

**Step 3** — Test `TestForallExists` — `forall e in stream('x'): e.amount > 0` on a store.

**Step 4** — Implement `forall` and `exists` as CEL macros or helper funcs that iterate.

**Step 5** — Test `TestBefore` — `a.ts before b.ts` with CEL `Timestamp` comparison.

**Step 6** — Implement `before` as CEL function over two `google.protobuf.Timestamp` values.

**Step 7** — Commit: `feat(checks): streaming operators — latency, forall, exists, before`.

---

## Task 7 — Check evaluator + result model

**Files:**
- Create: `pkg/checks/check.go`
- Create: `pkg/checks/result.go`
- Test: `pkg/checks/check_test.go`

**Step 1** — Test: `TestEvaluateAllChecks` — given a store + list of `Check` definitions, `EvaluateAll` returns `[]CheckResult` with pass/fail + observed value.

**Step 2** — `result.go`: `type CheckResult { Name, Severity, Expr string; Passed bool; Value any; Err error; At time.Time }`.

**Step 3** — `check.go`: iterate over `scenario.Checks`, compile each once, evaluate against store. In **run** mode, `window` is ignored.

**Step 4** — Commit: `feat(checks): evaluate all scenario checks`.

---

## Task 8 — Faker-style data generator

**Files:**
- Create: `pkg/data/generator.go`
- Create: `pkg/data/faker.go`
- Test: `pkg/data/generator_test.go`

**Step 1** — Test: `TestFakerBasics` — deterministic generation with seed: `uuid()`, `faker.person.id`, `faker.number.float(a,b)`. Same seed → same output.

**Step 2** — `faker.go`: thin wrapper over `go-faker/faker/v4` exposing named helpers; seeding.

**Step 3** — `generator.go`: `type Generator interface { Generate() (map[string]any, error) }`. `FakerGenerator` takes `Overrides map[string]string` where values are CEL-ish expressions (simple function calls for now — full CEL integration later).

**Step 4** — Test: `TestOverrides` — overrides applied on top of defaults.

**Step 5** — Commit: `feat(data): faker-style generator with overrides`.

---

## Task 9 — Kafka connector (produce/consume with SASL/PLAIN + SCRAM)

**Files:**
- Create: `pkg/kafka/client.go`
- Create: `pkg/kafka/auth.go`
- Test: `pkg/kafka/client_test.go` (integration, tagged `//go:build integration`)

**Step 1** — Context7 for `github.com/twmb/franz-go` — resolve current API for producer/consumer, SASL config.

**Step 2** — `auth.go`: `func buildSASL(auth scenario.Auth) ([]kgo.Opt, error)` — handle `sasl_plain`, `sasl_scram_sha_256`, `sasl_scram_sha_512`, `mtls`. Return slice of `kgo.Opt`.

**Step 3** — `client.go`: `type Client struct { c *kgo.Client }`. Constructor from `scenario.KafkaConnector`. Methods `Produce(ctx, topic, key, value, headers)` and `Consume(ctx, topic, group, onRecord func(Record))`. `Close()`.

**Step 4** — Integration test (Testcontainers Kafka): produce 10 records with SASL_PLAIN, consume them, assert receipt. Skip if Docker unavailable (log warning).

**Step 5** — Commit: `feat(kafka): produce + consume with SASL_PLAIN/SCRAM and mTLS`.

---

## Task 10 — HTTP connector

**Files:**
- Create: `pkg/http/client.go`
- Test: `pkg/http/client_test.go`

**Step 1** — Test: `TestHTTPGetExpectStatus` — spin up `httptest.Server`, call `Do(GET, /foo)`, assert response captured in struct with `.Status`, `.Body`, `.Headers`.

**Step 2** — `client.go`: `type Client struct { base string; httpc *http.Client; auth scenario.HTTPAuth }`. Method `Do(step scenario.HTTPStep) (*Response, error)`.

**Step 3** — Test `TestHTTPExpect` — evaluate an `expect` block (status, body field equality). Return a `CheckResult`-compatible output.

**Step 4** — Commit: `feat(http): HTTP connector with expect block`.

---

## Task 11 — Orchestrator (run steps, feed store)

**Files:**
- Create: `pkg/orchestrator/run.go`
- Test: `pkg/orchestrator/run_test.go`

**Step 1** — Test: `TestOrchestratorRunsStepsSequentially` — fake Kafka + HTTP clients; pass a scenario with 2 produce steps + 1 HTTP; orchestrator calls them in order; events land in store.

**Step 2** — `run.go`: `type Runner struct { kafka *kafka.Client; httpc *http.Client; store events.Store; data data.Registry }`. `Run(ctx, s *scenario.Scenario) error`. For each step: dispatch on type; execute; record events into store.

**Step 3** — Test: produce step with `rate: 10/s, count: 20` runs ~2s and produces 20 records.

**Step 4** — Test: consume step waits up to `timeout` for a matching event.

**Step 5** — Commit: `feat(orchestrator): sequential step runner`.

---

## Task 12 — Failure injection (produce fail_rate, consumer lag)

**Files:**
- Modify: `pkg/orchestrator/run.go`
- Modify: `pkg/kafka/client.go`
- Test: `pkg/orchestrator/failure_test.go`

**Step 1** — Test: `TestProduceFailRate` — `fail_rate: 50%, fail_mode: schema_violation`; over 100 tries, ~50 events marked `direction=produced_failed` in store, ~50 succeed. Use fixed seed for determinism in test.

**Step 2** — Implement: deterministic PRNG on step seed; `fail_mode` variants (schema_violation produces payload that won't match subject schema; timeout skips send after deliberate delay).

**Step 3** — Test `TestConsumerSlowMode` — `pause_every: 5, pause_for: 10ms` produces observable cumulative lag.

**Step 4** — Implement in consume loop.

**Step 5** — Commit: `feat(orchestrator): producer fail_rate + consumer slow_mode`.

---

## Task 13 — Report format (JSON + console)

**Files:**
- Create: `pkg/report/report.go`
- Create: `pkg/report/json.go`
- Create: `pkg/report/console.go`
- Test: `pkg/report/report_test.go`

**Step 1** — Test: `TestJSONReport` — build a `Report` from `CheckResult`s + metadata, serialize to JSON, round-trip OK.

**Step 2** — `report.go`: `type Report { ScenarioName, RunID string; StartedAt, FinishedAt time.Time; Checks []checks.CheckResult; Status string; ExitCode int }`.

**Step 3** — `json.go`: `WriteJSON(w io.Writer, r *Report) error`. `console.go`: human-readable summary with colors.

**Step 4** — Commit: `feat(report): JSON and console report writers`.

---

## Task 14 — CLI `edt run` subcommand

**Files:**
- Create: `cmd/edt/run.go`
- Modify: `cmd/edt/main.go`
- Test: `cmd/edt/run_test.go` (integration)

**Step 1** — Test: `TestEdtRunAgainstTestcontainers` — spin up Kafka + httptest.Server, run `edt run --file testdata/scenario.yaml`, assert exit code 0 and JSON report on stdout contains expected check result.

**Step 2** — `run.go` wires: parser → orchestrator → check evaluator → report writer. Flags: `--file`, `--format json|console`, `--bootstrap-servers` (override), `--timeout`.

**Step 3** — Handle SIGINT gracefully (cancel ctx, flush report).

**Step 4** — Exit code: 0 all-pass, 1 any critical check fail, 2 scenario error.

**Step 5** — Commit: `feat(cli): edt run end-to-end`.

---

## Task 15 — Dockerfile + GoReleaser

**Files:**
- Create: `Dockerfile`
- Create: `.goreleaser.yaml`
- Modify: `.github/workflows/release.yml`

**Step 1** — `Dockerfile`: distroless static base, single binary `edt`. Multi-stage build with Go builder.

**Step 2** — `.goreleaser.yaml`: builds for `darwin-{amd64,arm64}`, `linux-{amd64,arm64}`, `windows-amd64`. Sign with cosign. Archive format: tar.gz (zip for windows). Homebrew tap formula output.

**Step 3** — `release.yml` workflow: on tag `v*`, runs `goreleaser release --clean`. Uses `GITHUB_TOKEN`.

**Step 4** — Commit: `build: Dockerfile + GoReleaser multi-arch release pipeline`.

---

## Out of M1 (deferred)

- Control plane (`edt serve`) — M2.
- Worker mode (`edt worker`) — M2.
- Schema Registry + Avro/Proto/JSON Schema — M3.
- SASL OAUTHBEARER + AWS IAM — M3.
- `edt eval` (agent-under-test) + LLM-as-judge — M5.
- MCP server — M5.
- Helm chart — M6.
- TS SDK — M4.
- gRPC, WebSocket, SSE connectors — M4-M6.

## Acceptance — M1 is done when

1. `go test ./... -race` passes on a clean machine.
2. `edt validate --file examples/order-flow.yaml` exits 0 on a valid scenario.
3. `edt run --file examples/order-flow.yaml --bootstrap-servers <kafka>` executes against a real Kafka cluster, produces+consumes, evaluates checks, prints a report, exits 0 or 1 appropriately.
4. `goreleaser --snapshot --clean` produces signed multi-arch binaries and a Docker image locally.
5. Every commit is atomic per task above; no commit breaks `go test ./...`.

---

## Execution mode

User requested autonomous execution (no further questions, session proceeding without them). Execution proceeds serially task-by-task in this session. Each task: tests first, minimal implementation, run tests, commit.

Before touching any library, resolve via Context7 MCP per project policy.
