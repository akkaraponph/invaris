# Invaris architecture

Invaris validates a JSON harness, runs its commands, and writes a JSON report. Steps and guards run in sequence. A gate defines which results the action requires and how failures and refusals affect its decision.

## Components

| Component | Responsibility |
| --- | --- |
| `cmd/invaris` | Parse CLI arguments, select exit codes, and save reports without overwriting files |
| `internal/engine` | Load harnesses, schedule work, discover providers, and run challenges and mutation experiments |
| `internal/runner` | Run subprocesses with time and output limits, literal arguments, and an environment allowlist |
| `internal/adapter` | Validate requests and responses exchanged with provider processes |
| `internal/workspace` | Copy the project, apply one replacement, and verify cleanup |
| `internal/domain` | Define results and evaluate gate requirements |
| `internal/evidence` | Validate, redact, and serialize reports |

Providers translate external tool reports into typed checks. The [adapter protocol](adapter-protocol.md) defines their requests, responses, and error handling.

## Checks and challenges

A check reports `PASS`, `FAIL`, `REFUSE`, or `NOT_RUN`. A structured challenge requires a passing baseline and one finding that matches the expected policy, native rule, and changed file. A challenged guard that passes yields `UNPROVEN`; a tool failure or unusable evidence yields `INCONCLUSIVE`. Legacy marker proofs use weaker evidence based on command output.

The gate evaluates the results for the requested action. When failures and refusals occur together, its `precedence` selects which determines the decision. Reports use `ACCEPT`, `REJECT`, and `REFUSE`, mapped to exit codes 0, 1, and 2. These actions emit `invaris.evidence/v1alpha2`.

## Mutation experiments

The `mutate` action reads a v1alpha3 harness and emits a v1alpha3 report. It runs the selected baseline tests, applies one replacement, runs the validity evaluator, and runs the same tests on a valid mutant. Its verdicts are `KILLED`, `SURVIVED`, `INVALID`, and `INCONCLUSIVE`.

Challenges and mutation experiments share the runner, workspace copier, replacement operation, integrity checks, cleanup, and report processing. They use separate result models and a shared set of [reasons and categories](evidence-schema.md#reasons-and-categories).

See the [harness specification](harness-spec.md), [evidence schema](evidence-schema.md), [threat model](threat-model.md), and [roadmap](roadmap.md).
