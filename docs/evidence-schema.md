# Evidence schema: `invaris.evidence/v1alpha2`

Every `run`, `check` and `challenge` that loads a valid harness prints one JSON report on stdout (and to `--report`, byte for byte). Diagnostics go to stderr. A harness that fails to load produces no report: the CLI exits 2 with a diagnostic on stderr.

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
| `steps`, `checks`, `proofs` | Typed results. Statuses: checks `PASS`/`FAIL`/`REFUSE`/`NOT_RUN`; proofs `PROVEN`/`UNPROVEN`/`INCONCLUSIVE`/`NOT_RUN`. A proof carries its baseline and challenged check, a `faultId` and `evidenceStrength`. |
| `observations` | What ran: program, argv, provider identity (nullable versions), scope, timing, exit code, bounded redacted `stdout`/`stderr` excerpts and diagnostics. |
| `targetCount` | Evaluated units: inspected targets for structured guards, one per legacy command or proof. `null` when unknown. |
| `decision` | `ACCEPT`, `REJECT` or `REFUSE`. Exit codes 0, 1, 2. |
| `diagnostics`, `artifacts` | Report-level diagnostics; persisted artifacts `{path, sha256, bytes}`. |

Unknown values are explicit `null`s, never omitted keys, and lists are `[]`, never `null`. Serialization is deterministic: fixed field order, no maps, two-space indent, trailing newline; the same populated report always yields identical bytes (run ID and times differ between runs).

## Decision rules

ACCEPT requires all of: every step passed; every required guard and proof is present (missing, duplicated, `NOT_RUN`, `REFUSE` or `INCONCLUSIVE` required results refuse); no failing check, `UNPROVEN` proof or weak `PROVEN` proof (unless `allowWeakEvidence`); and at least one requested item. Failures and refusals both stay visible in the results. `precedence` only decides which signal names the decision when both exist: `failure-first` (default) gives REJECT, `refusal-first` gives REFUSE.

An ACCEPT additionally needs `configDigest`, a positive `targetCount`, inspected targets on every passing structured check (unless its policy has `allowEmpty`), a passing baseline and failing challenged check on every proof, and, for strong proofs, exactly one finding for the proof's policy at its target. If the engine produces an ACCEPT that fails validation it records an `EVIDENCE_INCOMPLETE` refusal instead.

Evidence strength: `strong` means a structured guard reported exactly one finding for the policy at the mutated path; `weak` means a marker string in command output. v1alpha1 harnesses permit weak proofs for compatibility and say so in `compatibility.warnings`.

## Redaction and artifacts

Values of the environment variables named in `redactEnv` are replaced with `[REDACTED]` in every string of the report (excerpts, argv, diagnostics, findings) before serialization. Matching is literal: overlapping matches are merged, longer secrets win, and a partial secret prefix cut off by an output size limit is also removed. Encoded, transformed or unconfigured secrets are **not** detected, nor are secrets in files the commands themselves write. Protocol parsing sees the original bounded bytes; only the report is redacted. `redactEnv` values are never serialized. Excerpts are bounded to 4 KiB per stream (64 KiB with `storeSensitiveOutput`).

`evidence.PersistArtifact`/`PersistRedacted` write with exclusive creation (existing files are never overwritten), return the SHA-256 and size of exactly the persisted (redacted) bytes, and never hash the report into itself. The engine does not yet produce artifacts, so CLI reports currently have `"artifacts": []`.

## Migrating from `invaris.evidence/v1alpha1`

| v1alpha1 | v1alpha2 |
| --- | --- |
| `status` `PASS`/`FAIL`/other | `decision` `ACCEPT`/`REJECT`/`REFUSE` (`INCONCLUSIVE` becomes `REFUSE`) |
| `results[]` of steps | `steps[]` |
| `results[]` of guards | `checks[]` (`inspection.inspected` is `inspected`; `inspection.findings` is `findings`) |
| `results[]` of challenges | `proofs[]` with `baseline`/`challenged` checks |
| `results[].output` | `observations[].stdout` and `.stderr`, separately, bounded and redacted |
| `results[].detail` | `diagnostics[]` on the result, plus report-level `diagnostics` |
| `results[].durationMs` | `observations[].startedAt`/`finishedAt`; `lifecycle[]` |
| `schemaVersion`, `configDigest`, `harness`, `action`, `startedAt`, `finishedAt` | unchanged names |

Exit codes are unchanged. Consumers must read `decision` instead of `status`. Results the harness does not declare (configuration or no-work refusals) appear as refused checks. Aggregate status is now computed from the typed results and gate, not carried over.
