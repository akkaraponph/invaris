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
| `gitDirty` | `true` when the target directory has uncommitted or untracked changes (bounded read-only `git status`), `false` when clean, `null` when unknown. A revision alone does not identify the files an experiment copied; see [reproducibility provenance](#reproducibility-provenance). |
| `environment`, `startedAt`, `finishedAt`, `durationMs` | OS/architecture and UTC timing. |
| `compatibility` | `native` (v1alpha2 input) or `legacy` (v1alpha1) with warnings. |
| `policies`, `gate` | Declared policies and the gate **applied** to this action. |
| `lifecycle` | One event per scheduled task in configuration order, `NOT_RUN` entries included, with null times when never started. |
| `steps`, `checks`, `proofs` | Typed results. Statuses: checks `PASS`/`FAIL`/`REFUSE`/`NOT_RUN`; proofs `PROVEN`/`UNPROVEN`/`INCONCLUSIVE`/`NOT_RUN`. A proof carries its baseline and challenged check, a `faultId`, `evidenceStrength`, a machine-readable `reason` and `reasonCategory` (see [reasons](#reasons-and-categories)), a `detail` sentence, a `sourceSnapshot`, the recorded `mutation` (`path`, `beforeSha256`, `afterSha256`, or `null` when none was made) and its `stages` (see below). The mutation record is hash-only; see [inspecting the actual edit](#inspecting-the-actual-edit). |
| `observations` | What ran (steps, guards, proof stages, and one `discover` observation per provider with its identity, availability and any discovery failure): program, argv, provider identity (nullable versions), scope, `executable` (resolved path and file SHA-256, or `null`), effective `timeoutSeconds`, the allowlisted environment variable **names** (never values), timing, exit code, bounded redacted `stdout`/`stderr` excerpts and diagnostics. |
| `targetCount` | Evaluated units: inspected targets for structured guards, one per legacy command or proof. `null` when unknown. |
| `decision` | `ACCEPT`, `REJECT` or `REFUSE`. Exit codes 0, 1, 2. |
| `diagnostics`, `artifacts` | Report-level diagnostics; persisted artifacts `{path, sha256, bytes}`. |

Unknown values are explicit `null`s, never omitted keys, and lists are `[]`, never `null`. Serialization is deterministic: fixed field order, no maps, two-space indent, trailing newline; the same populated report always yields identical bytes (run ID and times differ between runs).

## Decision rules

ACCEPT requires all of: every step passed; every required guard and proof is present (missing, duplicated, `NOT_RUN`, `REFUSE` or `INCONCLUSIVE` required results refuse); no failing check, `UNPROVEN` proof or weak `PROVEN` proof (unless `allowWeakEvidence`); and at least one requested item. Failures and refusals both stay visible in the results. `precedence` only decides which signal names the decision when both exist: `failure-first` (default) gives REJECT, `refusal-first` gives REFUSE.

An ACCEPT additionally needs `configDigest`, a positive `targetCount`, inspected targets on every passing structured check (unless its policy has `allowEmpty`), a passing baseline and failing challenged check on every proof, and, for strong proofs, exactly one finding for the proof's policy at its target. If the engine produces an ACCEPT that fails validation it records an `EVIDENCE_INCOMPLETE` refusal instead.

## Proof stages and cleanup

A proof runs in one disposable copy of the checkout: `prepare` (copy), `baseline` (the guard over the clean copy), `mutate` (the engine replaces the declared marker in the declared file), `challenged` (the same guard, evaluator and scope again) and `cleanup`. `stages` lists the stages that were attempted, each with `status` (`OK` or `FAILED`), `durationMs` and a `detail` for failures; a stage after an early failure is absent, except `cleanup`, which always runs once the copy exists. Proofs give the same integrity guarantee as mutation experiments: after the baseline the workspace root and manifest must be unchanged, and after the challenged check only the declared file may differ, otherwise the proof is `INCONCLUSIVE` with reason `workspace_changed`. The mutation target is revalidated after the baseline (it must still be the same regular, writable, non-linked file containing the marker exactly once), the workspace file manifest is compared before and after the replacement, and exactly the declared file may differ. `faultId` is derived from the policy, the target and the before and after digests, so analyzers need not echo it.

A proof is `PROVEN` only if the baseline passed, the challenged check failed with exactly one finding for the proof's policy, native rule and target path (more, fewer or different findings, a check that inspected nothing, and a tool failure are `INCONCLUSIVE`; a challenged pass is `UNPROVEN`), and the copy was removed and confirmed absent. A cleanup failure makes the proof `INCONCLUSIVE` and the report validator also rejects any `PROVEN` proof without a recorded mutation of its target or without an `OK` cleanup stage. The report is built only after cleanup has finished. v1alpha1 command guards identify a policy, not a separate native rule, so their findings carry the policy ID as the rule.

Evidence strength: `strong` means a structured guard reported exactly one finding for the policy at the mutated path; `weak` means a marker string in command output. v1alpha1 harnesses permit weak proofs for compatibility and say so in `compatibility.warnings`.

## Redaction and artifacts

Values of the environment variables named in `redactEnv` are replaced with `[REDACTED]` in every string of the report (excerpts, argv, diagnostics, findings) before serialization. Matching is literal: overlapping matches are merged, longer secrets win, and a partial secret prefix cut off by an output size limit is also removed. Encoded, transformed or unconfigured secrets are **not** detected, nor are secrets in files the commands themselves write. Protocol parsing sees the original bounded bytes; only the report is redacted. `redactEnv` values are never serialized. Excerpts are bounded to 4 KiB per stream (64 KiB with `storeSensitiveOutput`).

`evidence.PersistArtifact`/`PersistRedacted` write with exclusive creation (existing files are never overwritten), return the SHA-256 and size of exactly the persisted (redacted) bytes, and never hash the report into itself. The engine does not yet produce artifacts, so CLI reports currently have `"artifacts": []`.

## Reasons and categories

Proofs (`proofs[].reason`) and mutation experiments (`experiment.reason`) share one failure vocabulary so a consumer can tell a bad experiment from a broken machine without parsing prose. The verdicts stay separate: `PROVEN`/`UNPROVEN` for guards, `KILLED`/`SURVIVED`/`INVALID` for tests. `reasonCategory` is derived from `reason` and is validated against it.

| Category | Meaning | Reasons |
| --- | --- | --- |
| `result` | A completed assessment | `guard_detected`, `marker_matched` (weak), `guard_passed`, `marker_not_found` (weak), `assertion_failed`, `all_tests_passed`, `mutant_invalid` |
| `setup` | The experiment as configured cannot give a meaningful answer; fix the harness or the project | `invalid_target`, `guard_not_found`, `provider_unavailable`, `unhealthy_baseline`, `mutation_rejected`, `no_targets_inspected`, `finding_mismatch` |
| `infrastructure` | The tools or machine failed; retry may help | `workspace_prepare_failed`, `timeout`, `canceled`, `missing_executable`, `output_limit`, `execution_failed`, `cleanup_failed` |
| `evidence` | The evidence or the workspace could not be trusted | `malformed_evidence`, `workspace_changed` |

`PROVEN` and `UNPROVEN` proofs carry only `result` reasons and an `INCONCLUSIVE` proof never does. A proof that never ran (`NOT_RUN`) has an empty reason and category. Exit code 2 alone cannot tell these apart; read the report.

## Reproducibility provenance

A report identifies these inputs, and says so explicitly when a value is unknown (`null`):

| Input | Where | Notes |
| --- | --- | --- |
| Source snapshot | `proofs[].sourceSnapshot`, `experiment.sourceSnapshot`: `sha256` of the workspace manifest (paths, modes, file hashes) and the file count, taken after the copy and before the baseline | Identifies the copied files, including dirty and untracked ones. `null` when no workspace was prepared. Digests of the mutation target alone do not identify the project. |
| Source control state | `gitRevision`, `gitDirty` | Context only; the snapshot is the identifying value. |
| Detector or test configuration | `configDigest` over the exact harness bytes | Commands, arguments, policy IDs and selected tests. |
| Tools | `observations[].executable`: resolved path and SHA-256 of the file | A hash of one file. `null` when the program did not resolve; its `sha256` is `null` when unreadable. Libraries, interpreters and tool **versions** are not captured. `provider.toolVersion` is `null` unless a provider reports one. |
| Execution settings | `observations[].timeoutSeconds`, `envAllowlist` | Allowlisted names only. Environment values, the host's other state, clocks and network responses are not recorded. |

These fields make a run **identifiable and comparable**, not reproducible: Invaris does not promise that the same inputs give the same result, and hashes are not attestation.

## Inspecting the actual edit

A report alone does not show what was edited. The `mutation` record in a v1alpha2 proof (`proofs[].mutation`) and a v1alpha3 experiment (`experiment.mutation`) holds only `path`, `beforeSha256` and `afterSha256`: SHA-256 digests of the **whole target file** before and after the replacement. The report does not contain the replacement text or a diff, and the engine does not produce patch artifacts (`artifacts` stays `[]`). Inspecting the edit therefore needs the report **plus inputs the operator retained** outside the disposable workspace:

1. The report, kept with its `runId`.
2. The exact harness file bytes. Compute their SHA-256 and compare `sha256:<hex>` with `configDigest`. The digest covers the bytes, so reformatting equivalent JSON changes it. `harness` is only the configured name and does not locate an archived file.
3. The target file's bytes as they were during the run, including dirty or untracked content. A Git commit in `gitRevision` suffices only if the retrieved bytes match `beforeSha256`. Keeping the snapshot is the operator's responsibility; Invaris adds no archive and no retention period.

To check a declared edit, select the harness entry (`challenge`: the challenge whose `id` equals `proofs[].id`, read `file`, `before`, `after`; `mutate`: the experiment whose `id` and mutation `id` equal `experiment.id` and `experiment.mutationId`, read `path`, `before`, `after`). Require its path to equal the report's mutation path. Then, on a separate copy and without running any harness command:

1. Hash the retained target bytes and compare with `beforeSha256`.
2. Replace the one literal occurrence of `before` (encoded as UTF-8) with `after`, preserving the raw bytes and line endings of the rest of the file.
3. Hash the result and compare with `afterSha256`.

If both digests match, the retained inputs reproduce the recorded edit. If either does not, call the declared edit **unverified against that run**; do not guess or reinterpret the verdict. Cases that prevent verification:

- The retained harness does not match `configDigest`, or `configDigest` is `null`.
- The target changed before the replacement. In a `challenge`, a baseline command may have altered it, so the checkout can differ from the preimage; require the digest match rather than assuming equivalence.
- The target is missing. Retained postimage bytes show the resulting file, but do not prove the original or uniquely invert a replacement.
- A redacted path, identifier or digest.
- `mutation` is `null`: no edit was captured. A non-null mutation on a run that later failed records a change but implies no completed proof or conclusive verdict.

Limits. The retained harness and source are not covered by report redaction and can contain sensitive content; protect them under your own access policy. Observation excerpts are bounded and redacted diagnostics, not a patch. Digests identify content; they do not authenticate the report, its author, the tool or the execution. See the [threat model](threat-model.md#what-is-not-protected). Optional redacted patch artifacts are not part of the current contract.

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


## Mutation experiments

`mutate` emits [`invaris.evidence/v1alpha3`](../schemas/evidence-v1alpha3.schema.json) through the same stdout and optional `--report` path. Its `experiment` records the experiment and mutation IDs, engine version when known, replacement operator and target, test and validation command IDs, selected test IDs, before and after SHA-256 digests, ordered stage outcomes, and the final verdict and reason. Unattempted commands and an unapplied mutation appear as null. `reasonCategory` and `sourceSnapshot` have the meanings above. The digests cover the whole file, not the replacement text; see [inspecting the actual edit](#inspecting-the-actual-edit). Each attempted command links to a bounded, redacted observation with its exit code and timestamps. Parsed evidence is retained when available, including when workspace integrity or cleanup later fails.

| Verdict | Required evidence | Typical reason | Exit |
| --- | --- | --- | --- |
| `KILLED` | Healthy baseline, valid mutant, a selected assertion failure, and verified cleanup | `assertion_failed` | 0 |
| `SURVIVED` | Healthy baseline, valid mutant, all selected tests pass, and verified cleanup | `all_tests_passed` | 1 |
| `INVALID` | Healthy baseline, captured mutation, validity evaluator reports an invalid mutant, and verified cleanup | `mutant_invalid` | 2 |
| `INCONCLUSIVE` | Insufficient evidence or a failed execution precondition | For example `unhealthy_baseline`, `timeout`, `canceled`, `malformed_evidence`, or `cleanup_failed` | 2 |

The offline verification suite targets Linux and macOS with a POSIX shell and Go 1.27.4 or later. The [fixture harnesses](../examples/mutation) require no external analyzer. See the [threat model](threat-model.md) for command trust and stage-boundary integrity.
