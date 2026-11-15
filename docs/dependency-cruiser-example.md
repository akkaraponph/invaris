# Dependency-Cruiser example

This example demonstrates the bundled JavaScript adapter with Dependency-Cruiser 18.7.2 (MIT). It was tested with real checks and an engine-managed proof on macOS arm64.

## Install and run

From the repository root, use Node.js 22.x, 24.x, or 26 and later with npm. The integration was tested with Node 26.8.2 and npm 11.19.1. Install into the adapter directory using the checked-in lockfile, then add that directory to `PATH`. Invaris runs the provider with the fixture as its working directory, and the provider locates its own installation by absolute path. The challenge's disposable copy contains only the fixture.

```sh
npm ci --prefix adapters/dependency-cruiser
export PATH="$PWD/adapters/dependency-cruiser:$PATH"
go run ./cmd/invaris check examples/dependency-cruiser/invaris.json
go run ./cmd/invaris challenge examples/dependency-cruiser/invaris.json
```

The check reports `PASS` with three inspected modules. The challenge replaces `./allowed.js` with `./forbidden.js` and reports `PROVEN` with the native finding `no-forbidden-import` at `src/main.js`. The challenged check records three modules and process exit 1.

## Configure the adapter

The adapter supports JavaScript and the `discover` and `check` operations.

- Set guard `scope` to project-relative source roots.
- Set `options` to `{"configPath": ".dependency-cruiser.cjs"}` or another project-relative rules file.
- Use a named rule declared directly in `forbidden` with severity `error`. Discovery advertises these rules; use the native rule name as the guard's `policyId`.
- Use one adapter declaration per rules configuration.

JavaScript rules files execute as trusted code during discovery and checking.

## Native reports and refusals

Dependency-Cruiser's built-in JSON reporter exits 0 for both a clean report and a policy violation. The bundled reporter plugin serializes the native result and uses its `summary.advisedExitCode` as the CLI process exit.

The adapter verifies that exit against the JSON summary and reads `summary.totalCruised`, `summary.violations[].rule.name`, and `summary.violations[].from`. Invalid native reports, unexpected exits, unrelated error rules, zero modules, and multiple errors (native exit greater than 1) produce refusals.

[Upstream CLI](https://github.com/sverweij/dependency-cruiser/blob/v18.7.2/doc/cli.md) · [Rules reference](https://github.com/sverweij/dependency-cruiser/blob/v18.7.2/doc/rules-reference.md) · [License](https://github.com/sverweij/dependency-cruiser/blob/v18.7.2/LICENSE)

## Verify the example

```sh
node --test adapters/dependency-cruiser/provider.test.mjs
go test -tags=integration ./internal/engine -run TestDependencyCruiserProof -count=1 -v
```

The tagged test requires the pinned installation and checks that the same named rule yields `UNPROVEN` when its target pattern matches no fixture file. It compares the original fixture's file hashes and modes before and after the run. The ordinary Go suite needs no Node installation or network access.

For an end-to-end CLI smoke run across the bundled examples, use `sh scripts/smoke.sh --with-dependency-cruiser` after installation. The pinned integration job runs this alongside the parser and tagged tests.
