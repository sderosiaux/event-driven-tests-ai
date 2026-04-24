import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  emit,
  parse,
  type Scenario,
  API_VERSION,
  KIND_SCENARIO,
} from "../src/index.js";

const fixture = (name: string): string =>
  readFileSync(resolve(__dirname, "../testdata", name), "utf8");

describe("emit", () => {
  it("rejects a scenario without steps", () => {
    const bad: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "x" },
      spec: { connectors: {}, steps: [] },
    };
    expect(() => emit(bad)).toThrow(/at least one step/);
  });

  it("rejects a scenario without metadata.name", () => {
    const bad = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: {} as { name: string },
      spec: {
        connectors: {},
        steps: [{ name: "p", sleep: "1s" }],
      },
    } satisfies Scenario;
    expect(() => emit(bad)).toThrow(/metadata\.name/);
  });

  it("rejects wrong apiVersion", () => {
    const bad = {
      apiVersion: "wrong" as typeof API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "x" },
      spec: { connectors: {}, steps: [{ name: "p", sleep: "1s" }] },
    } satisfies Scenario;
    expect(() => emit(bad)).toThrow(/apiVersion/);
  });

  it("emits apiVersion/kind/metadata/spec in canonical order", () => {
    const s: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      // Deliberately build with out-of-order keys.
      spec: {
        steps: [{ name: "p", sleep: "10ms" }],
        connectors: { kafka: { bootstrap_servers: "b:9092" } },
      } as Scenario["spec"],
      metadata: { name: "demo" },
    };
    const out = emit(s);
    const keys = out.split("\n").filter((l) => /^[a-zA-Z]/.test(l)).map((l) => l.split(":")[0]);
    expect(keys.slice(0, 4)).toEqual(["apiVersion", "kind", "metadata", "spec"]);
  });

  it("drops undefined optional fields", () => {
    const s: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "demo", labels: undefined },
      spec: {
        connectors: { kafka: { bootstrap_servers: "b:9092", auth: undefined } },
        steps: [{ name: "p", sleep: "10ms" }],
        checks: undefined,
      },
    };
    const out = emit(s);
    expect(out).not.toContain("labels");
    expect(out).not.toContain("auth");
    expect(out).not.toContain("checks");
    expect(out).not.toContain("null");
  });

  it("round-trips the full fixture (parse → emit → parse equals parse)", () => {
    const original = parse(fixture("full.yaml"));
    const emitted = emit(original);
    const reparsed = parse(emitted);
    expect(reparsed).toEqual(original);
  });

  it("round-trip preserves CEL expressions verbatim", () => {
    const s: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "cel-demo" },
      spec: {
        connectors: { kafka: { bootstrap_servers: "b:9092" } },
        steps: [{ name: "p", sleep: "10ms" }],
        checks: [
          {
            name: "no-orphans",
            expr: "forall e in stream('c'): exists o in stream('o') where o.id == e.id && o.ts.before(e.ts)",
            severity: "warning",
          },
        ],
      },
    };
    const round = parse(emit(s));
    expect(round.spec.checks?.[0]?.expr).toBe(s.spec.checks?.[0]?.expr);
  });

  it("emits steps with consume slow_mode and match rules", () => {
    const s: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "c" },
      spec: {
        connectors: { kafka: { bootstrap_servers: "b:9092" } },
        steps: [
          {
            name: "wait",
            consume: {
              topic: "acks",
              group: "g",
              timeout: "5s",
              match: [{ key: "payload.id == previous.id" }],
              slow_mode: { pause_every: 100, pause_for: "500ms" },
            },
          },
        ],
      },
    };
    const out = emit(s);
    expect(out).toMatch(/slow_mode:\s*\n\s+pause_every: 100/);
    expect(out).toContain("payload.id == previous.id");
  });

  it("emits eval blocks with judge + threshold", () => {
    const s: Scenario = {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: { name: "eval-demo" },
      spec: {
        connectors: { http: { base_url: "http://a" } },
        steps: [{ name: "p", sleep: "1s" }],
        agent_under_test: { name: "agent", produces: ["replies"] },
        evals: [
          {
            name: "polite",
            judge: { model: "claude-opus-4-7", rubric: "Is it polite?" },
            threshold: { aggregate: "avg", value: ">= 4.2", over: "50 runs" },
            severity: "critical",
          },
        ],
      },
    };
    const round = parse(emit(s));
    expect(round.spec.evals?.[0]?.judge?.model).toBe("claude-opus-4-7");
    expect(round.spec.evals?.[0]?.threshold?.over).toBe("50 runs");
    expect(round.spec.agent_under_test?.produces).toEqual(["replies"]);
  });
});
