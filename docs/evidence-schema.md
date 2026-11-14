# Evidence schema: `invaris.evidence/v1alpha2`

Every `run`, `check` and `challenge` that loads a valid harness prints one JSON report on stdout (and to `--report`, byte for byte). CLI errors such as a failed harness load or report publication go to stderr and exit 2 without emitting a verdict. Check, proof, and gate diagnostics are fields in the JSON report.

Schema: [`schemas/evidence-v1alpha2.schema.json`](../schemas/evidence-v1alpha2.schema.json) (JSON Schema Draft 2020-12). The schema checks shape. The correlation between `decision` and the results, and the completeness requirements below, are enforced by `evidence.Validate` in Go; a report that fails it is never printed with a passing decision. Run `python3 scripts/validate-schemas.py` (with `pip install -r scripts/requirements-schema.txt`, pinned to `jsonschema==4.23.0`, which requires Python 3.8 or newer) to validate real CLI output and deliberate counterexamples.

## Fields

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Always `invaris.evidence/v1alpha2`. |
| `runId` | Random 128-bit lowercase hex, unique per run. |
| `action`, `target`, `harness` | The action, the harness directory, and the harness `name`. |
| `configDigest` | `sha256:` of the exact accepted harness bytes; `null` only for non-ACCEPT reports built without a loaded file. |
| `gitRevision` | `HEAD` of the target from a bounded read-only `git rev-parse`; `null` when unavailable. |
| `environment`, `startedAt`, `finishedAt`, `durationMs` | OS/architecture and UTC timing. |
| `compatibility` | `native` (v1alpha2 input) or `legacy` (v1alpha1) with warnings. |
| `policies`, `gate` | Declared policies and the gate **applied** to this action. |
| `lifecycle` | One event per scheduled task in configuration order, `NOT_RUN` entries included, with null times when never started. |
| `steps`, `checks`, `proofs` | Typed results. Statuses: checks `PASS`/`FAIL`/`REFUSE`/`NOT_RUN`; proofs `PROVEN`/`UNPROVEN`/`INCONCLUSIVE`/`NOT_RUN`. A proof carries its baseline and challenged check, a `faultId`, `evidenceStrength`, a `detail` sentence, the recorded `mutation` (`path`, `beforeSha256`, `afterSha256`, or `null` when none was made) and its `stages` (see below). |
| `observations` | What ran (steps, guards, proof stages, and one `discover` observation per provider with its identity, availability and any discovery failure): program, argv, provider identity (nullable versions), scope, timing, exit code, bounded redacted `stdout`/`stderr` excerpts and diagnostics. |
| `targetCount` | Evaluated units: inspected targets for structured guards, one per legacy command or proof. `null` when unknown. |
| `decision` | `ACCEPT`, `REJECT` or `REFUSE`. Exit codes 0, 1, 2. |
| `diagnostics`, `artifacts` | Report-level diagnostics; persisted artifacts `{path, sha256, bytes}`. |

Unknown values are explicit `null`s, never omitted keys, and lists are `[]`, never `null`. Serialization is deterministic: fixed field order, no maps, two-space indent, trailing newline; the same populated report always yields identical bytes (run ID and times differ between runs).

## Decision rules

ACCEPT requires all of: every step passed; every required guard and proof is present (missing, duplicated, `NOT_RUN`, `REFUSE` or `INCONCLUSIVE` required results refuse); no failing check, `UNPROVEN` proof or weak `PROVEN` proof (unless `allowWeakEvidence`); and at least one requested item. Failures and refusals both stay visible in the results. `precedence` only decides which signal names the decision when both exist: `failure-first` (default) gives REJECT, `refusal-first` gives REFUSE.

An ACCEPT additionally needs `configDigest`, a positive `targetCount`, inspected targets on every passing structured check (unless its policy has `allowEmpty`), a passing baseline and failing challenged check on every proof, and, for strong proofs, exactly one finding for the proof's policy at its target. If the engine produces an ACCEPT that fails validation it records an `EVIDENCE_INCOMPLETE` refusal instead.

## Proof stages and cleanup

A proof runs in one disposable copy of the checkout: `prepare` (copy), `baseline` (the guard over the clean copy), `mutate` (the engine replaces the declared marker in the declared file), `challenged` (the same guard, evaluator and scope again) and `cleanup`. `stages` lists the stages that were attempted, each with `status` (`OK` or `FAILED`), `durationMs` and a `detail` for failures; a stage after an early failure is absent, except `cleanup`, which always runs once the copy exists. The mutation target is revalidated after the baseline (it must still be the same regular, writable, non-linked file containing the marker exactly once), the workspace file manifest is compared before and after the replacement, and exactly the declared file may differ. `faultId` is derived from the policy, the target and the before and after digests, so analyzers need not echo it.

A proof is `PROVEN` only if the baseline passed, the challenged check failed with exactly one finding for the proof's policy, native rule and target path (more, fewer or different findings, a check that inspected nothing, and a tool failure are `INCONCLUSIVE`; a challenged pass is `UNPROVEN`), and the copy was removed and confirmed absent. A cleanup failure makes the proof `INCONCLUSIVE` and the report validator also rejects any `PROVEN` proof without a recorded mutation of its target or without an `OK` cleanup stage. The report is built only after cleanup has finished. v1alpha1 command guards identify a policy, not a separate native rule, so their findings carry the policy ID as the rule.

Evidence strength: `strong` means a structured guard reported exactly one finding for the policy at the mutated path; `weak` means a marker string in command output. v1alpha1 harnesses permit weak proofs for compatibility and say so in `compatibility.warnings`.

## Redaction and artifacts

Values of the environment variables named in `redactEnv` are replaced with `[REDACTED]` in every string of the report (excerpts, argv, diagnostics, findings) before serialization. Matching is literal: overlapping matches are merged, longer secrets win, and a partial secret prefix cut off by an output size limit is also removed. Encoded, transformed or unconfigured secrets are **not** detected, nor are secrets in files the commands themselves write. Protocol parsing sees the original bounded bytes; only the report is redacted. `redactEnv` values are never serialized. Excerpts are bounded to 4 KiB per stream (64 KiB with `storeSensitiveOutput`).

`evidence.PersistArtifact`/`PersistRedacted` write with exclusive creation (existing files are never overwritten), return the SHA-256 and size of exactly the persisted (redacted) bytes, and never hash the report into itself. The engine does not yet produce artifacts, so CLI reports currently have `"artifacts": []`.

## Output migration

| v1alpha1 | v1alpha2 |
| --- | --- |
| `status` `PASS`/`FAIL`/other | `decision` `ACCEPT`/`REJECT`/`REFUSE` (`INCONCLUSIVE` becomes `REFUSE`) |
| `results[]` of steps | `steps[]` |
| `results[]` of guards | `checks[]` (`inspection.inspected` is `inspected`; `inspection.findings` is `findings`) |
| `results[]` of challenges | `proofs[]` with `baseline`/`challenged` checks, `mutation` digests and `stages` |
| `results[].output` | `observations[].stdout` and `.stderr`, separately, bounded and redacted |
| `results[].detail` | `diagnostics[]` on the result, plus report-level `diagnostics` |
| `results[].durationMs` | `observations[].startedAt`/`finishedAt`; `lifecycle[]` |
| `schemaVersion`, `configDigest`, `harness`, `action`, `startedAt`, `finishedAt` | unchanged names |

Exit codes are unchanged. Consumers must read `decision` instead of `status`. Results the harness does not declare (configuration or no-work refusals) appear as refused checks. Aggregate status is now computed from the typed results and gate, not carried over.
