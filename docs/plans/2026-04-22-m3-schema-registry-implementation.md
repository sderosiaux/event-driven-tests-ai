# event-driven-tests-ai — M3 Schema Registry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: superpowers:executing-plans to implement this plan task-by-task.

**Goal:** After M3, edt scenarios can use Schema Registry-backed payloads:
- Producers serialize Avro / Protobuf / JSON Schema-validated payloads with the
  Confluent wire format (magic byte + schema id + body).
- Consumers deserialize the same wire format so CEL checks see typed Go values
  (`payload.orderId == previous.orderId` works without manual JSON munging).
- Both Confluent SR and Apicurio SR (in CC compat mode and in native mode) are
  supported via a thin Registry interface.

**Architecture:** A new `pkg/schemaregistry` package owns the SR HTTP client + a
`Codec` interface (encode/decode). The orchestrator's produce/consume path
checks for `connectors.kafka.schema_registry`; when present, payloads flow
through the codec instead of raw JSON marshal.

**Tech Stack:**
- Avro: `github.com/hamba/avro/v2` (pure Go, fast).
- Protobuf: `google.golang.org/protobuf` (already a transitive dep of cel-go).
- JSON Schema: existing `github.com/santhosh-tekuri/jsonschema/v6` (already used).

---

## Task M3-T1 — Schema Registry HTTP client

**Files:**
- Create: `pkg/schemaregistry/client.go`
- Create: `pkg/schemaregistry/client_test.go`

`Client.GetSchemaByID(int)`, `RegisterSchema(subject, schema, type) (id int, error)`,
`GetLatestVersion(subject)`. CC + Apicurio compat-mode share the same wire surface.

**Commit:** `feat(schemaregistry): HTTP client for Confluent + Apicurio compat mode`.

## Task M3-T2 — Codec interface + Avro impl

**Files:**
- Create: `pkg/schemaregistry/codec.go`
- Create: `pkg/schemaregistry/avro.go`
- Create: `pkg/schemaregistry/avro_test.go`

`Codec.Encode(value any, subject string) ([]byte, error)` and `Decode(data []byte) (any, error)`.
Wire format: `[0x00, schemaID(4 BE bytes), payload]`.

**Commit:** `feat(schemaregistry): Codec interface + Avro encode/decode`.

## Task M3-T3 — Protobuf codec

**Files:**
- Create: `pkg/schemaregistry/proto.go`
- Create: `pkg/schemaregistry/proto_test.go`

For M3, only descriptor-set-driven dynamic messages (no compile-time stubs).

**Commit:** `feat(schemaregistry): Protobuf codec`.

## Task M3-T4 — JSON Schema codec

**Files:**
- Create: `pkg/schemaregistry/jsonschema.go`
- Create: `pkg/schemaregistry/jsonschema_test.go`

Validates payload against schema before encoding. Decoder is plain JSON.

**Commit:** `feat(schemaregistry): JSON Schema codec`.

## Task M3-T5 — Wire SR into orchestrator produce/consume

**Files:**
- Modify: `pkg/orchestrator/run.go`
- Modify: `pkg/scenario/types.go` (`ProduceStep.SchemaSubject` if not already there)
- Create: `pkg/orchestrator/sr_test.go`

When `connectors.kafka.schema_registry` is set:
- Produce: serialize with the codec for the matching subject
- Consume: deserialize the wire-format header, hand the typed payload to events.Store

**Commit:** `feat(orchestrator): SR-aware produce/consume`.

## Task M3-T6 — SASL OAUTHBEARER (OIDC client_credentials flow)

**Files:**
- Modify: `pkg/kafka/auth.go`
- Create: `pkg/kafka/oauth_test.go`

Use `franz-go/pkg/sasl/oauth`. Token URL + client_id + secret + scopes.

**Commit:** `feat(kafka): SASL OAUTHBEARER auth (OIDC client_credentials)`.

## Task M3-T7 — AWS IAM (MSK)

**Files:**
- Modify: `pkg/kafka/auth.go`
- Add dep: `github.com/twmb/franz-go/pkg/sasl/aws`

**Commit:** `feat(kafka): AWS IAM auth for MSK`.

## Acceptance — M3 done when

1. `go test ./... -race -count=1` green.
2. A scenario with `connectors.kafka.schema_registry` produces Avro-encoded
   records that round-trip through a real Confluent SR (integration test).
3. SASL OAUTHBEARER + AWS IAM auth no longer surface "M3, not available" errors;
   auth_test covers all four mechanisms.
