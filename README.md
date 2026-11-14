# Invaris

**Execution under invariants.**

Invaris is an experimental, standalone harness execution and guardrail-verification engine. It is designed for multi-language workflows and tool adapters. The initial implementation is a deliberately small Go CLI with **no third-party dependencies**.

> **Status: v0 prototype.** Invaris currently executes explicit local commands and tests one class of controlled file-mutation proof. It is NOT a secure sandbox, a general-purpose agent runtime, or an implementation of the full proposed universal adapter protocol.

## Quick start

Requires Go 1.23+ and a POSIX shell for the bundled demonstration.

```sh
go test ./...
go run ./cmd/invaris check examples/invaris.json
go run ./cmd/invaris challenge examples/invaris.json
go run ./cmd/invaris run examples/invaris.json
```

The commands print a structured JSON report to stdout and return exit status `0` for a successful action, `1` for a detected failure, and `2` for an inconclusive/refused execution or configuration error.

## How it works

The versioned JSON harness describes ordered `steps`, independent `guards`, and `challenges`. Each command specifies an executable and arguments; the engine does not implicitly invoke a shell. A command may explicitly invoke a shell, as the bundled POSIX example does. Treat all harness configuration as trusted code.

- `run`: execute steps in order; stop on first failed step, then run all guards.
- `check`: execute guards without steps.
- `challenge`: create a separate temporary workspace, run a clean baseline guard there, replace one expected marker, and require that the *selected guard* exits unsuccessfully with the configured output marker.

A challenge is `PROVEN` under the legacy weak output-marker contract only when the baseline passed and the mutated guard failed with the expected evidence. This does not establish a structured rule identity. A missing executable, timeout, workspace error, or ambiguous replacement is `INCONCLUSIVE`. A challenge that does not detect the injected violation is `UNPROVEN`.

### Example configuration

See [`examples/invaris.json`](examples/invaris.json) and [`examples/source.txt`](examples/source.txt). For an actual project, put `invaris.json` in the repository root and set the check commands to use that project's installed tools.

## Security and evidence limits

**Do not run untrusted harness files.** Local commands execute with the invoking user's privileges. Challenge workspaces are disposable copies, **not security sandboxes**. A command can still access the network, environment variables, absolute paths, and external services. Do not target production systems. The prototype has command timeouts and output caps but does not yet implement full process-tree termination, hermetic dependency resolution, rigorous resource quotas, or an attested evidence store.

The initial proof is an *output-marker correlation*, not a guarantee that a particular static-analysis rule caused the failure. Strong future adapters should return structured rule IDs and target-level evidence. The workspace copier rejects symlinks and files larger than 10 MiB; large repositories and unusual file layouts are not supported. Challenges have no source-tree write-back.

## Architecture direction

See [`docs/architecture.md`](docs/architecture.md) for the intended runtime, adapter contracts, guard lifecycle, and next milestones. Invaris is intentionally independent from Panda Suite and can be called from any AI skill, CI job, or developer workflow.

## License

The source repository has an Apache-2.0 license. Keep its existing LICENSE and notices when publishing modifications.

## Structured command guards

The [structured fixture](examples/structured/invaris.json) demonstrates exact policy and path matching with the [inspection contract](docs/inspection.md). Legacy guards still use exit status and optional weak output-marker proof.

Child commands receive a default `PATH` of `/usr/bin:/bin` and only variables named by their `envAllowlist`. Executable lookup for a bare program name still uses the launching process's search path. Cancellation signals the command's process group on macOS and Linux. Detached descendants can escape that group. Command output remains unredacted.

Use `invaris --report new-file.json check examples/invaris.json` to save the same JSON emitted on stdout. The destination must be new and on a filesystem that supports hard links. A save error exits with status 2 before emitting a verdict.
