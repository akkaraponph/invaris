# Contributing

Use Go 1.23 or later and a POSIX shell on Linux or macOS. Python 3 enables the independent adapter fixture and the schema checks.

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
