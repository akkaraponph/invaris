# Changelog

## Unreleased (0.3.0 candidate)

Developer experience and trustworthy mutation verification. Default inputs, flags, report versions and exit codes (`0` ACCEPT, `1` REJECT, `2` REFUSE or error) are unchanged; everything below is additive or opt-in. Build with Go 1.27.4 or later.

### Added

- `invaris init` creates a starter `invaris.json` (`generic`, or `--preset go` with a `go test ./...` guard) and `invaris init --detect` reports project markers. It never runs project code or overwrites a file. See [create and upgrade harnesses](docs/authoring.md).
- `invaris migrate` converts a v1alpha1 harness to v1alpha2 with the same commands, writable targets, policies and effective gate (including weak marker evidence), refusing anything it cannot carry over. `--dry-run` previews; a real migration keeps an exact `.bak`, takes a lock and replaces atomically.
- `--format text|compact` for `check` and `challenge`, and `--format compact` for `campaign`. JSON stays the default and the `--report` file is always JSON. Output has no color or control sequences.
- `invaris.dev/v1alpha4` guard harnesses with built-in challenge operators `trailing-whitespace`, `crlf-injection` and `secret-leak-injection` (version `"1"`, pinned by `preimageSha256`), proven only with structured guard evidence. See the [operator catalog](docs/mutation-operators.md) and the [offline example](examples/operators/README.md).
- `invaris.evidence/v1alpha5`, a portable profile for every action: built-in mutation metadata with a stable reproduction identity, command digests, the engine version, per-guard and per-operator campaign effectiveness, and a privacy projection that withholds paths, arguments, output and free text. Automatic for v1alpha4 inputs; `--evidence-version v1alpha5` for others. See the [evidence reference](docs/evidence-schema.md#portable-profile-v1alpha5).
- Schemas [`harness-v1alpha4`](schemas/harness-v1alpha4.schema.json) and [`evidence-v1alpha5`](schemas/evidence-v1alpha5.schema.json). Older schemas are unchanged.

### Changed

- Workspace copies stop on cancellation between entries and 1 MiB chunks, remove the partial copy, and report `canceled` or `timeout` instead of `workspace_prepare_failed`.
- Release builds can link their version (`-X github.com/akkaraponph/invaris/internal/engine.releaseVersion=v0.3.0`), which `engineVersion` then reports.
- CI also runs a 10-second operator fuzz smoke and cross-builds both executables for Linux and macOS on amd64 and arm64.

### Compatibility notes

- Reports from a migrated harness say compatibility `native`, and its config digest changes.
- v1alpha5 consumers must read new fields; older consumers keep receiving the older versions unless they opt in. A v1alpha4 harness always produces v1alpha5, and an older binary refuses it.
- To undo a migration, review and restore the `.bak` file by hand.

## 0.2.0

Invaris 0.2.0 adds campaigns: several mutation and guardrail experiments run together from one frozen copy of the source, with repeats, separate mutation and guardrail scores, and an explicit acceptance decision. Build with Go 1.27.4 or later. The offline examples use a POSIX shell.

### Highlights

- Run `invaris campaign` with an [`invaris.campaign/v1alpha1`](schemas/campaign-v1alpha1.schema.json) file. A campaign references existing v1alpha3 mutation experiments and v1alpha2 structured challenges anywhere under its directory, copies that directory once into a frozen snapshot, and runs each selected experiment up to five times from fresh copies. Reports use [`invaris.evidence/v1alpha4`](schemas/evidence-v1alpha4.schema.json). See [Campaigns](docs/campaigns.md).
- Every attempt and its full child evidence is kept. Repeats are `CONSISTENT`, `MIXED` or `INCOMPLETE`; there is no voting.
- The mutation score (`killed / (killed + survived)`) and the guardrail proof rate (`proven / (proven + unproven)`) count consistent experiments once each and are `null` when nothing is eligible. They are never combined.
- A typed acceptance policy (`requireNoSurvivors`, `requireAllGuardrailsProven`, `requireNoInconclusive`, `requireNoMixed`, optional `requireNoInvalid` and `minMutationScore`) decides `ACCEPT`, `REJECT` or `REFUSE`. Incomplete or contradictory evidence refuses before any reject.
- CLI options: `--only ID` runs a recorded subset, `--format text` prints a summary, and `--parallel N` (1 to 8, default 1) runs attempts concurrently. Exit codes stay `0` ACCEPT, `1` REJECT, `2` REFUSE or error.
- Source identity: the snapshot manifest digest, one digest per evaluated directory, Git revision and dirty state. Optional campaign `tools` record tool versions such as `go version`.
- Cancellation stops further attempts, records canceled and unstarted work, removes the snapshot, and still writes a valid partial report.

### Evidence and compatibility

- `run`, `check`, `challenge` and `mutate` keep their inputs, exit codes and report versions.
- Proofs gain `reason` and `reasonCategory`, and mutation experiments gain `reasonCategory`, with one shared vocabulary (`result`, `setup`, `infrastructure`, `evidence`).
- Reports record `gitDirty`, a `sourceSnapshot` digest per proof and experiment, and per-observation `executable` (path and SHA-256), `timeoutSeconds` and `envAllowlist`. Unknown values are `null`.
- **Schema update:** these keys are required in the `invaris.evidence/v1alpha2` and `v1alpha3` schemas published with 0.1.0. Consumers that validate strictly against the 0.1.0 schemas must update.
- **Behaviour change in `challenge`:** a guard that writes into the workspace now yields `INCONCLUSIVE` (`workspace_changed`) instead of a proof. A structured guard that exits 0 or 1 with rejected output now gives reason `malformed_evidence` instead of `execution_failed`; the verdict stays `INCONCLUSIVE`.
- Add optional `preimageSha256` to the v1alpha3 mutation (a stale target is rejected before any write) and optional `minInspected` to structured command guards.

### Examples and verification

- Add [`examples/go-project`](examples/go-project/README.md): Go tests killing or missing a mutant, an import guard proving a challenge, and campaigns with six outcomes and a deterministic mixed-result fixture.
- Add campaigns for the shell mutation and forbidden-dependency examples. `scripts/smoke.sh` and `scripts/validate-schemas.py` cover every campaign file and report.
- Add a [design note](docs/decisions/0001-campaign-engine.md) for the campaign decisions, including why execution is not sandboxed and baselines are not reused.

## 0.1.0

Invaris runs trusted local workflows and turns guard challenges and single-mutant experiments into versioned JSON evidence. Build with Go 1.27.4 or later. The offline examples use a POSIX shell.

### Highlights

- Run a single explicit replacement with `invaris mutate`. The v1alpha3 harness declares the writable target, validity command, test command, and selected test IDs. The v1alpha3 report records test and validity evidence, before and after digests, and cleanup.
- Mutation exits are `KILLED` (0), `SURVIVED` (1), and `INVALID` or `INCONCLUSIVE` (2). A kill requires a passing baseline and a structured assertion failure on the valid mutant.
- Reject overlapping replacement matches and install replacement bytes without writing through hard links. The scoped forbidden-dependency fixture checks the expected policy, rule, and target.
- See the [single-mutant harness](docs/harness-spec.md#single-mutant-harness), [mutation evidence](docs/evidence-schema.md#mutation-experiments), and [execution limits](docs/threat-model.md).

### Execution, adapters, and compatibility

- Validate harnesses before direct execution as well as on load; reject ambiguous JSON and unsafe paths.
- Preserve v1alpha1 harness compatibility, including the shipped command and structured examples. Invalid direct calls that previously reached execution now return `REFUSE`.
- Preflight report destinations before executing commands. Publication remains subject to race-time failures.
- **Output migration:** the CLI now emits `invaris.evidence/v1alpha2` for both v1alpha1 and v1alpha2 harnesses; the v1alpha1 report (`results`, `status`) is no longer produced. See `docs/evidence-schema.md` for the field mapping. The exit code is now derived from the `decision` (`ACCEPT` 0, `REJECT` 1, `REFUSE` 2), with the same values as before.
- Add `invaris.dev/v1alpha2` harnesses (explicit trust mode, policy objects, gate with `failure-first`/`refusal-first` precedence, `redactEnv`, `storeSensitiveOutput`). v1alpha1 harnesses are unchanged.
- Typed checks, proofs and gate decisions (`internal/domain`); evidence validation, deterministic serialization, redaction of configured secrets and exclusive artifact persistence (`internal/evidence`). Missing required results or incomplete evidence can no longer yield `ACCEPT`.
- Publish `schemas/harness-v1alpha2.schema.json` and `schemas/evidence-v1alpha2.schema.json`; `scripts/validate-schemas.py` validates real CLI output against them.
- Fix: `runner.Result.FinishedAt` was never set (and command durations were consequently wrong); it is now recorded on every path.
- Add the [provider protocol](docs/adapter-protocol.md): one bounded JSON request and response per provider process. Validate UTF-8, single JSON values, request IDs, versions, provider identity, finding scope, and native exits. Include conformance tests, an external-provider test kit (`INVARIS_CONFORMANCE_PROVIDER`), and a Python example (`adapters/example/main.py`).
- Run v1alpha2 adapters, provider guards, and `requiredAdapters`. Discover each used provider once before steps run. Missing required capabilities refuse the run; unsupported optional guards are `NOT_RUN`. Provider failures, protocol errors, and identity changes cause refusal. Reports record discovery and provider identity. Execution order is steps, command guards, then provider guards.
- Add `invaris-command-provider`, a generic structured-command provider (`cmd/invaris-command-provider`) that preserves the command's native exit status and inspected counts and validates evidence, plus `examples/provider/invaris.json`.
- Add `internal/workspace` for restricted copying, one-file replacement with digests and manifest checks, and verified cleanup. Baseline and challenged checks use the same evaluator and copy. Revalidate the target after the baseline. Strong proofs require one finding matching the policy, rule, and path. Reports add `detail`, `mutation`, and `stages`; `PROVEN` requires a recorded mutation and an `OK` cleanup stage. Document the [threat model](docs/threat-model.md).
- `adapter.Call` now always sends `policyIds` and `scope` as arrays, never `null`.
- Verify Dependency-Cruiser 18.7.2 (MIT) end to end, including an ineffective-rule control, using the adapter lockfile.
- Add subprocess exit-contract tests and offline smoke checks. The release workflow targets Go 1.27.4 on Linux and macOS and runs pinned Dependency-Cruiser separately.
- Add a related-projects catalog of public open-source repositories.
