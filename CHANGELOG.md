# Changelog

## Unreleased

- Add `reason` and `reasonCategory` to every proof and `reasonCategory` to mutation experiments, with one shared vocabulary (`result`, `setup`, `infrastructure`, `evidence`).
- Record input and execution details: `gitDirty`, a `sourceSnapshot` digest per proof and experiment, and per-observation `executable` (path and SHA-256), `timeoutSeconds` and `envAllowlist`. Unknown values are `null`.
- `challenge` now verifies workspace integrity across the baseline and challenged check, as `mutate` does. A guard that writes into the workspace now yields `INCONCLUSIVE` (`workspace_changed`) instead of a proof.
- The new report keys are required in the existing `invaris.evidence/v1alpha2` and `v1alpha3` schemas, which were published with 0.1.0. Consumers that validate strictly against the old schemas must update.

- Add [`examples/go-project`](examples/go-project/README.md): ordinary Go tests killing or missing a mutant, and a structured import guard proving a forbidden-dependency challenge.
- Add optional `preimageSha256` to the v1alpha3 mutation: a stale target is rejected before any write.
- Add optional `minInspected` to structured command guards: reports below the declared floor are refused.

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
