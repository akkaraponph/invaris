# Current implementation

Invaris runs trusted local commands in sequence. It supports five verification actions, two authoring commands, two diagnostic commands and a test-report converter:

| Action | Harness version | Report version | Purpose |
| --- | --- | --- | --- |
| `run` | v1alpha1, v1alpha2 or v1alpha4 | v1alpha2 (v1alpha5 for v1alpha4 or on request) | Run steps and guards |
| `check` | v1alpha1, v1alpha2 or v1alpha4 | v1alpha2 (v1alpha5 for v1alpha4 or on request) | Run guards |
| `challenge` | v1alpha1, v1alpha2 or v1alpha4 | v1alpha2 (v1alpha5 for v1alpha4 or on request) | Test whether a guard detects a declared violation or built-in operator edit |
| `mutate` | v1alpha3 | v1alpha3 (v1alpha5 on request) | Test whether selected tests detect one declared code change |
| `campaign` | campaign v1alpha1 | v1alpha4 (v1alpha5 with a v1alpha4 harness or on request) | Run several mutation and challenge experiments in sequence, with repeats, scores and an acceptance policy |
| `init` | writes v1alpha2 | none | Detect project markers and create a starter harness without running project code |
| `migrate` | v1alpha1 to v1alpha2 | none | Upgrade a harness with the same behavior, keeping a backup |
| `doctor` | any harness version | none (JSON `invaris.doctor/v1alpha1`) | Inspect a harness's setup without running any of its commands |
| `explain` | report v1alpha2 to v1alpha5 | none (JSON `invaris.explanation/v1alpha1`) | Interpret a saved report offline: detected, missed or inconclusive, and what the report cannot show |
| `test-result` | none | none (JSON `invaris.test-result/v1alpha1`) | Convert `go test -json`, jest or vitest JSON, or pytest JUnit XML into the result object a mutation test command prints; a run that fails outside an assertion exits 2 and never counts as a killed mutant |

`invaris --version` prints the linked release, and `invaris --help` prints usage and exits 0. v1alpha3 mutation harnesses accept the `replace` and `delete` operators. `check`, `challenge` and `campaign` can print `--format text` or `compact` summaries; JSON is the default. [`doctor` and `explain`](diagnostics.md) print text by default; neither runs a harness command, and neither writes a report.

Harness versions use the `invaris.dev/` prefix, campaign files use `invaris.campaign/`, and reports use `invaris.evidence/`. The CLI prints a JSON report and can save an identical copy to a new file with `--report`.

## Experiments and results

A challenge runs a passing baseline, applies one replacement, and checks for the expected finding. Strong proofs match the policy, native rule, and target path. Legacy harnesses can accept weaker proofs based on output markers.

A mutation experiment runs the selected tests on the original code, applies one replacement, checks validity, and runs the same tests on a valid mutant. Offline fixtures demonstrate `KILLED`, `SURVIVED`, and `INVALID` results.

Both actions check workspace integrity between stages and verify cleanup before reporting a conclusive result. Commands must write caches and build products outside the disposable workspace. Reports include reasons, categories, source snapshot digests, Git state, and command settings. See [reasons](evidence-schema.md#reasons-and-categories) and [recorded inputs](evidence-schema.md#reproducibility-provenance).

A v1alpha4 challenge can name a [built-in operator](mutation-operators.md) (trailing whitespace, CRLF injection or a synthetic secret line) instead of literal `before` and `after` text; it requires a structured guard and a `preimageSha256`. Use `preimageSha256` to require an exact target file before a mutation. Use `minInspected` to require a minimum reported inspection count from a structured command guard. The engine still trusts tools to report their work truthfully.

Reports record whole-file digests for each edit. To inspect the edit later, retain the harness and original target bytes as described in [inspecting the actual edit](evidence-schema.md#inspecting-the-actual-edit). The CLI currently writes reports without additional artifact files.

## Campaigns

A [campaign](campaigns.md) references existing mutation and challenge harnesses anywhere under its directory, copies that directory once into a frozen snapshot, and runs each selected experiment up to five times from fresh copies of its harness's directory. The report keeps every attempt, marks disagreeing repeats as `MIXED`, computes a mutation score and a separate guardrail proof rate over consistent results only, records optional tool versions, and evaluates a typed acceptance policy. Incomplete evidence gives `REFUSE`. `--only` runs a subset and records the reduced scope, `--parallel N` runs attempts concurrently, and `--format text` prints a summary. The [Go project example](../examples/go-project/README.md) includes six outcomes and a deterministic mixed-result fixture.

## Providers and verification

Required providers are discovered before commands run. Offline fixtures cover the generic command bridge and Python protocol example. The [Dependency-Cruiser example](dependency-cruiser-example.md) documents the tested JavaScript adapter and its tool version.

CI runs on pull requests to `staging` and `main` and on pushes to `main`, on Linux and macOS using the Go version in `go.mod`, and rehearses packaging on every run. Pushing a version tag runs the same checks, builds and natively verifies the archives, and publishes the GitHub release. See the [CI workflow](../.github/workflows/ci.yml), the [release workflow](../.github/workflows/release.yml), [hosted results](https://github.com/akkaraponph/invaris/actions), and the [release guide](releasing.md).

The [portable v1alpha5 profile](evidence-schema.md#portable-profile-v1alpha5) withholds paths, arguments, output and free text, and adds command digests, reproduction identities for built-in edits and campaign effectiveness rows. Isolation, caching, semantic (AST) operators and equivalent-mutant detection are future work; baseline reuse was declined. See the [roadmap](roadmap.md) and [execution limits](threat-model.md).
