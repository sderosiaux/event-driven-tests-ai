// Scenario type definitions — 1:1 mirror of pkg/scenario/types.go.
//
// Field names match the YAML wire format (snake_case), so a Scenario object
// serialized through emit() round-trips identically with the Go parser.
// A camelCase fluent builder layer will ship in M4-T2.

export const API_VERSION = "edt.io/v1";
export const KIND_SCENARIO = "Scenario";

export interface Scenario {
  apiVersion: typeof API_VERSION;
  kind: typeof KIND_SCENARIO;
  metadata: Metadata;
  spec: Spec;
}

export interface Metadata {
  name: string;
  labels?: Record<string, string>;
}

export interface Spec {
  connectors: Connectors;
  data?: Record<string, Data>;
  steps: Step[];
  checks?: Check[];
  agent_under_test?: AgentUnderTest;
  evals?: Eval[];
}

// ---- Connectors ------------------------------------------------------------

export interface Connectors {
  kafka?: KafkaConnector;
  http?: HTTPConnector;
  websocket?: WebSocketConnector;
}

export interface WebSocketConnector {
  base_url: string;
  auth?: HTTPAuth;
}

export interface KafkaConnector {
  bootstrap_servers: string;
  auth?: KafkaAuth;
  schema_registry?: SchemaRegistryConfig;
}

export type KafkaAuthType =
  | "sasl_plain"
  | "sasl_scram_sha_256"
  | "sasl_scram_sha_512"
  | "mtls"
  | "sasl_oauthbearer"
  | "aws_iam";

export interface KafkaAuth {
  type: KafkaAuthType;
  username?: string;
  password?: string;
  ca_file?: string;
  cert_file?: string;
  key_file?: string;
  token_url?: string;
  client_id?: string;
  scopes?: string[];
  region?: string;
}

export interface SchemaRegistryConfig {
  url: string;
  flavor?: "confluent" | "apicurio";
  base_path?: string;
  username?: string;
  password?: string;
  bearer_token?: string;
}

export interface HTTPConnector {
  base_url: string;
  auth?: HTTPAuth;
}

export interface HTTPAuth {
  type: "bearer" | "basic" | "none";
  token?: string;
  user?: string;
  pass?: string;
}

// ---- Data ------------------------------------------------------------------

export interface Data {
  schema?: DataSchema;
  generator: Generator;
}

export interface DataSchema {
  source: "schema_registry" | "inline" | "file";
  subject?: string;
  inline?: string;
  file?: string;
}

export interface Generator {
  strategy: "faker";
  seed?: number;
  overrides?: Record<string, string>;
}

// ---- Steps -----------------------------------------------------------------

export interface Step {
  name: string;
  produce?: ProduceStep;
  consume?: ConsumeStep;
  http?: HTTPStep;
  websocket?: WebSocketStep;
  sse?: SSEStep;
  sleep?: string;
}

export interface WebSocketStep {
  path: string;
  send?: string;
  count?: number;
  timeout?: string;
  match?: MatchRule[];
  slow_mode?: SlowMode;
}

export interface SSEStep {
  path: string;
  count?: number;
  timeout?: string;
  match?: MatchRule[];
  slow_mode?: SlowMode;
}

export type FailMode =
  | "schema_violation"
  | "timeout"
  | "broker_not_available"
  | "http_5xx";

/** Percentage accepts a fraction (0..1) or a string like "10%". */
export type Percentage = number | string;

export interface ProduceStep {
  topic: string;
  payload: string;
  key?: string;
  rate?: string;
  count?: number;
  fail_rate?: Percentage;
  fail_mode?: FailMode;
  schema_subject?: string;
}

export interface ConsumeStep {
  topic: string;
  group?: string;
  timeout?: string;
  match?: MatchRule[];
  slow_mode?: SlowMode;
}

export interface MatchRule {
  key?: string;
}

export interface SlowMode {
  pause_every: number;
  pause_for: string;
}

export interface HTTPStep {
  method: string;
  path: string;
  headers?: Record<string, string>;
  body?: string;
  expect?: HTTPExpect;
  fail_rate?: Percentage;
  fail_mode?: FailMode;
}

export interface HTTPExpect {
  status?: number;
  headers?: Record<string, string>;
  body?: Record<string, unknown>;
}

// ---- Checks ----------------------------------------------------------------

export type Severity = "critical" | "warning" | "info";

export interface Check {
  name: string;
  expr: string;
  window?: string;
  severity?: Severity;
}

// ---- Evals (agent-under-test) ---------------------------------------------

export interface AgentUnderTest {
  name: string;
  consumes?: string[];
  produces?: string[];
  http_endpoints?: string[];
}

export interface Eval {
  name: string;
  expr?: string;
  judge?: Judge;
  threshold?: EvalThreshold;
  severity?: Severity;
}

export interface Judge {
  model: string;
  rubric: string;
  rubric_version?: string;
}

export interface EvalThreshold {
  aggregate?: "avg" | "p50" | "p95" | "min" | "max";
  value: string;
  over?: string;
}
