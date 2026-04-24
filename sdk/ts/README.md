# @event-driven-tests-ai/sdk

TypeScript SDK for authoring [edt](https://github.com/sderosiaux/event-driven-tests-ai) scenarios.

The Go engine is the source of truth. This SDK emits canonical YAML the engine already understands — no parallel runtime, no drift. If you prefer YAML, write YAML. If you want types, autocomplete, and the ability to pull topic names from your Kafka codebase, write TypeScript.

## Install

```bash
npm install --save-dev @event-driven-tests-ai/sdk
```

## Example

```ts
import { emit, type Scenario } from "@event-driven-tests-ai/sdk";

const scenario: Scenario = {
  apiVersion: "edt.io/v1",
  kind: "Scenario",
  metadata: { name: "order-flow-e2e" },
  spec: {
    connectors: {
      kafka: { bootstrap_servers: "localhost:9092" },
    },
    steps: [
      {
        name: "place-order",
        produce: { topic: "orders", payload: "${data.orders}", count: 100 },
      },
    ],
    checks: [
      {
        name: "order_ack_p99",
        expr: "percentile(latency('orders', 'orders.ack'), 99) < duration('200ms')",
        severity: "critical",
      },
    ],
  },
};

console.log(emit(scenario));
```

Pipe the output into `edt run -f -` or write it to disk and commit it.

## What's in this package

- `Scenario` types mirroring the Go data model (`pkg/scenario/types.go`)
- `emit(scenario)` — canonical YAML serializer with stable field ordering
- `parse(yaml)` — load YAML back into typed objects (round-trip tests)

A camelCase fluent builder (`scenario().kafka(...).produce(...)`) lands in the next release.

## Status

Early — API may still change. Pin exact versions until 0.2.x.

## License

Apache-2.0
