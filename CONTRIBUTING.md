# Contributing

## Pull requests

1. Create a topic branch from `staging` in the repository or in your fork. Use the `panda-feature/` prefix for feature branches.
2. Run the local checks below and open a pull request targeting `staging`.
3. Keep the pull request up to date with its base branch and resolve review conversations. Merge when the three required checks pass.
4. To promote tested changes, open a pull request from `staging` to `main`. Hotfix pull requests can target `main` and must pass the same checks.

The Release checks workflow builds and tests only pull requests targeting `staging` or `main`. GitHub requires `Go checks (ubuntu-latest)`, `Go checks (macos-latest)`, and `dependency-cruiser`. The [branch ruleset](https://github.com/akkaraponph/invaris/rules/27257768) blocks direct pushes, force pushes, and deletion on both branches. Approvals are optional.

## Local checks

Use Go 1.27.4 or later and a POSIX shell on Linux or macOS. Python 3 enables the independent adapter fixture and the schema checks.

```sh
go test ./...
go test -race ./...
go vet ./...
sh scripts/smoke.sh
python3 -m venv /tmp/invaris-schema-venv
/tmp/invaris-schema-venv/bin/python -m pip install -r scripts/requirements-schema.txt
/tmp/invaris-schema-venv/bin/python scripts/validate-schemas.py
```

For an adapter, start with the [protocol and conformance kit](docs/adapter-protocol.md#testing-a-third-party-provider). Provide clean, violating, and unevaluable fixtures. Run a positive proof and a surviving-mutation control with any real analyzer, and record the tool version and platform. See [provider support](docs/provider-support.md) for the pinned Dependency-Cruiser installation and test commands.

## Security disclosure

Use **Report a vulnerability** on the repository's Security page if GitHub offers it. If it is unavailable, use the repository's Issues page to ask the owner, **@akkaraponph**, for a private contact method. Keep the public request free of vulnerability details, reproduction material, and affected data. [Security](https://github.com/akkaraponph/invaris/security) · [Issues](https://github.com/akkaraponph/invaris/issues) · [Repository owner](https://github.com/akkaraponph)
