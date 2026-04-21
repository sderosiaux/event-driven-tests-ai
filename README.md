# event-driven-tests-ai

Event-driven testing engine for Kafka, HTTP, gRPC, WebSocket and SSE flows — with first-class support for testing AI agents sitting in the flow.

Three modes, one scenario DSL:

- `edt run` — one-shot scenario, CI-friendly, pass/fail exit code.
- `edt watch` — infinite synthetic probe, continuous SLO evaluation (v2).
- `edt eval` — agent-under-test harness with LLM-as-judge scoring (v2).

Status: pre-alpha. M1 in progress. See `docs/plans/` for the design spec and implementation plan.

License: Apache 2.0.
