import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { handle, PROTOCOL, translate } from "./provider.mjs";

const rule = "no-forbidden-import";
const fixture = (name) => JSON.parse(readFileSync(new URL(`testdata/${name}.json`, import.meta.url)));
const parse = (report, nativeExit) => translate(report, { policy: rule, nativeExit });

test("clean and violating native reports preserve evidence and actual reporter exits", () => {
  const clean = parse(fixture("clean"), 0);
  assert.deepEqual([clean.status, clean.inspected, clean.exitCode, clean.findings], ["PASS", 3, 0, []]);
  const violation = parse(fixture("violation"), 1);
  assert.deepEqual([violation.status, violation.inspected, violation.exitCode], ["FAIL", 3, 1]);
  assert.deepEqual(violation.findings.map(({ policyId, ruleId, path }) => [policyId, ruleId, path]), [[rule, rule, "src/main.js"]]);
});

test("zero modules cannot establish a pass", () => {
  const report = fixture("clean");
  report.modules = [];
  report.summary.totalCruised = 0;
  assert.throws(() => parse(report, 0), { code: "EVIDENCE_UNAVAILABLE" });
});

test("an unrelated failing rule refuses the requested policy", () => {
  const report = fixture("violation");
  report.summary.violations[0].rule.name = "some-other-rule";
  assert.throws(() => parse(report, 1), { code: "EVIDENCE_UNAVAILABLE" });
});

test("multiple native errors cannot be normalized to a protocol violation exit", async () => {
  const report = fixture("violation");
  report.summary.violations.push(structuredClone(report.summary.violations[0]));
  report.summary.error = 2;
  report.summary.advisedExitCode = 2;
  const result = await handle(request, {
    ...deps,
    runNative: async () => ({ code: 2, stderr: "", stdout: JSON.stringify(report) }),
  });
  assert.equal(result.check.status, "REFUSE");
  assert.equal(result.check.exitCode, 2);
  assert.equal(result.check.inspected, 3);
});

test("malformed output and disagreement with native exit refuse", () => {
  assert.throws(() => parse(fixture("violation"), 0), { code: "MALFORMED_OUTPUT" });
  assert.throws(() => parse(fixture("clean"), 1), { code: "MALFORMED_OUTPUT" });
  const report = fixture("violation");
  report.summary.violations[0].type = "unknown";
  assert.throws(() => parse(report, 1), { code: "MALFORMED_OUTPUT" });
});

const request = {
  version: PROTOCOL,
  requestId: "test-request",
  operation: "check",
  scope: ["src"],
  policyIds: [rule],
  options: { configPath: ".dependency-cruiser.cjs" },
};
const deps = {
  cwd: fileURLToPath(new URL("../../examples/dependency-cruiser/", import.meta.url)),
  pinnedVersion: () => "18.7.2",
  locateTool: () => ({ version: "18.7.2" }),
};

test("discovery lists the configured rule and the pinned tool version", async () => {
  const result = await handle({ ...request, operation: "discover" }, deps);
  assert.equal(result.provider.toolVersion, "18.7.2");
  assert.deepEqual(result.capabilities.policyIds, [rule]);
  assert.deepEqual(result.capabilities.operations, ["discover", "check"]);
});

test("missing tool and nonexistent config refuse", async () => {
  const result = await handle(request, { ...deps, locateTool: () => null });
  assert.equal(result.error.code, "TOOL_UNAVAILABLE");
  const invalid = await handle({ ...request, options: { configPath: "missing.cjs" } }, deps);
  assert.equal(invalid.error.code, "INVALID_REQUEST");
});

test("invalid native config and crash preserve the process exit and never pass", async () => {
  for (const [code, stderr] of [[1, "configuration is not valid"], [7, "crashed"]]) {
    const result = await handle(request, {
      ...deps,
      runNative: async () => ({ code, stderr, stdout: code === 7 ? JSON.stringify(fixture("clean")) : "" }),
    });
    assert.equal(result.check.status, "REFUSE");
    assert.equal(result.check.exitCode, code);
    assert.deepEqual(result.check.findings, []);
  }
});
