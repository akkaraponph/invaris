#!/usr/bin/env node
// Invaris provider (invaris.adapter/v1alpha1) over dependency-cruiser.
//
// It reads one JSON request on stdin and writes one JSON response on stdout.
// The pinned tool is installed next to this file (node_modules is never inside
// the project being checked) and is run with the current Node binary, an
// explicit configuration file and explicit scope entries. The policy ID of a
// check names one rule of the configured rule set; the native rule name is
// reported separately as the finding's ruleId.
//
// The reporter emits the upstream JSON result and uses its advised exit code.
import { spawn } from "node:child_process";
import { existsSync, lstatSync, readFileSync, realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const PROTOCOL = "invaris.adapter/v1alpha1";
export const PROVIDER_NAME = "invaris-dependency-cruiser";
export const PROVIDER_VERSION = "0.1.0";
const HERE = path.dirname(fileURLToPath(import.meta.url));
const MAX_REQUEST = 1 << 20;
const TOOL_TIMEOUT_MS = 100_000;
const MAX_NATIVE_BYTES = 64 << 20;
const SUPPORTED_VIOLATION_TYPES = new Set(["dependency"]);

/** A protocol-level failure: becomes an `error` response. */
export class ProviderError extends Error {
  constructor(code, message, inspected = null) {
    super(message);
    this.code = code;
    this.inspected = inspected;
  }
}

export function safeRelative(p) {
  if (p === ".") return true;
  if (typeof p !== "string" || p === "" || p.startsWith("/") || p.startsWith("-") || /[\\:\0]/.test(p)) return false;
  return p.split("/").every((part) => part !== "" && part !== "." && part !== "..");
}

/** Locates the installed tool. Returns {script, version} or null. */
export function locateTool(root = HERE) {
  const dir = path.join(root, "node_modules", "dependency-cruiser");
  try {
    const manifest = JSON.parse(readFileSync(path.join(dir, "package.json"), "utf8"));
    const script = path.join(dir, "bin", "dependency-cruiser.mjs");
    if (manifest.name !== "dependency-cruiser" || typeof manifest.version !== "string" || !existsSync(script)) return null;
    return { script, version: manifest.version };
  } catch {
    return null;
  }
}

/** The exact version this provider was written and tested against. */
export function pinnedVersion(root = HERE) {
  const manifest = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
  return manifest.devDependencies["dependency-cruiser"];
}

function identity(tool) {
  return { name: PROVIDER_NAME, version: PROVIDER_VERSION, toolName: "dependency-cruiser", toolVersion: tool ? tool.version : null };
}

/** Validates options: exactly {configPath}, a regular non-symlink file under cwd. */
export function parseOptions(options, cwd) {
  if (options === null || typeof options !== "object" || Array.isArray(options)) {
    throw new ProviderError("INVALID_REQUEST", "options must be an object {configPath}");
  }
  const keys = Object.keys(options);
  if (keys.length !== 1 || keys[0] !== "configPath" || !safeRelative(options.configPath)) {
    throw new ProviderError("INVALID_REQUEST", "options must be exactly {configPath: <canonical relative path>}");
  }
  const full = path.join(cwd, ...options.configPath.split("/"));
  let info;
  try {
    info = lstatSync(full);
  } catch {
    throw new ProviderError("INVALID_REQUEST", `configPath ${options.configPath} does not exist`);
  }
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new ProviderError("INVALID_REQUEST", `configPath ${options.configPath} is not a regular file`);
  }
  if (realpathSync(full) !== path.join(realpathSync(cwd), ...options.configPath.split("/"))) {
    throw new ProviderError("INVALID_REQUEST", `configPath ${options.configPath} resolves outside its own path`);
  }
  return { configPath: options.configPath, full };
}

/** Rule names of the `forbidden` section of a configuration file. */
export async function configuredRules(full) {
  let config;
  if (full.endsWith(".json")) {
    config = JSON.parse(readFileSync(full, "utf8"));
  } else {
    const module = await import(pathToFileURL(full).href);
    config = module.default ?? module;
  }
  const forbidden = config && config.forbidden;
  if (!Array.isArray(forbidden)) throw new ProviderError("INVALID_REQUEST", "configuration has no forbidden rule list");
  return forbidden.filter(isSupportedRule).map((rule) => rule.name);
}

function isSupportedRule(rule) {
  return rule && typeof rule.name === "string" && rule.name.trim() !== "" &&
    rule.severity === "error" && (rule.scope === undefined || rule.scope === "module") &&
    rule.from && rule.to;
}

/**
 * Translates one native JSON report into a protocol check, or throws a
 * ProviderError for anything that is not a recognised report. It never
 * guesses: unknown shapes are MALFORMED_OUTPUT.
 */
