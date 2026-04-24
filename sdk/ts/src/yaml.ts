// YAML emitter for Scenario objects.
//
// Produces canonical YAML matching what pkg/scenario expects. The emitter is
// deliberately thin: schema validation happens in the Go engine (edt validate).
// What this layer guarantees: stable field ordering, undefined stripped, and
// CEL/rubric blocks rendered as block scalars for diff-friendly output.

import { stringify as yamlStringify, parse as yamlParse, Document } from "yaml";
import type { Scenario } from "./types.js";
import { API_VERSION, KIND_SCENARIO } from "./types.js";

export interface EmitOptions {
  /** Override header. Defaults omit it. */
  header?: string;
}

const KEY_ORDER: Record<string, string[]> = {
  scenario: ["apiVersion", "kind", "metadata", "spec"],
  metadata: ["name", "labels"],
  spec: [
    "connectors",
    "data",
    "steps",
    "checks",
    "agent_under_test",
    "evals",
  ],
  connectors: ["kafka", "http"],
  kafka: ["bootstrap_servers", "auth", "schema_registry"],
  http: ["base_url", "auth"],
  schema_registry: [
    "url",
    "flavor",
    "base_path",
    "username",
    "password",
    "bearer_token",
  ],
  step: ["name", "produce", "consume", "http", "sleep"],
  produce: [
    "topic",
    "key",
    "payload",
    "rate",
    "count",
    "schema_subject",
    "fail_rate",
    "fail_mode",
  ],
  consume: ["topic", "group", "timeout", "match", "slow_mode"],
  httpStep: [
    "method",
    "path",
    "headers",
    "body",
    "expect",
    "fail_rate",
    "fail_mode",
  ],
  check: ["name", "expr", "window", "severity"],
  evaluation: ["name", "expr", "judge", "threshold", "severity"],
  judge: ["model", "rubric", "rubric_version"],
};

/** Emits a Scenario as canonical YAML. */
export function emit(scenario: Scenario, opts: EmitOptions = {}): string {
  assertValidScenario(scenario);
  const normalized = normalize(scenario);
  const body = yamlStringify(normalized, {
    indent: 2,
    lineWidth: 0, // never wrap
    defaultStringType: "PLAIN",
    defaultKeyType: "PLAIN",
    blockQuote: "literal",
  });
  return opts.header ? `${opts.header}\n${body}` : body;
}

/** Parses canonical YAML back into a Scenario (for round-trip tests). */
export function parse(text: string): Scenario {
  const v = yamlParse(text);
  if (!v || typeof v !== "object") {
    throw new Error("parse: empty or non-object YAML");
  }
  return v as Scenario;
}

function assertValidScenario(s: Scenario): void {
  if (s.apiVersion !== API_VERSION) {
    throw new Error(`emit: apiVersion must be "${API_VERSION}", got "${s.apiVersion}"`);
  }
  if (s.kind !== KIND_SCENARIO) {
    throw new Error(`emit: kind must be "${KIND_SCENARIO}", got "${s.kind}"`);
  }
  if (!s.metadata?.name) {
    throw new Error("emit: metadata.name is required");
  }
  if (!Array.isArray(s.spec?.steps) || s.spec.steps.length === 0) {
    throw new Error("emit: spec.steps must contain at least one step");
  }
}

// normalize walks the object and:
//   1. drops undefined keys (yaml lib preserves them as null otherwise)
//   2. reorders keys so `spec` comes after `metadata` etc.
function normalize(value: unknown, orderKey = "scenario"): unknown {
  if (Array.isArray(value)) {
    const childOrder = arrayChildOrder(orderKey);
    return value.map((v) => normalize(v, childOrder));
  }
  if (value === null || typeof value !== "object") {
    return value;
  }
  const order = KEY_ORDER[orderKey] ?? [];
  const src = value as Record<string, unknown>;
  const keys = Object.keys(src);
  const ordered: Record<string, unknown> = {};
  for (const k of order) {
    if (k in src && src[k] !== undefined) {
      ordered[k] = normalize(src[k], childOrderFor(orderKey, k));
    }
  }
  for (const k of keys) {
    if (k in ordered || src[k] === undefined) continue;
    ordered[k] = normalize(src[k], childOrderFor(orderKey, k));
  }
  return ordered;
}

function childOrderFor(parent: string, key: string): string {
  switch (parent) {
    case "scenario":
      if (key === "metadata") return "metadata";
      if (key === "spec") return "spec";
      return "";
    case "spec":
      if (key === "connectors") return "connectors";
      if (key === "steps") return "step";
      if (key === "checks") return "check";
      if (key === "evals") return "evaluation";
      return "";
    case "connectors":
      if (key === "kafka") return "kafka";
      if (key === "http") return "http";
      return "";
    case "kafka":
      if (key === "schema_registry") return "schema_registry";
      return "";
    case "step":
      if (key === "produce") return "produce";
      if (key === "consume") return "consume";
      if (key === "http") return "httpStep";
      return "";
    case "evaluation":
      if (key === "judge") return "judge";
      return "";
    default:
      return "";
  }
}

function arrayChildOrder(orderKey: string): string {
  // When we descend into an array, items typically share the parent's child order key.
  // e.g. spec.steps is ordered via "step" at the caller site.
  return orderKey;
}

/** Re-exported so callers can embed scenarios inside larger YAML pipelines. */
export { Document };
