# Current implementation

Invaris runs trusted local commands in sequence. It supports five actions:

| Action | Harness version | Report version | Purpose |
| --- | --- | --- | --- |
| `run` | v1alpha1 or v1alpha2 | v1alpha2 | Run steps and guards |
| `check` | v1alpha1 or v1alpha2 | v1alpha2 | Run guards |
| `challenge` | v1alpha1 or v1alpha2 | v1alpha2 | Test whether a guard detects a declared violation |
| `mutate` | v1alpha3 | v1alpha3 | Test whether selected tests detect one declared code change |
| `campaign` | campaign v1alpha1 | v1alpha4 | Run several mutation and challenge experiments in sequence, with repeats, scores and an acceptance policy |

Harness versions use the `invaris.dev/` prefix, campaign files use `invaris.campaign/`, and reports use `invaris.evidence/`. The CLI prints a JSON report and can save an identical copy to a new file with `--report`.

## Experiments and results

A challenge runs a passing baseline, applies one replacement, and checks for the expected finding. Strong proofs match the policy, native rule, and target path. Legacy harnesses can accept weaker proofs based on output markers.

A mutation experiment runs the selected tests on the original code, applies one replacement, checks validity, and runs the same tests on a valid mutant. Offline fixtures demonstrate `KILLED`, `SURVIVED`, and `INVALID` results.

Both actions check workspace integrity between stages and verify cleanup before reporting a conclusive result. Commands must write caches and build products outside the disposable workspace. Reports include reasons, categories, source snapshot digests, Git state, and command settings. See [reasons](evidence-schema.md#reasons-and-categories) and [recorded inputs](evidence-schema.md#reproducibility-provenance).

Use `preimageSha256` to require an exact target file before a mutation. Use `minInspected` to require a minimum reported inspection count from a structured command guard. The engine still trusts tools to report their work truthfully.

Reports record whole-file digests for each edit. To inspect the edit later, retain the harness and original target bytes as described in [inspecting the actual edit](evidence-schema.md#inspecting-the-actual-edit). The CLI currently writes reports without additional artifact files.

## Campaigns

A [campaign](campaigns.md) references existing mutation and challenge harnesses anywhere under its directory, copies that directory once into a frozen snapshot, and runs each selected experiment up to five times from fresh copies of its harness's directory. The report keeps every attempt, marks disagreeing repeats as `MIXED`, computes a mutation score and a separate guardrail proof rate over consistent results only, records optional tool versions, and evaluates a typed acceptance policy. Incomplete evidence gives `REFUSE`. `--only` runs a subset and records the reduced scope, `--parallel N` runs attempts concurrently, and `--format text` prints a summary. The [Go project example](../examples/go-project/README.md) includes six outcomes and a deterministic mixed-result fixture.

## Providers and verification

Required providers are discovered before commands run. Offline fixtures cover the generic command bridge and Python protocol example. The [Dependency-Cruiser example](dependency-cruiser-example.md) documents the tested JavaScript adapter and its tool version.

The release workflow tests pull requests targeting `main`, and each push to `main` after a merge, on Linux and macOS using the Go version in `go.mod`. See the [workflow](../.github/workflows/ci.yml), [hosted results](https://github.com/akkaraponph/invaris/actions), and [contribution guide](../CONTRIBUTING.md).

Isolation, caching, and equivalent-mutant detection are future work; baseline reuse was declined. See the [roadmap](roadmap.md) and [execution limits](threat-model.md).