export function translate(native, { policy, nativeExit, providerName = PROVIDER_NAME }) {
  const bad = (why) => new ProviderError("MALFORMED_OUTPUT", "unrecognised dependency-cruiser output: " + why);
  if (native === null || typeof native !== "object" || Array.isArray(native)) throw bad("not an object");
  const { summary, modules } = native;
  if (!summary || typeof summary !== "object" || !Array.isArray(modules) || !Array.isArray(summary.violations)) throw bad("missing summary or modules");
  const isCount = (n) => Number.isInteger(n) && n >= 0;
  if (!isCount(summary.totalCruised) || !isCount(summary.error) || !isCount(summary.advisedExitCode)) throw bad("counts are not integers");
  if (modules.some((m) => typeof m?.source !== "string" || !Array.isArray(m.dependencies))) throw bad("invalid module");
  if (summary.totalCruised !== modules.length) throw bad("totalCruised disagrees with the module list");
  const rules = summary.ruleSetUsed && summary.ruleSetUsed.forbidden;
  if (!Array.isArray(rules)) throw bad("rule set is absent");
  if (!rules.some((r) => isSupportedRule(r) && r.name === policy)) {
    throw new ProviderError("UNSUPPORTED_CAPABILITY", `rule ${JSON.stringify(policy)} is not declared in the configuration`);
  }
  const errors = [];
  for (const v of summary.violations) {
    if (!v || typeof v !== "object" || !v.rule || typeof v.rule.name !== "string" || !["error", "warn", "info", "ignore"].includes(v.rule.severity)) throw bad("violation without a rule");
    if (!SUPPORTED_VIOLATION_TYPES.has(v.type)) throw bad(`unsupported violation type ${JSON.stringify(v.type)}`);
    if (v.from === "." || !safeRelative(v.from) || typeof v.to !== "string") throw bad("violation without from/to");
    if (v.rule.severity === "error") errors.push(v);
  }
  if (errors.length !== summary.error) throw bad("error count disagrees with the violation list");
  if (nativeExit !== summary.advisedExitCode || nativeExit !== summary.error) {
    throw bad("process exit, advised exit and error count disagree");
  }
  if (summary.totalCruised === 0) throw new ProviderError("EVIDENCE_UNAVAILABLE", "dependency-cruiser inspected no modules in the requested scope", 0);

  const own = errors.filter((v) => v.rule.name === policy);
  if (own.length !== errors.length) {
    throw new ProviderError("EVIDENCE_UNAVAILABLE", "dependency-cruiser reported an error from a different rule", summary.totalCruised);
  }
  if (nativeExit > 1) {
    throw new ProviderError("EVIDENCE_UNAVAILABLE", "multiple native errors exceed the adapter protocol's violation exit code", summary.totalCruised);
  }
  return {
    policyId: policy,
    providerId: providerName,
    status: own.length > 0 ? "FAIL" : "PASS",
    inspected: summary.totalCruised,
    findings: own.map((v) => ({ policyId: policy, ruleId: v.rule.name, path: v.from, message: `${v.from} must not depend on ${v.to}` })),
    diagnostics: [],
    exitCode: nativeExit,
  };
}

/** Runs the pinned tool. Resolves {code, stdout, stderr}; never rejects. */
export function runNative(tool, args, cwd) {
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [tool.script, ...args], { cwd, env: process.env, stdio: ["ignore", "pipe", "pipe"], timeout: TOOL_TIMEOUT_MS });
    const out = [];
    const err = [];
    let outBytes = 0;
    let overflow = false;
    child.stdout.on("data", (b) => {
      outBytes += b.length;
      if (outBytes > MAX_NATIVE_BYTES) {
        overflow = true;
        child.kill("SIGKILL");
      } else out.push(b);
    });
    child.stderr.on("data", (b) => {
      if (err.reduce((n, x) => n + x.length, 0) < 8192) err.push(b);
    });
    child.on("error", (e) => resolve({ code: null, signal: null, stdout: "", stderr: String(e), overflow }));
    child.on("close", (code, signal) =>
      resolve({ code, signal, stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), overflow }),
    );
  });
}

function reply(requestId, body) {
  return { version: PROTOCOL, requestId, ...body };
}

function errorReply(requestId, tool, code, message) {
  return reply(requestId, { provider: identity(tool), error: { code, message } });
}

/**
 * Handles one request object. `deps` lets tests supply a fake tool locator and
 * native runner; production uses the real ones.
 */
