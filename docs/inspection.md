# Structured command guards

Set a guard's `policyId` to opt into structured inspection. The executable receives its declared arguments and runs from the harness workspace. It writes exactly one JSON object to stdout. Stderr cannot supply proof evidence.

```json
{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":1,"findings":[]}
```

## Exit codes and findings

- Exit 0 requires an empty `findings` array.
- Exit 1 requires at least one finding.
- Each finding needs the matching `policyId` and a relative `path`.
- A finding may add `ruleId` (1 to 128 bytes; the policy is reported as the rule when it is omitted), `message` (at most 1024 bytes) and `line` (an integer of at least 1). They are copied into the report's findings, so details such as which route lost its guard stay in structured evidence instead of stderr. Messages are withheld in v1alpha5 reports like other free text; `line` is kept. Unknown fields, `null` values, an empty `ruleId` and a `line` below 1 are refused.

```json
{"version":"invaris.inspection/v1alpha1","policyId":"auth","inspected":14,"findings":[{"policyId":"auth","path":"src/routes.ts","ruleId":"route-unguarded","message":"GET /admin has no auth check","line":42}]}
```
- Invalid JSON, inconsistent exits, and zero inspected targets yield `REFUSE`.

## Challenge results

The engine runs the baseline and challenged check with the same guard in one disposable copy. A strong proof requires:

1. A passing baseline that inspected at least one target.
2. One replacement of the declared marker in the declared file.
3. Exactly one finding for the expected policy at that path. A structured command guard may report any `ruleId`; a provider guard's finding must also name the expected rule.
4. Removal of the copy and confirmation that it is absent.

A challenged pass yields `UNPROVEN`. Missing, extra, or misplaced findings, tool failures, and cleanup failures yield `INCONCLUSIVE`. The engine reads the source checkout, including uncommitted files, to create the copy. See [proof stages](evidence-schema.md#proof-stages-and-cleanup) and the [threat model](threat-model.md).

## Inspection count and trust

Optionally, set `minInspected` on the guard (see [declared inspection floor](harness-spec.md#declared-inspection-floor)) to refuse a report whose inspected count is below a number you chose independently of the guard. Zero inspected targets is always refused, with or without it. Legacy guards that rely on an `expectedOutput` marker are weak evidence and are separate from this structured contract.

This contract trusts the declared command to report its inspection truthfully. It supports one policy per command. For external provider discovery, see the [adapter protocol](adapter-protocol.md). For a tool-specific integration, see the [Dependency-Cruiser example](dependency-cruiser-example.md). Run `go run ./cmd/invaris challenge examples/structured/invaris.json` for the offline fixture.

The [forbidden-dependency fixture](../examples/forbidden-dependency) declares `dependencies.txt` as the exact provider scope. The engine checks returned finding paths against that scope and matches the challenged finding to the mutated target. A clean inspection count is the provider’s claim and carries no per-file coverage inventory.
