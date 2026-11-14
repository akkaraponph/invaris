# Invaris architecture

Invaris is a local Go CLI. The engine validates a JSON harness before dispatch, runs steps and guards serially, and evaluates the resulting evidence against the action's gate.

- `cmd/invaris` owns CLI arguments, exit codes, and exclusive report publication.
- `internal/engine` owns harness decoding, scheduling, provider negotiation and proof orchestration.
- `internal/runner` runs bounded subprocesses with explicit argv and an environment allowlist.
- `internal/adapter` validates provider requests and responses across a subprocess boundary. Providers translate their tools' own reports into typed checks.
- `internal/workspace` copies a checkout, performs the controlled replacement, and verifies cleanup.
- `internal/domain` evaluates typed results and gate requirements. `internal/evidence` validates, redacts, and serializes the public report.

A check can pass, fail, refuse, or remain unrun. A proof requires a passing baseline and a challenged finding that matches the expected policy, native rule, and file. Legacy marker proofs carry weak evidence. A guard that misses the challenged fault is unproven. An unevaluable challenge is inconclusive. A gate can choose failure-first or refusal-first when both outcomes are present in one report.

The CLI emits `invaris.evidence/v1alpha2` and maps `ACCEPT`, `REJECT`, and `REFUSE` to exits 0, 1, and 2. See the [evidence schema](evidence-schema.md), [adapter protocol](adapter-protocol.md), [threat model](threat-model.md), and [roadmap](roadmap.md).


The `mutate` action reads a v1alpha3 harness. It shares the runner, workspace copier, replacement, cleanup, and evidence pipeline. The engine runs the selected baseline tests, applies a single replacement, runs the validity evaluator, and runs the same selected tests on a valid mutant. Mutation verdicts use a separate model and are published in the v1alpha3 report. See [mutation experiments](evidence-schema.md#mutation-experiments).
