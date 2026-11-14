# Invaris

![Invaris banner: Harness execution and guardrail verification](assets/banners/github-readme-1600x480.png)

**Execution under invariants.**

Invaris is an experimental, standalone harness execution and guardrail-verification engine. It is designed for multi-language workflows and tool adapters. The initial implementation is a deliberately small Go CLI with a standard-library Go core and optional external analyzers.

> **Status: experimental local CLI.** Invaris runs trusted commands and provider guards and verifies controlled file-mutation proofs. See the [provider support matrix](docs/provider-support.md) for the tested integrations.

## Quick start

Requires Go 1.27.4 or later and a POSIX shell for the bundled demonstration.

```sh
go test ./...
go run ./cmd/invaris check examples/invaris.json
go run ./cmd/invaris challenge examples/invaris.json
go run ./cmd/invaris run examples/invaris.json
```

The commands emit `invaris.evidence/v1alpha2` JSON for both supported harness versions. Exit `0` means acceptance, `1` means a step or policy failure or an unproven challenge, and `2` means refusal, inconclusive execution, or a CLI or evidence error. See the [output migration](docs/evidence-schema.md#output-migration) for consumers of the previous format.

## How it works

The versioned JSON harness describes ordered `steps`, independent `guards`, and `challenges`. Each command specifies an executable and arguments; the engine does not implicitly invoke a shell. A command may explicitly invoke a shell, as the bundled POSIX example does. Treat all harness configuration as trusted code.

- `run`: execute steps in order; stop on first failed step, then run all guards.
- `check`: execute guards without steps.
- `challenge`: create a separate temporary workspace, run a clean baseline guard there, replace one expected marker, and require the selected guard to produce the expected finding for that policy, native rule, and file. Legacy marker-only guards can produce weak proofs.

A challenge is `PROVEN` under the legacy weak output-marker contract only when the baseline passed and the mutated guard failed with the expected evidence. This does not establish a structured rule identity. A missing executable, timeout, workspace error, or ambiguous replacement is `INCONCLUSIVE`. A challenge that does not detect the injected violation is `UNPROVEN`.

### Example configuration

See [`examples/invaris.json`](examples/invaris.json) and [`examples/source.txt`](examples/source.txt). For an actual project, put `invaris.json` in the repository root and set the check commands to use that project's installed tools.

## Security and evidence limits

Run only trusted harnesses, executables, and analyzer configs. Commands have the invoking user's permissions and can access external paths and the network. `writablePaths` constrains the engine's replacement and `networkPolicy` declares unrestricted access. The engine caps command time and output, and the copier rejects symlinks and special files and limits copies to 2,000 entries and 10 MiB per file. See the [threat model](docs/threat-model.md).

## Architecture direction

See [`docs/architecture.md`](docs/architecture.md) for runtime, adapter contracts, and guard lifecycle, and the [roadmap](docs/roadmap.md) for follow-up work. Invaris is intentionally independent from Panda Suite and can be called from any AI skill, CI job, or developer workflow.

## Contributing

Develop changes on a topic branch and open a pull request targeting `staging`. Promote tested changes from `staging` to `main` with a pull request. Both branches require the Linux, macOS, and Dependency-Cruiser checks. See the [contribution guide](CONTRIBUTING.md) for local validation and the [branch ruleset](https://github.com/akkaraponph/invaris/rules/27257768) for merge requirements.

## License

The source repository has an Apache-2.0 license. The optional [Dependency-Cruiser](docs/provider-support.md) tool uses the MIT license.

## Structured command guards

The [structured fixture](examples/structured/invaris.json) demonstrates exact policy and path matching with the [inspection contract](docs/inspection.md). Legacy guards still use exit status and optional weak output-marker proof.

Child commands and bare executable lookup use `/usr/bin:/bin` unless `PATH` is allowlisted. Lookup skips empty and relative PATH entries. Other environment variables are inherited only when allowlisted. Linux and macOS cancellation targets the command's process group. Detached descendants can escape it. Report redaction covers literal values explicitly configured in `redactEnv`.

Use `invaris --report new-file.json check examples/invaris.json` to save the same JSON emitted on stdout. The destination must be new and on a filesystem that supports hard links. A save error exits with status 2 before emitting a verdict.
