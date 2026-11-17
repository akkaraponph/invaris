# Contributing

## Pull requests

1. Create a topic branch from `staging` in the repository or in your fork. Use the `panda-feature/` prefix for feature branches.
2. Run the local checks below and open a pull request targeting `staging`.
3. Keep the pull request up to date with its base branch. Resolve review conversations and pass local checks before merging.
4. To promote tested changes, open a pull request from `staging` to `main`. Hotfix pull requests can target `main` and must pass the same local checks.

The [CI workflow](.github/workflows/ci.yml) builds `main` only: it runs on every pull request to `main`, on every push to `main` (the merged result that a release is cut from), and on demand. Pull requests to `staging` and other branches are not built; run the local checks below before merging into `staging`. It checks formatting and `go vet`, runs the tests with and without the race detector on Linux and macOS, runs the offline smoke examples, validates the JSON Schemas against real CLI output, runs a short operator fuzz, exercises the real Dependency-Cruiser adapter. Runs for `main` also rehearse packaging: archives and the npm package are built once with a placeholder version `v0.0.0` and verified on Linux amd64, Linux arm64 and macOS arm64. Releases, packages and the GitHub Pages site come only from `main`. A new push to a pull request cancels its earlier run. Wait for every job to pass before merging. The [branch ruleset](https://github.com/akkaraponph/invaris/rules/27257768) requires pull requests and blocks force pushes and deletion on both branches; approvals are optional. To make merging depend on CI, add the CI jobs as required status checks to that ruleset.

## Local checks

Use Go 1.27.4 or later and a POSIX shell on Linux or macOS. Python 3 enables the independent adapter fixture and the schema checks.

```sh
go test ./...
go test -race ./...
go vet ./...
go test ./internal/mutation -run '^$' -fuzz '^FuzzPlan$' -fuzztime=10s
sh scripts/smoke.sh
python3 -m venv /tmp/invaris-schema-venv
/tmp/invaris-schema-venv/bin/python -m pip install -r scripts/requirements-schema.txt
/tmp/invaris-schema-venv/bin/python scripts/validate-schemas.py
```

`smoke.sh` sets `GOTOOLCHAIN=local`, so the Go on `PATH` must itself be 1.27.4 or later. The compatibility goldens in `cmd/invaris/testdata/compat` pin the default report shapes; refresh them with `go test ./cmd/invaris -run TestCompatibilityGoldens -update-compat` only for a deliberate contract change, and review the diff. The invalid mutation fixture normalizes only the known Bash/dash syntax diagnostic strings; its status, exit code and other output remain pinned. Text and compact output goldens are in `cmd/invaris/testdata/format` (`-update-format`).

Release archives link their version into the engine, so `engineVersion` reports it. Build all four platform archives, verify checksums and run the installed native binaries with Python 3.11 or later:

```sh
python3 scripts/package-release.py v0.3.0 --output dist/v0.3.0
python3 scripts/verify-release.py v0.3.0 dist/v0.3.0
```

The output directory must be new. CI runs these checks on Linux and macOS and uploads the checked archives and `SHA256SUMS`. See [release preparation](docs/releasing.md) for artifact download, publication gates and rollback.

For an adapter, start with the [protocol and conformance kit](docs/adapter-protocol.md#testing-a-third-party-provider). Provide clean, violating, and unevaluable fixtures. Run a positive proof and a surviving-mutation control with any real analyzer, and record the tool version and platform. See the [Dependency-Cruiser example](docs/dependency-cruiser-example.md) for the pinned Dependency-Cruiser installation and test commands.

## Security disclosure

Use **Report a vulnerability** on the repository's Security page if GitHub offers it. If it is unavailable, use the repository's Issues page to ask the owner, **@akkaraponph**, for a private contact method. Keep the public request free of vulnerability details, reproduction material, and affected data. [Security](https://github.com/akkaraponph/invaris/security) · [Issues](https://github.com/akkaraponph/invaris/issues) · [Repository owner](https://github.com/akkaraponph)