export async function handle(request, deps = {}) {
  const cwd = deps.cwd ?? process.cwd();
  const locate = deps.locateTool ?? (() => locateTool());
  const run = deps.runNative ?? runNative;
  const pinned = deps.pinnedVersion ?? (() => pinnedVersion());
  const requestId = request.requestId;
  const tool = locate();
  const fail = (code, message) => errorReply(requestId, tool, code, message);
  if (request.version !== PROTOCOL) return fail("UNSUPPORTED_PROTOCOL", "only " + PROTOCOL + " is supported");

  const wantVersion = pinned();
  const toolProblem = !tool
    ? "dependency-cruiser is not installed; run: npm ci --prefix adapters/dependency-cruiser"
    : tool.version !== wantVersion
      ? `installed dependency-cruiser ${tool.version} differs from the pinned ${wantVersion}`
      : "";

  try {
    switch (request.operation) {
      case "discover": {
        let policyIds = [];
        const prerequisites = [];
        if (toolProblem) prerequisites.push(toolProblem);
        else if (request.options != null) {
          // Same options as check: the configured rule names are negotiable.
          const { full } = parseOptions(request.options, cwd);
          policyIds = await configuredRules(full);
        }
        return reply(requestId, {
          provider: identity(tool),
          capabilities: {
            languages: ["javascript"],
            policyIds,
            operations: ["discover", "check"],
            proofTypes: [],
            reportFormats: ["json"],
            available: !toolProblem,
            prerequisites,
          },
        });
      }
      case "mutate":
        return fail("UNSUPPORTED_CAPABILITY", "this provider does not support mutation");
      case "check":
        break;
      default:
        return fail("INVALID_REQUEST", "unknown operation");
    }

    if (toolProblem) return fail("TOOL_UNAVAILABLE", toolProblem);
    const policies = request.policyIds ?? [];
    const scope = request.scope ?? [];
    if (policies.length !== 1 || typeof policies[0] !== "string" || policies[0] === "") {
      return fail("UNSUPPORTED_CAPABILITY", "exactly one policy (a rule name) per check is supported");
    }
    if (scope.length === 0 || !scope.every(safeRelative)) {
      return fail("INVALID_REQUEST", "scope must be non-empty canonical relative paths");
    }
    const { full } = parseOptions(request.options, cwd);
    const reporter = pathToFileURL(path.join(HERE, "reporter.mjs")).href;
    const refuse = (native, code, message, inspected = null) => reply(requestId, {
      provider: identity(tool),
      check: {
        id: "check-" + String(requestId).slice(0, 8),
        providerId: PROVIDER_NAME,
        policyId: policies[0],
        status: "REFUSE",
        inspected,
        findings: [],
        diagnostics: [{ code, message }],
        exitCode: native.code,
      },
    });
    const native = await run(tool, ["--config", full, "--output-type", "plugin:" + reporter, "--output-to", "-", "--no-cache", "--", ...scope], cwd);
    if (native.overflow) return refuse(native, "MALFORMED_OUTPUT", "dependency-cruiser output exceeded the size limit");
    if (native.code === null) {
      return refuse(native, "INTERNAL_PROVIDER_FAILURE", "dependency-cruiser could not be started or was killed: " + native.stderr.trim().slice(0, 300));
    }
    let check;
    try {
      check = translate(JSON.parse(native.stdout), { policy: policies[0], nativeExit: native.code });
    } catch (e) {
      if (native.code !== 0 && native.stderr.trim()) {
        return refuse(native, "INTERNAL_PROVIDER_FAILURE", `dependency-cruiser exited ${native.code}: ${native.stderr.trim().slice(0, 300)}`);
      }
      const code = e instanceof ProviderError ? e.code : native.code === 0 ? "MALFORMED_OUTPUT" : "INTERNAL_PROVIDER_FAILURE";
      const message = e instanceof ProviderError ? e.message : "dependency-cruiser did not print one JSON document";
      return refuse(native, code, message, e.inspected);
    }
    return reply(requestId, { provider: identity(tool), check: { id: "check-" + String(requestId).slice(0, 8), ...check } });
  } catch (e) {
    if (e instanceof ProviderError) return fail(e.code, e.message);
    return fail("INTERNAL_PROVIDER_FAILURE", String(e && e.message ? e.message : e).slice(0, 300));
  }
}

async function readStdin() {
  const chunks = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    size += chunk.length;
    if (size > MAX_REQUEST) return null;
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

async function main() {
  const raw = await readStdin();
  let request;
  try {
    request = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw ?? new Uint8Array()));
    if (!request || typeof request.requestId !== "string" || request.requestId === "" || typeof request.operation !== "string") throw new Error("shape");
  } catch {
    process.stdout.write(JSON.stringify(errorReply("", null, "INVALID_REQUEST", "request is not a JSON object with requestId and operation")) + "\n");
    return 1;
  }
  process.stdout.write(JSON.stringify(await handle(request)) + "\n");
  return 0;
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main();
}
