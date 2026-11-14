# Structured command guards

Set a guard's `policyId` to opt into structured inspection. The executable receives its declared arguments and runs from the harness workspace. It writes exactly one JSON object to stdout. Stderr cannot supply proof evidence.

```json
{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":1,"findings":[]}
```

Exit 0 requires an empty findings array. Exit 1 requires at least one finding. Each finding has a matching `policyId` and a relative `path`. An invalid response, an inconsistent exit code, or zero inspected targets refuses the check. A challenge requires a clean baseline and exactly one finding at the mutated path. A passing challenged check is unproven.

This contract trusts the declared command to report its inspection truthfully. It supports one policy per command. Provider discovery, capability negotiation, and tool-specific integration are still planned. Run `go run ./cmd/invaris challenge examples/structured/invaris.json` for the offline fixture.
