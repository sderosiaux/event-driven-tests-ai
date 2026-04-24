// Fluent builder layer.
//
// The Scenario types use snake_case to match the YAML wire format. This layer
// exposes a camelCase JS-idiomatic API (`slowMode`, `pauseEvery`, ...) and
// translates to snake_case at build() time. Every builder returns `this` so
// authoring flows read top-to-bottom.

import { emit } from "./yaml.js";
import {
  API_VERSION,
  KIND_SCENARIO,
  type AgentUnderTest,
  type Check,
  type ConsumeStep,
  type Data,
  type Eval,
  type FailMode,
  type HTTPAuth,
  type HTTPConnector,
  type HTTPExpect,
  type HTTPStep,
  type Judge,
  type KafkaAuth,
  type KafkaConnector,
  type MatchRule,
  type Percentage,
  type ProduceStep,
  type Scenario,
  type SchemaRegistryConfig,
  type Severity,
  type SlowMode,
  type SSEStep,
  type Spec,
  type Step,
  type WebSocketConnector,
  type WebSocketStep,
} from "./types.js";

// --- camelCase input shapes (user-facing) ----------------------------------

export interface KafkaOptions {
  auth?: KafkaAuth;
  schemaRegistry?: SchemaRegistryConfig;
}

export interface HTTPOptions {
  auth?: HTTPAuth;
}

export interface DataOptions {
  schema?: Data["schema"];
  generator: Data["generator"];
}

export interface ProduceOptions {
  topic: string;
  payload: string;
  key?: string;
  rate?: string;
  count?: number;
  failRate?: Percentage;
  failMode?: FailMode;
  schemaSubject?: string;
}

export interface ConsumeOptions {
  topic: string;
  group?: string;
  timeout?: string;
  match?: Array<string | MatchRule>;
  slowMode?: { pauseEvery: number; pauseFor: string };
}

export interface HTTPStepOptions {
  method: string;
  path: string;
  headers?: Record<string, string>;
  body?: string;
  expect?: HTTPExpect;
  failRate?: Percentage;
  failMode?: FailMode;
}

export interface WebSocketOptions {
  auth?: HTTPAuth;
}

export interface WebSocketStepOptions {
  path: string;
  send?: string;
  count?: number;
  timeout?: string;
  match?: Array<string | MatchRule>;
  slowMode?: { pauseEvery: number; pauseFor: string };
}

export interface SSEStepOptions {
  path: string;
  count?: number;
  timeout?: string;
  match?: Array<string | MatchRule>;
  slowMode?: { pauseEvery: number; pauseFor: string };
}

export interface CheckOptions {
  window?: string;
  severity?: Severity;
}

export interface EvalOptions {
  expr?: string;
  judge?: Judge;
  threshold?: { aggregate?: Eval["threshold"] extends infer T ? T extends { aggregate?: infer A } ? A : never : never; value: string; over?: string };
  severity?: Severity;
}

// --- Builder ---------------------------------------------------------------

export class ScenarioBuilder {
  private readonly name: string;
  private labels: Record<string, string> = {};
  private kafkaConn?: KafkaConnector;
  private httpConn?: HTTPConnector;
  private wsConn?: WebSocketConnector;
  private dataDefs: Record<string, Data> = {};
  private steps: Step[] = [];
  private checks: Check[] = [];
  private agent?: AgentUnderTest;
  private evalsList: Eval[] = [];

  constructor(name: string) {
    if (!name) throw new Error("scenario: name is required");
    this.name = name;
  }

  label(key: string, value: string): this {
    this.labels[key] = value;
    return this;
  }

  kafka(bootstrapServers: string, opts: KafkaOptions = {}): this {
    this.kafkaConn = {
      bootstrap_servers: bootstrapServers,
      auth: opts.auth,
      schema_registry: opts.schemaRegistry,
    };
    return this;
  }

  http(baseURL: string, opts: HTTPOptions = {}): this {
    this.httpConn = { base_url: baseURL, auth: opts.auth };
    return this;
  }

  websocket(baseURL: string, opts: WebSocketOptions = {}): this {
    this.wsConn = { base_url: baseURL, auth: opts.auth };
    return this;
  }

  data(key: string, opts: DataOptions): this {
    this.dataDefs[key] = { schema: opts.schema, generator: opts.generator };
    return this;
  }

