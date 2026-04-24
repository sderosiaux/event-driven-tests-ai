import { describe, expect, it } from "vitest";
import { scenario, parse } from "../src/index.js";

describe("scenario builder", () => {
  it("produces the minimum valid scenario", () => {
    const s = scenario("min")
      .kafka("localhost:9092")
      .sleep("pause", "100ms")
      .build();
    expect(s.apiVersion).toBe("edt.io/v1");
    expect(s.metadata.name).toBe("min");
    expect(s.spec.connectors.kafka?.bootstrap_servers).toBe("localhost:9092");
    expect(s.spec.steps).toHaveLength(1);
  });

  it("maps camelCase fluent API to snake_case wire format", () => {
    const s = scenario("mapping")
      .kafka("b:9092")
      .consume("wait", {
        topic: "acks",
        timeout: "5s",
        match: ["payload.id == previous.id"],
        slowMode: { pauseEvery: 100, pauseFor: "500ms" },
      })
      .produce("go", {
        topic: "orders",
        payload: "${data.x}",
        failRate: 0.02,
        failMode: "schema_violation",
        schemaSubject: "orders-value",
      })
      .build();

    const consume = s.spec.steps[0]?.consume;
    expect(consume?.slow_mode).toEqual({ pause_every: 100, pause_for: "500ms" });
    expect(consume?.match).toEqual([{ key: "payload.id == previous.id" }]);

    const produce = s.spec.steps[1]?.produce;
    expect(produce?.fail_rate).toBe(0.02);
    expect(produce?.fail_mode).toBe("schema_violation");
    expect(produce?.schema_subject).toBe("orders-value");
  });

  it("builder.toYAML() round-trips through parse()", () => {
    const yaml = scenario("e2e")
      .label("team", "commerce")
      .kafka("b:9092", { auth: { type: "sasl_plain", username: "a", password: "b" } })
      .http("https://api.example.com", { auth: { type: "bearer", token: "t" } })
      .data("orders", {
        schema: { source: "schema_registry", subject: "orders-value" },
        generator: { strategy: "faker", seed: 42 },
      })
      .produce("p", { topic: "orders", payload: "${data.orders}", count: 10 })
      .check("c", "size(stream('orders')) >= 1", { severity: "critical" })
      .toYAML();

    const parsed = parse(yaml);
    expect(parsed.metadata.labels).toEqual({ team: "commerce" });
    expect(parsed.spec.connectors.kafka?.auth?.username).toBe("a");
    expect(parsed.spec.connectors.http?.auth?.token).toBe("t");
    expect(parsed.spec.data?.orders?.generator.seed).toBe(42);
    expect(parsed.spec.checks?.[0]?.severity).toBe("critical");
  });

  it("omits empty checks / data / evals from the emitted spec", () => {
    const y = scenario("tiny").kafka("b:9092").sleep("s", "10ms").toYAML();
    expect(y).not.toMatch(/^\s+checks:/m);
    expect(y).not.toMatch(/^\s+data:/m);
    expect(y).not.toMatch(/^\s+evals:/m);
  });

  it("eval() accepts judge + threshold with camelCase aggregate", () => {
    const s = scenario("eval")
      .http("http://a")
      .sleep("s", "1ms")
      .agentUnderTest({ name: "bot", produces: ["replies"] })
      .eval("polite", {
        judge: { model: "claude-opus-4-7", rubric: "Be polite." },
        threshold: { aggregate: "avg", value: ">= 4.2", over: "50 runs" },
        severity: "critical",
      })
      .build();

    expect(s.spec.evals?.[0]?.threshold?.aggregate).toBe("avg");
    expect(s.spec.agent_under_test?.produces).toEqual(["replies"]);
  });

  it("rejects empty scenario name", () => {
    expect(() => scenario("")).toThrow(/name is required/);
  });

  it("websocket + wsStep builders translate camelCase to wire YAML", () => {
    const s = scenario("ws-demo")
      .websocket("wss://api.example.com", { auth: { type: "bearer", token: "tok" } })
      .wsStep("watch", {
        path: "/orders/stream",
        send: `{"subscribe":"orders"}`,
        timeout: "5s",
        match: ["payload.orderId == 'abc'"],
        slowMode: { pauseEvery: 10, pauseFor: "200ms" },
      })
      .build();

    expect(s.spec.connectors.websocket?.base_url).toBe("wss://api.example.com");
    const ws = s.spec.steps[0]?.websocket;
    expect(ws?.path).toBe("/orders/stream");
    expect(ws?.slow_mode).toEqual({ pause_every: 10, pause_for: "200ms" });
    expect(ws?.match).toEqual([{ key: "payload.orderId == 'abc'" }]);
  });

  it("sseStep builder emits under connectors.http", () => {
    const s = scenario("sse-demo")
      .http("https://api.example.com")
      .sseStep("watch", {
        path: "/events",
        count: 5,
        timeout: "10s",
        match: ["payload.type == 'order.placed'"],
      })
      .build();

    const sse = s.spec.steps[0]?.sse;
    expect(sse?.path).toBe("/events");
    expect(sse?.count).toBe(5);
    expect(sse?.match).toEqual([{ key: "payload.type == 'order.placed'" }]);
    // SSE reuses the HTTP connector — there should be no websocket connector.
    expect(s.spec.connectors.websocket).toBeUndefined();
  });
});
