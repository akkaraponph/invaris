# Current implementation

The release slice supports `run`, `check`, and `challenge` for trusted local harnesses. It reads `invaris.dev/v1alpha1` and `invaris.dev/v1alpha2`, emits typed `invaris.evidence/v1alpha2` JSON, and can publish an exact copy to a new report path. The CLI retains separate check outcomes even when a gate must choose one exit status.

The CLI also supports `mutate` with a [v1alpha3 harness](harness-spec.md#single-mutant-harness). It evaluates one explicit replacement, requires completed validity and test evidence, and emits a separate mutation verdict in the v1alpha3 report. The offline fixtures cover killed, surviving, and invalid mutants.

Required providers are discovered before dispatch. Strong proofs record a passing baseline, a single controlled replacement and its hashes, matching challenged evidence, and verified cleanup. The legacy harness accepts weak marker proofs for compatibility.

The generic command bridge and Python protocol example are covered by offline fixtures. Dependency-Cruiser 18.7.2 is the verified real analyzer. See [provider support](provider-support.md) for its platform and language coverage. The release workflow builds and tests pull requests targeting `staging` or `main` on Linux and macOS with the Go version in `go.mod`. See [GitHub Actions](https://github.com/akkaraponph/invaris/actions) for hosted results and the [contribution guide](../CONTRIBUTING.md) for merge requirements.

Execution is trusted-local and serial. The report has an artifact field and the library supports exclusive artifact persistence. The CLI does not produce additional artifact files. See the [threat model](threat-model.md) and [roadmap](roadmap.md) for the release boundary and follow-up priorities.
