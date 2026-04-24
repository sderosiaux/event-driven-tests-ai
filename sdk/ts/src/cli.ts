#!/usr/bin/env node
// edt-ts compile <file.ts> [-o out.yaml]
//
// Loads a TypeScript scenario file (ESM), resolves the exported Scenario (or
// ScenarioBuilder), and writes canonical YAML. One file → one YAML document.
//
// Exports supported (checked in order):
//   - default export                       → Scenario | ScenarioBuilder
//   - named export `scenario`              → Scenario | ScenarioBuilder
//   - named export `scenarios` (array)     → emitted as YAML multi-doc
//
// Errors go to stderr with a non-zero exit code so CI pipelines fail loudly.

import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { emit } from "./yaml.js";
import { ScenarioBuilder } from "./builder.js";
import type { Scenario } from "./types.js";

interface Args {
  cmd: "compile" | "help" | "version";
  file?: string;
  out?: string;
}

function parseArgs(argv: string[]): Args {
  if (argv.length === 0 || argv[0] === "-h" || argv[0] === "--help") {
    return { cmd: "help" };
  }
  if (argv[0] === "-v" || argv[0] === "--version") {
    return { cmd: "version" };
  }
  if (argv[0] !== "compile") {
    die(`unknown command: ${argv[0]}`);
  }
  const rest = argv.slice(1);
  let file: string | undefined;
  let out: string | undefined;
  for (let i = 0; i < rest.length; i++) {
    const a = rest[i]!;
    if (a === "-o" || a === "--out") {
      out = rest[++i];
      if (!out) die("flag -o requires a path");
    } else if (!file && !a.startsWith("-")) {
      file = a;
    } else {
      die(`unexpected argument: ${a}`);
    }
  }
  if (!file) die("compile: missing <file.ts>");
  return { cmd: "compile", file, out };
}

function die(msg: string): never {
  process.stderr.write(`edt-ts: ${msg}\n`);
  process.exit(2);
}

function usage(): void {
  process.stdout.write(
    [
      "Usage: edt-ts <command> [options]",
      "",
      "Commands:",
      "  compile <file.ts> [-o out.yaml]    Emit canonical YAML from a TS scenario",
      "",
      "Flags:",
      "  -h, --help      Show help",
      "  -v, --version   Show version",
      "",
    ].join("\n"),
  );
}

async function loadTS(path: string): Promise<unknown> {
  // tsx exposes an ESM loader hook. Register it once, then dynamic-import the
  // user's file as a file:// URL so relative imports inside their file work.
  let api: typeof import("tsx/esm/api");
  try {
    api = await import("tsx/esm/api");
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ERR_MODULE_NOT_FOUND") {
      die(
        "tsx runtime is missing. Install it: npm i -D tsx (or run edt-ts via npm exec)",
      );
    }
    throw err;
  }
  const unregister = api.register();
  try {
    const url = pathToFileURL(resolve(path)).href;
    // ERR_MODULE_NOT_FOUND from the user's module graph must surface as-is,
    // not get rewritten to "tsx runtime is missing". Only the outer import
    // of tsx itself is gated by the tsx-missing diagnostic above.
    return await import(url);
  } finally {
    unregister();
  }
}

function materialize(v: unknown): Scenario | Scenario[] | undefined {
  if (v instanceof ScenarioBuilder) return v.build();
  if (isScenario(v)) return v;
  if (Array.isArray(v) && v.every((x) => x instanceof ScenarioBuilder || isScenario(x))) {
    return v.map((x) => (x instanceof ScenarioBuilder ? x.build() : (x as Scenario)));
  }
  return undefined;
}

function isScenario(v: unknown): v is Scenario {
  if (!v || typeof v !== "object") return false;
  const o = v as Record<string, unknown>;
  return o.apiVersion === "edt.io/v1" && o.kind === "Scenario";
}

async function compile(file: string, out?: string): Promise<void> {
  const mod = (await loadTS(file)) as Record<string, unknown>;
  const picked =
    materialize(mod.default) ??
    materialize(mod.scenario) ??
    materialize(mod.scenarios);
  if (!picked) {
    die(
      `${file}: expected default export (or 'scenario'/'scenarios') to be a Scenario or ScenarioBuilder`,
    );
  }
  const body = Array.isArray(picked)
    ? picked.map((s) => emit(s)).join("---\n")
    : emit(picked);
  if (out) {
    writeFileSync(out, body);
  } else {
    process.stdout.write(body);
  }
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  switch (args.cmd) {
    case "help":
      usage();
      return;
    case "version":
      process.stdout.write(`edt-ts ${await readVersion()}\n`);
      return;
    case "compile":
      await compile(args.file!, args.out);
      return;
  }
}

async function readVersion(): Promise<string> {
  try {
    const { readFileSync } = await import("node:fs");
    const { fileURLToPath } = await import("node:url");
    const pkgPath = fileURLToPath(new URL("../package.json", import.meta.url));
    const pkg = JSON.parse(readFileSync(pkgPath, "utf8")) as { version: string };
    return pkg.version;
  } catch {
    return "0.0.0-dev";
  }
}

main().catch((err: unknown) => {
  process.stderr.write(`edt-ts: ${(err as Error).message}\n`);
  process.exit(1);
});
