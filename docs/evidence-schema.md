# Evidence reports

`run`, `check`, and `challenge` emit `invaris.evidence/v1alpha2`; [`mutate`](#mutation-experiments) emits v1alpha3; [`campaign`](#campaign-reports) emits v1alpha4. Each action prints one JSON report on stdout. `--report` also saves an identical copy to a new file.

CLI errors, such as a failed harness load or report save, go to stderr and exit 2 without a verdict. Check, proof, and gate diagnostics appear inside the report.

The [v1alpha2 schema](../schemas/evidence-v1alpha2.schema.json) uses JSON Schema Draft 2020-12 to validate report structure. Go validation (`evidence.Validate`) also checks that decisions agree with results and that required evidence is complete. Invalid evidence cannot produce a passing report.

Run `python3 scripts/validate-schemas.py` to validate CLI output and deliberate counterexamples. Install the pinned dependency with `pip install -r scripts/requirements-schema.txt` in a virtual environment; `jsonschema==4.23.0` requires Python 3.8 or newer. See [local checks](../CONTRIBUTING.md#local-checks).

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
| `steps`, `checks`, `proofs` | Results for each step, guard, and challenge. See [result details](#result-details). |
| `observations` | Execution records for steps, guards, proof stages, and provider discovery. See [result details](#result-details). |
| `targetCount` | Evaluated units: inspected targets for structured guards, one per legacy command or proof. `null` when unknown. |
| `decision` | `ACCEPT`, `REJECT` or `REFUSE`. Exit codes 0, 1, 2. |
| `diagnostics`, `artifacts` | Report-level diagnostics; persisted artifacts `{path, sha256, bytes}`. |

Unknown values are explicit `null`s, never omitted keys, and lists are `[]`, never `null`. Serialization is deterministic: fixed field order, no maps, two-space indent, trailing newline; the same populated report always yields identical bytes (run ID and times differ between runs).

## Result details

Checks use `PASS`, `FAIL`, `REFUSE`, or `NOT_RUN`. Proofs use `PROVEN`, `UNPROVEN`, `INCONCLUSIVE`, or `NOT_RUN`.

Each proof records its baseline and challenged check, `faultId`, `evidenceStrength`, `reason`, `reasonCategory`, readable `detail`, `sourceSnapshot`, and attempted `stages`. Its `mutation` is either `null` or an object with `path`, `beforeSha256`, and `afterSha256`. See [reasons](#reasons-and-categories), [proof stages](#proof-stages-and-cleanup), and [edit inspection](#inspecting-the-actual-edit).

Observations record the program, arguments, provider identity, scope, resolved executable and hash, effective timeout, allowlisted environment variable names, timestamps, exit code, bounded redacted stdout and stderr, and diagnostics. Each provider has one `discover` observation with identity, availability, and any discovery failure. Unknown executable details and versions use `null`.

## Decision rules

`ACCEPT` requires all of the following:

- Every step passed and at least one item was requested.
- Every required guard and proof is present once and has an acceptable result.
- No check failed and no proof is `UNPROVEN`.
- Weak `PROVEN` results are allowed by `allowWeakEvidence`.

Missing, duplicated, `NOT_RUN`, `REFUSE`, or `INCONCLUSIVE` required results cause refusal. Failures and refusals remain visible together. If both occur, `failure-first` precedence gives `REJECT`; `refusal-first` gives `REFUSE`.

An accepted report also needs `configDigest`, a positive `targetCount`, and inspected targets on every passing structured check unless its policy permits `allowEmpty`. Each proven challenge needs a passing baseline and the required challenged evidence. Strong proofs require exactly one finding matching the expected policy and target. An invalid `ACCEPT` becomes an `EVIDENCE_INCOMPLETE` refusal.

## Proof stages and cleanup

A proof uses one disposable workspace and attempts these stages in order:

| Stage | Work |
| --- | --- |
| `prepare` | Copy the project |
| `baseline` | Run the guard on the original copy |
| `mutate` | Replace the declared marker in the declared file |
| `challenged` | Run the same guard with the same scope |
| `cleanup` | Remove the copy and confirm it is absent |

Each attempted stage records `status` (`OK` or `FAILED`), `durationMs`, and failure `detail` when applicable. A failure skips later work, except cleanup, which always runs once a copy exists.

The workspace root and manifest must remain unchanged after the baseline. After the challenged check, only the engine's replacement may differ. A violation yields `INCONCLUSIVE` with reason `workspace_changed`. Before replacement, the target is revalidated as a regular, writable file without links and with exactly one marker occurrence. The engine compares manifests before and after replacement. `faultId` is derived from the policy, target, and before and after digests.

For a strong `PROVEN` result, the baseline must pass and the challenged check must fail with exactly one finding for the expected policy, native rule, and path. Extra or mismatched findings, zero inspected targets, and tool failures yield `INCONCLUSIVE`. A challenged pass yields `UNPROVEN`.

Cleanup finishes before the report is built. A cleanup failure yields `INCONCLUSIVE`; validation rejects `PROVEN` without a recorded target mutation and an `OK` cleanup stage.

`evidenceStrength` distinguishes `strong` structured findings from `weak` output-marker matches. Legacy command guards use their policy ID as the native rule ID. v1alpha1 harnesses permit weak proofs and record that choice in `compatibility.warnings`.

## Redaction and artifacts

Values of the environment variables named in `redactEnv` are replaced with `[REDACTED]` in every string of the report (excerpts, argv, diagnostics, findings) before serialization.

Matching is literal: overlapping matches are merged, longer secrets win, and a partial secret prefix cut off by an output size limit is also removed. Encoded, transformed or unconfigured secrets are **not** detected, nor are secrets in files the commands themselves write.

Protocol parsing sees the original bounded bytes; only the report is redacted. `redactEnv` values are never serialized. Excerpts are bounded to 4 KiB per stream (64 KiB with `storeSensitiveOutput`).

`evidence.PersistArtifact`/`PersistRedacted` write with exclusive creation (existing files are never overwritten), return the SHA-256 and size of exactly the persisted (redacted) bytes, and never hash the report into itself. The engine does not yet produce artifacts, so CLI reports currently have `"artifacts": []`.

## Reasons and categories

Read `proofs[].reason` or `experiment.reason` to identify why an outcome occurred. Both actions use the same reasons and categories. Guard verdicts use `PROVEN` and `UNPROVEN`; mutation verdicts use `KILLED`, `SURVIVED`, and `INVALID`. `reasonCategory` is derived from `reason` and checked during validation.

| Category | Meaning | Reasons |
| --- | --- | --- |
| `result` | A completed assessment | `guard_detected`, `marker_matched` (weak), `guard_passed`, `marker_not_found` (weak), `assertion_failed`, `all_tests_passed`, `mutant_invalid` |
| `setup` | The experiment as configured cannot give a meaningful answer; fix the harness or the project | `invalid_target`, `guard_not_found`, `provider_unavailable`, `unhealthy_baseline`, `mutation_rejected`, `no_targets_inspected`, `finding_mismatch` |
| `infrastructure` | The tools or machine failed; retry may help | `workspace_prepare_failed`, `timeout`, `canceled`, `missing_executable`, `output_limit`, `execution_failed`, `cleanup_failed` |
| `evidence` | The evidence or the workspace could not be trusted | `malformed_evidence`, `workspace_changed` |

A structured guard that exits 0 or 1 but whose output is rejected (unparsable, wrong policy, findings that disagree with the exit status, or fewer targets than `minInspected`) gives `malformed_evidence`. A guard that exits with any other status gives `execution_failed`. Before the next release, the exit-1 case was also reported as `execution_failed`.

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

Use these fields to identify and compare runs. Repeating a run can still produce different results because environment values, external state, and other dependencies may differ. Hashes identify recorded bytes; they do not authenticate an execution.

## Inspecting the actual edit

The `mutation` record contains `path`, `beforeSha256`, and `afterSha256`. These digests cover the whole target file before and after replacement. To recover the edit, retain these inputs outside the disposable workspace:

1. The report, kept with its `runId`.
2. The exact harness file bytes. Compute their SHA-256 and compare `sha256:<hex>` with `configDigest`. The digest covers the bytes, so reformatting equivalent JSON changes it. `harness` is only the configured name and does not locate an archived file.
3. The target file's bytes as they were during the run, including dirty or untracked content. A Git commit in `gitRevision` suffices only if the retrieved bytes match `beforeSha256`. Keep a copy of these bytes yourself; Invaris does not archive them.

Find the harness entry for the reported experiment:

- For `challenge`, match the challenge `id` to the proof `id`, then read `file`, `before`, and `after`.
- For `mutate`, match the experiment and mutation IDs to `experiment.id` and `experiment.mutationId`, then read the mutation's `path`, `before`, and `after`.

Require the configured path to match the reported mutation path. On a separate copy, without running harness commands:

1. Hash the retained target bytes and compare with `beforeSha256`.
2. Replace the one literal occurrence of `before` (encoded as UTF-8) with `after`, preserving the raw bytes and line endings of the rest of the file.
3. Hash the result and compare with `afterSha256`.

If both digests match, the retained inputs reproduce the recorded edit. If either does not, call the declared edit **unverified against that run**; do not guess or reinterpret the verdict. Cases that prevent verification:

- The retained harness does not match `configDigest`, or `configDigest` is `null`.
- The retained target differs from the file copied for the run. Check the digest, including any dirty or untracked content. Baseline workspace changes make the experiment inconclusive.
- The target is missing. Retained postimage bytes show the resulting file, but do not prove the original or uniquely invert a replacement.
- A redacted path, identifier or digest.
- `mutation` is `null`: no edit was captured. A non-null mutation on a run that later failed records a change but implies no completed proof or conclusive verdict.

Protect retained harnesses and source files as sensitive inputs. Report redaction does not cover them. Observation excerpts contain bounded diagnostics, while edit verification uses the retained bytes and digests. See the [threat model](threat-model.md#what-is-not-protected).

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

`mutate` emits [`invaris.evidence/v1alpha3`](../schemas/evidence-v1alpha3.schema.json) through the same stdout and optional `--report` path.

The `experiment` object records IDs for the experiment, mutation, commands, and selected tests. It also records the engine version when known, replacement operator and target, before and after digests, attempted stages, verdict, and reason.

Unattempted commands and an unapplied mutation appear as `null`. `reasonCategory` and `sourceSnapshot` have the meanings above. The digests cover the whole file, not the replacement text; see [inspecting the actual edit](#inspecting-the-actual-edit).

Each attempted command links to a bounded, redacted observation with its exit code and timestamps. Parsed evidence is retained when available, including when workspace integrity or cleanup later fails.

| Verdict | Required evidence | Typical reason | Exit |
| --- | --- | --- | --- |
| `KILLED` | Healthy baseline, valid mutant, a selected assertion failure, and verified cleanup | `assertion_failed` | 0 |
| `SURVIVED` | Healthy baseline, valid mutant, all selected tests pass, and verified cleanup | `all_tests_passed` | 1 |
| `INVALID` | Healthy baseline, captured mutation, validity evaluator reports an invalid mutant, and verified cleanup | `mutant_invalid` | 2 |
| `INCONCLUSIVE` | Insufficient evidence or a failed execution precondition | For example `unhealthy_baseline`, `timeout`, `canceled`, `malformed_evidence`, or `cleanup_failed` | 2 |

The offline verification suite targets Linux and macOS with a POSIX shell and Go 1.27.4 or later. The [fixture harnesses](../examples/mutation) require no external analyzer. See the [threat model](threat-model.md) for command trust and stage-boundary integrity.

## Campaign reports

See [Campaigns](campaigns.md) for behavior and examples.

`campaign` emits [`invaris.evidence/v1alpha4`](../schemas/evidence-v1alpha4.schema.json) through the same stdout and optional `--report` path. `--format text` and `--format compact` change stdout only; the report file is always JSON. Serialization follows the rules above: fixed field order, explicit `null`, `[]` for empty lists, declaration order for experiments and attempt order within them.

| Field | Meaning |
| --- | --- |
| `schemaVersion`, `runId`, `action` | `invaris.evidence/v1alpha4`, a random 128-bit hex ID, and `campaign` |
| `campaign`, `campaignConfigDigest` | The campaign `name` and `sha256:` of the exact campaign file bytes |
| `engineVersion` | Module version of the CLI; `null` for development builds |
| `sourceIdentity` | `snapshotSha256` and `files` of the frozen snapshot (`null` if it was never prepared); `roots`, one `{path, sha256, files}` per directory an experiment evaluates, sorted by path, where `"."` equals the whole snapshot; `gitRevision` and `gitDirty` of the campaign directory (`null` when unknown); and `excluded`, always `[".git", ".invaris"]` |
| `environment`, `startedAt`, `finishedAt`, `durationMs` | OS/architecture and UTC timing |
| `selection` | `mode` (`all` or `filtered`), the `--only` IDs, and the counts of selected and filtered experiments |
| `parallelism` | The `--parallel` value, 1 to 8 |
| `stages` | `snapshot`, then `cleanup` whenever a snapshot directory existed, each `OK` or `FAILED` with a detail |
| `tools` | One observation of kind `tool` (ID `tool:<id>`) per declared version command, with its executable, exit code and bounded output. Empty when none are declared or no snapshot was prepared |
| `experiments` | One entry per declared experiment; see below |
| `mutationSummary`, `guardrailSummary` | Counts per experiment, the mutation `score` and the guardrail `proofRate`, each with `eligible` and `excluded` and `null` when nothing is eligible. See [scores](campaigns.md#scores-and-summaries). |
| `acceptancePolicy` | The policy from the campaign file, with `requireNoInvalid` false and `minMutationScore` `null` when omitted |
| `acceptanceEvaluation` | `complete` and one `{requirement, status, experiments}` entry per enabled requirement |
| `decision` | `ACCEPT`, `REJECT` or `REFUSE`; exit 0, 1, 2 |
| `diagnostics`, `artifacts` | Report-level diagnostics such as `SNAPSHOT_FAILED`, `PREFLIGHT_PROVIDER`, `SNAPSHOT_CHANGED`, `CLEANUP_FAILED` and `CAMPAIGN_INCOMPLETE`; persisted artifacts (currently none) |

Each experiment records `id`, `kind`, `harness`, `root` (the harness's directory, `"."` for the campaign directory), `harnessDigest`, `referenceId`, `repeat`, `selection` (`SELECTED` or `FILTERED_OUT`), its `attempts`, and the aggregate: `stability` (`CONSISTENT`, `MIXED`, `INCOMPLETE`, `NOT_EVALUATED`), `aggregateVerdict` (only for `CONSISTENT`) and `stabilityReason`.

Each attempt records `id` (`<experiment>#<n>`), `index`, `execution` (`EXECUTED`, `CANCELED`, `REFUSED`, `NOT_STARTED`), `verdict`, `reason`, `reasonCategory`, `detail`, timing, and the child evidence of its kind: `mutation` holds a [v1alpha3 experiment record](#mutation-experiments) and `proof` holds a [v1alpha2 proof](#result-details). `observations` holds the observations that child evidence refers to. An attempt that did not start has no verdict, no child evidence and no observations.

Go validation (`evidence.ValidateCampaign`) rejects a report unless:

- IDs are unique, attempts are numbered `1..repeat` in order, filtered experiments have no attempts, and the selection agrees with the experiments.
- Executed and canceled attempts carry the child evidence of their kind and nothing else, and their verdict and reason equal the child's. A `CANCELED` attempt has reason `canceled`.
- Every child record passes the same rules as a single report: `KILLED` needs a completed assertion failure, every conclusive mutation verdict needs verified cleanup, and `PROVEN` needs a recorded mutation, a passing baseline, exactly one finding for the policy at the target, strong evidence and verified cleanup.
- Every experiment `root` is its harness's directory and has a digest in `sourceIdentity.roots`. Every child `sourceSnapshot` equals the digest of its experiment's root, and every conclusive child has one.
- `parallelism` is 1 to 8, and tool observations have kind `tool`, unique `tool:` IDs, and exist only with a prepared snapshot.
- Aggregates, both summaries, `acceptanceEvaluation.complete`, every requirement status and the decision equal the values recomputed from the attempts.

The digests identify content. They are not signatures and do not identify who ran the campaign.