  produce(stepName: string, opts: ProduceOptions): this {
    const produce: ProduceStep = {
      topic: opts.topic,
      payload: opts.payload,
      key: opts.key,
      rate: opts.rate,
      count: opts.count,
      fail_rate: opts.failRate,
      fail_mode: opts.failMode,
      schema_subject: opts.schemaSubject,
    };
    this.steps.push({ name: stepName, produce });
    return this;
  }

  consume(stepName: string, opts: ConsumeOptions): this {
    const match: MatchRule[] | undefined = opts.match?.map((m) =>
      typeof m === "string" ? { key: m } : m,
    );
    const slow: SlowMode | undefined = opts.slowMode && {
      pause_every: opts.slowMode.pauseEvery,
      pause_for: opts.slowMode.pauseFor,
    };
    const consume: ConsumeStep = {
      topic: opts.topic,
      group: opts.group,
      timeout: opts.timeout,
      match,
      slow_mode: slow,
    };
    this.steps.push({ name: stepName, consume });
    return this;
  }

  httpStep(stepName: string, opts: HTTPStepOptions): this {
    const http: HTTPStep = {
      method: opts.method,
      path: opts.path,
      headers: opts.headers,
      body: opts.body,
      expect: opts.expect,
      fail_rate: opts.failRate,
      fail_mode: opts.failMode,
    };
    this.steps.push({ name: stepName, http });
    return this;
  }

  wsStep(stepName: string, opts: WebSocketStepOptions): this {
    const match: MatchRule[] | undefined = opts.match?.map((m) =>
      typeof m === "string" ? { key: m } : m,
    );
    const slow: SlowMode | undefined = opts.slowMode && {
      pause_every: opts.slowMode.pauseEvery,
      pause_for: opts.slowMode.pauseFor,
    };
    const websocket: WebSocketStep = {
      path: opts.path,
      send: opts.send,
      count: opts.count,
      timeout: opts.timeout,
      match,
      slow_mode: slow,
    };
    this.steps.push({ name: stepName, websocket });
    return this;
  }

  sseStep(stepName: string, opts: SSEStepOptions): this {
    const match: MatchRule[] | undefined = opts.match?.map((m) =>
      typeof m === "string" ? { key: m } : m,
    );
    const slow: SlowMode | undefined = opts.slowMode && {
      pause_every: opts.slowMode.pauseEvery,
      pause_for: opts.slowMode.pauseFor,
    };
    const sse: SSEStep = {
      path: opts.path,
      count: opts.count,
      timeout: opts.timeout,
      match,
      slow_mode: slow,
    };
    this.steps.push({ name: stepName, sse });
    return this;
  }

  sleep(stepName: string, duration: string): this {
    this.steps.push({ name: stepName, sleep: duration });
    return this;
  }

  check(name: string, expr: string, opts: CheckOptions = {}): this {
    this.checks.push({ name, expr, window: opts.window, severity: opts.severity });
    return this;
  }

  agentUnderTest(a: AgentUnderTest): this {
    this.agent = a;
    return this;
  }

  eval(name: string, opts: EvalOptions): this {
    this.evalsList.push({
      name,
      expr: opts.expr,
      judge: opts.judge,
      threshold: opts.threshold && {
        aggregate: opts.threshold.aggregate,
        value: opts.threshold.value,
        over: opts.threshold.over,
      },
      severity: opts.severity,
    });
    return this;
  }

  build(): Scenario {
    const spec: Spec = {
      connectors: {
        kafka: this.kafkaConn,
        http: this.httpConn,
        websocket: this.wsConn,
      },
      data: Object.keys(this.dataDefs).length ? this.dataDefs : undefined,
      steps: this.steps,
      checks: this.checks.length ? this.checks : undefined,
      agent_under_test: this.agent,
      evals: this.evalsList.length ? this.evalsList : undefined,
    };
    return {
      apiVersion: API_VERSION,
      kind: KIND_SCENARIO,
      metadata: {
        name: this.name,
        labels: Object.keys(this.labels).length ? this.labels : undefined,
      },
      spec,
    };
  }

  toYAML(): string {
    return emit(this.build());
  }
}

/** Entry point: `scenario("my-test").kafka(...).produce(...).build()`. */
export function scenario(name: string): ScenarioBuilder {
  return new ScenarioBuilder(name);
}
