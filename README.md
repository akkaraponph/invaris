# Invaris

![Invaris banner: Harness execution and guardrail verification](assets/banners/github-readme-1600x480.png)

Invaris is an experimental Go CLI that runs project checks and tests whether those checks detect a deliberate file change. A JSON harness defines the commands, guards, and changes to test. Each run produces a JSON report.

Use `challenge` to test a guard against an expected violation, or `mutate` to test whether selected tests catch a code change. The `campaign` action runs several of these experiments together, with repeats, a mutation score and an acceptance policy. Commands run with your user permissions, so use trusted harnesses and tools. The Go core uses only the standard library; optional adapters connect external tools. See the [Dependency-Cruiser example](docs/dependency-cruiser-example.md).

Version [0.3.0](CHANGELOG.md#030), prepared for release. Until the tag is published, build from source or use the checked main-build artifacts described in the [release guide](docs/releasing.md).

## Install the CLI

Download the archive for your system from the [v0.3.0 release](https://github.com/akkaraponph/invaris/releases/tag/v0.3.0). Linux and macOS archives are available for `amd64` and `arm64`; each contains `invaris`, `invaris-command-provider`, the license, and a README. Go is not required to run these binaries.

See the [installation and terminal guide](docs/installation.md) for commands to:

- Download and extract the CLI into a folder you choose.
- Add it to Bash or Zsh so you can run `invaris` by name.
- Copy a runnable example into a chosen project folder and save a JSON report.
- Run from Windows CMD or PowerShell through WSL. This release has no native Windows archive.

### Use `invaris` from Bash

After following the [installation guide](docs/installation.md#install-into-a-specific-folder), add the installation folder to the current session:

```bash
export PATH="$HOME/.local/share/invaris:$PATH"
invaris
```

Replace the path if you installed elsewhere. `invaris` without an action prints usage and exits with status 2. See the guide for [persistent Bash and Zsh setup](docs/installation.md#run-from-bash-or-zsh) and [project examples](docs/installation.md#put-example-files-in-a-specific-project-folder).

For your own project, run `invaris init` to create a starter `invaris.json` (see [create and upgrade harnesses](docs/authoring.md)), or put a trusted `invaris.json` in the project directory, and run `invaris check`, `invaris run`, or `invaris challenge`. Pass a harness path as the last argument to select another file. Run `invaris --report report.json check invaris.json` to save the JSON report to a new file. See the [harness specification](docs/harness-spec.md) for configuration and the [mutation contract](docs/harness-spec.md#single-mutant-harness) for `mutate`.

## Quick start

Requires Go 1.27.4 or later and a POSIX shell for the bundled demonstration.

```sh
go test ./...
go run ./cmd/invaris check examples/invaris.json
go run ./cmd/invaris challenge examples/invaris.json
go run ./cmd/invaris run examples/invaris.json
```

These three commands emit `invaris.evidence/v1alpha2` JSON for v1alpha1 and v1alpha2 harnesses. Exit `0` means acceptance, `1` means a step or policy failure or an unproven challenge, and `2` means refusal, inconclusive execution, or a CLI or evidence error. Add `--format text` or `--format compact` after `check` or `challenge` for a human-readable summary; JSON stays the default and the `--report` file is always JSON (see [output formats](docs/harness-spec.md#output-formats)). See the [output migration](docs/evidence-schema.md#output-migration) for consumers of the previous format.

## Go project example

The [Go project example](examples/go-project/README.md) shows tests detecting and missing a code change, plus a guard catching a forbidden import. Its guide explains how to read each result.

## Campaign example

A [campaign](docs/campaigns.md) runs existing mutation and challenge harnesses in sequence from one frozen copy of the source and decides on the whole set:

```sh
go build -o /tmp/invaris ./cmd/invaris
cd examples/go-project
/tmp/invaris campaign --format text campaign.json        # six outcomes, REFUSE, exit 2
/tmp/invaris campaign campaign-accept.json               # ACCEPT, exit 0
```

The report is `invaris.evidence/v1alpha4`. Exit `0` means ACCEPT, `1` REJECT, and `2` REFUSE or an error. See the [Go project example](examples/go-project/README.md#run-a-campaign) for the expected results.

## Single-mutant example

Build the CLI and run the three offline POSIX fixtures:

```sh
go build -o /tmp/invaris ./cmd/invaris
/tmp/invaris mutate examples/mutation/killed.json
/tmp/invaris mutate examples/mutation/survived.json
/tmp/invaris mutate examples/mutation/invalid.json
```

The fixtures report `KILLED` (exit 0), `SURVIVED` (exit 1), and `INVALID` (exit 2), respectively. Each uses a v1alpha3 harness and emits v1alpha3 evidence. An unhealthy baseline or insufficient evidence reports `INCONCLUSIVE` (exit 2). See the [single-mutant harness contract](docs/harness-spec.md#single-mutant-harness) and [mutation evidence](docs/evidence-schema.md#mutation-experiments).

## How it works

The JSON harness describes ordered `steps`, policy checks called `guards`, and deliberate changes called `challenges`. Each command specifies an executable and literal arguments. To use shell syntax, configure a shell as the executable, as the bundled POSIX example does.

- `run`: execute steps in order; by default, stop on the first failed step, then run all guards.
- `check`: execute guards without steps.
- `challenge`: create a separate temporary workspace, run a clean baseline guard there, replace one expected marker, and require the selected guard to produce the expected finding for that policy, native rule, and file. Legacy marker-only guards can produce weak proofs.
- `mutate`: run selected tests on the original code, apply one replacement, validate the changed code, then run the same tests again.
- `campaign`: run several `mutate` and `challenge` experiments from one frozen source snapshot, repeat them, and evaluate an acceptance policy.

A challenge is `PROVEN` under the legacy weak output-marker contract only when the baseline passed and the mutated guard failed with the expected evidence. This does not establish a structured rule identity. A missing executable, timeout, workspace error, or ambiguous replacement is `INCONCLUSIVE`. A challenge that does not detect the injected violation is `UNPROVEN`.

### Example configuration

See [`examples/invaris.json`](examples/invaris.json) and [`examples/source.txt`](examples/source.txt). For an actual project, put `invaris.json` in the repository root and set the check commands to use that project's installed tools.

## Security and evidence limits

Run only trusted harnesses, executables, and analyzer configs. Commands have the invoking user's permissions and can access external paths and the network. `writablePaths` constrains the engine's replacement and `networkPolicy` declares unrestricted access. The engine caps command time and output, and the copier rejects symlinks and special files and limits copies to 2,000 entries and 10 MiB per file. See the [threat model](docs/threat-model.md).

## Architecture

See the [architecture guide](docs/architecture.md) for component responsibilities and execution flow, and the [roadmap](docs/roadmap.md) for future work. Scripts, CI jobs, and AI tools can invoke the CLI.

## Inspiration and related projects

Dependency-Cruiser, Redproof, Stryker, and Promptfoo [inspired Invaris](docs/provider-support.md). The [open-source ecosystem catalog](docs/ecosystem.md) lists related testing, guardrail, and security tools.

## Contributing

Develop changes on a topic branch and open a pull request targeting `staging`. Promote tested changes from `staging` to `main` with a pull request. Builds run only after pushes to `main`; verify the Linux, macOS and Dependency-Cruiser results on the merged commit before publishing. See the [contribution guide](CONTRIBUTING.md) for local validation and the [branch ruleset](https://github.com/akkaraponph/invaris/rules/27257768) for merge requirements.

## License

The source repository has an Apache-2.0 license. The optional [Dependency-Cruiser](docs/dependency-cruiser-example.md) tool uses the MIT license.

## Structured command guards

The [structured fixture](examples/structured/invaris.json) demonstrates exact policy and path matching with the [inspection contract](docs/inspection.md). Legacy guards still use exit status and optional weak output-marker proof.

Child commands and bare executable lookup use `/usr/bin:/bin` unless `PATH` is allowlisted. Lookup skips empty and relative PATH entries. Other environment variables are inherited only when allowlisted. Linux and macOS cancellation targets the command's process group. Detached descendants can escape it. Report redaction covers literal values explicitly configured in `redactEnv`.

Use `invaris --report new-file.json check examples/invaris.json` to save the same JSON emitted on stdout. The destination must be new and on a filesystem that supports hard links. A save error exits with status 2 before emitting a verdict.
