# Changelog

## Unreleased

- Validate harnesses before direct execution as well as on load; reject ambiguous JSON and unsafe paths.
- Preserve v1alpha1 harness compatibility, including the shipped command and structured examples. Invalid direct calls that previously reached execution now return `REFUSE`.
- Preflight report destinations before executing commands. Publication remains subject to race-time failures.
- **Output migration:** the CLI now emits `invaris.evidence/v1alpha2` for both v1alpha1 and v1alpha2 harnesses; the v1alpha1 report (`results`, `status`) is no longer produced. See `docs/evidence-schema.md` for the field mapping. The exit code is now derived from the `decision` (`ACCEPT` 0, `REJECT` 1, `REFUSE` 2), with the same values as before.
- Add `invaris.dev/v1alpha2` harnesses (explicit trust mode, policy objects, gate with `failure-first`/`refusal-first` precedence, `redactEnv`, `storeSensitiveOutput`). Provider adapters and provider guards are accepted by the schema and refused by the loader until providers are executable. v1alpha1 harnesses are unchanged.
- Typed checks, proofs and gate decisions (`internal/domain`); evidence validation, deterministic serialization, redaction of configured secrets and exclusive artifact persistence (`internal/evidence`). Missing required results or incomplete evidence can no longer yield `ACCEPT`.
- Publish `schemas/harness-v1alpha2.schema.json` and `schemas/evidence-v1alpha2.schema.json`; `scripts/validate-schemas.py` validates real CLI output against them.
- Fix: `runner.Result.FinishedAt` was never set (and command durations were consequently wrong); it is now recorded on every path.
- Add the `invaris.adapter/v1alpha1` provider protocol (`internal/adapter`, `schemas/adapter-v1alpha1.schema.json`, `docs/adapter-protocol.md`): one bounded JSON request and one response per provider process over the shared runner, with strict validation of UTF-8, the single-value boundary, request ID, version, provider identity, finding scope and native exit semantics; a conformance test table, a provider conformance kit (`INVARIS_CONFORMANCE_PROVIDER`) and an independent Python example provider (`adapters/example/main.py`). The engine does not launch providers yet.
