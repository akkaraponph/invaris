# Changelog

## Unreleased

- Validate harnesses before direct execution as well as on load; reject ambiguous JSON and unsafe paths.
- Preserve v1alpha1 harness compatibility, including the shipped command and structured examples. Invalid direct calls that previously reached execution now return `REFUSE`.
- Preflight report destinations before executing commands. Publication remains subject to race-time failures.
