// Round-trip against the Go engine: emit YAML, run `edt validate`, expect 0.
//
// Requires the edt binary. The test auto-builds it on first run from the
// Go source tree at the repo root (cwd relative). Set EDT_BIN to skip the
// build and use a pre-built binary (CI path).

import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { execFileSync, execSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { emit, scenario, type Scenario, API_VERSION, KIND_SCENARIO } from "../src/index.js";

function resolveBinary(): string | null {
  const override = process.env.EDT_BIN;
  if (override && existsSync(override)) return override;

  // Build from source — repo root is three levels up from dist layout (sdk/ts/test).
  const repoRoot = resolve(__dirname, "..", "..", "..");
  if (!existsSync(join(repoRoot, "go.mod"))) return null;

  try {
    execSync("go version", { stdio: "ignore" });
  } catch {
    return null;
  }

  const out = join(tmpdir(), "edt-test-bin");
  try {
    execSync(`go build -o "${out}" ./cmd/edt`, { cwd: repoRoot, stdio: "ignore" });
    return out;
  } catch {
    return null;
  }
}

const validate = (bin: string, yaml: string): { status: number; stderr: string } => {
  const dir = mkdtempSync(join(tmpdir(), "edt-roundtrip-"));
  const file = join(dir, "s.yaml");
  writeFileSync(file, yaml);
  try {
    const out = execFileSync(bin, ["validate", "--file", file], { encoding: "utf8" });
    return { status: 0, stderr: out };
  } catch (err) {
    const e = err as { status?: number; stderr?: Buffer | string };
    return {
      status: e.status ?? -1,
      stderr: e.stderr ? String(e.stderr) : String(err),
    };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
};

describe("round-trip against edt validate", () => {
  let bin: string | null;

  beforeAll(() => {
    bin = resolveBinary();
  });

  afterAll(() => {
    if (bin && !process.env.EDT_BIN && bin.includes("edt-test-bin")) {
      rmSync(bin, { force: true });
    }
  });

  const cases: Array<[string, () => Scenario]> = [
    [
      "minimal produce",
      () =>
        scenario("minimal")
          .kafka("localhost:9092")
          .produce("p", { topic: "t", payload: "${data.x}", count: 1 })
          .build(),
    ],
    [
      "consume with slow_mode and match",
      () =>
        scenario("consume-slow")
          .kafka("b:9092")
          .consume("w", {
            topic: "acks",
            timeout: "5s",
            match: ["payload.id == previous.id"],
            slowMode: { pauseEvery: 100, pauseFor: "500ms" },
          })
          .build(),
    ],
    [
      "http step with expect",
      () =>
        scenario("http-step")
          .http("https://api.example.com")
          .httpStep("call", { method: "GET", path: "/x", expect: { status: 200 } })
          .build(),
    ],
    [
      "checks + labels",
      () =>
        scenario("checks")
          .label("env", "ci")
          .kafka("b:9092")
          .produce("p", { topic: "t", payload: "${data.x}", count: 5 })
          .check("c", "size(stream('t')) >= 1", { window: "30s", severity: "critical" })
          .build(),
    ],
    [
      "agent_under_test + eval",
      () =>
        scenario("eval")
          .http("http://a")
          .sleep("s", "1ms")
          .agentUnderTest({ name: "bot", produces: ["replies"] })
          .eval("polite", {
            judge: { model: "claude-opus-4-7", rubric: "Be polite." },
            threshold: { aggregate: "avg", value: ">= 4.2", over: "50 runs" },
            severity: "critical",
          })
          .build(),
    ],
    [
      "all connectors + data + fail injection",
      () => ({
        apiVersion: API_VERSION,
        kind: KIND_SCENARIO,
        metadata: { name: "big", labels: { team: "commerce" } },
        spec: {
          connectors: {
            kafka: {
              bootstrap_servers: "b:9092",
              auth: { type: "sasl_scram_sha_512", username: "a", password: "b" },
              schema_registry: { url: "http://sr:8081", flavor: "confluent" },
            },
            http: {
              base_url: "https://api.example.com",
              auth: { type: "bearer", token: "t" },
            },
          },
          data: {
            orders: {
              schema: { source: "schema_registry", subject: "orders-value" },
              generator: { strategy: "faker", seed: 42 },
            },
          },
          steps: [
            {
              name: "p",
              produce: {
                topic: "orders",
                payload: "${data.orders}",
                count: 10,
                fail_rate: 0.05,
                fail_mode: "schema_violation",
                schema_subject: "orders-value",
              },
            },
          ],
        },
      }),
    ],
  ];

  for (const [name, build] of cases) {
    it(`validates: ${name}`, () => {
      if (!bin) {
        console.warn("edt binary unavailable — set EDT_BIN or install Go; skipping");
        return;
      }
      const yaml = emit(build());
      const { status, stderr } = validate(bin, yaml);
      expect(status, stderr).toBe(0);
    });
  }
});
