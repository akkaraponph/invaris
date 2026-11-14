# Structured command guards

Set a guard's `policyId` to opt into structured inspection. The executable receives its declared arguments and runs from the harness workspace. It writes exactly one JSON object to stdout. Stderr cannot supply proof evidence.

```json
{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":1,"findings":[]}
```

Exit 0 requires an empty findings array. Exit 1 requires at least one finding. Each finding has a matching `policyId` and a relative `path`. An invalid response, an inconsistent exit code, or zero inspected targets refuses the check. A challenge runs the baseline and the challenged check with the same guard in one disposable copy of the checkout, so scope and evaluator cannot differ. It requires a clean baseline that inspected at least one target and, after the engine replaces the declared marker (exactly one occurrence) in the declared file, exactly one finding for the guard's policy and rule at that path. Zero, several or misplaced findings and tool failures are inconclusive; a passing challenged check is unproven. The copy is removed and verified absent before the proof is final, and a cleanup failure prevents `PROVEN`. The source checkout, including uncommitted files, is only read. See [evidence-schema.md](evidence-schema.md#proof-stages-and-cleanup) and [threat-model.md](threat-model.md).

This contract trusts the declared command to report its inspection truthfully. It supports one policy per command. For external provider discovery and tool-specific integration, see [provider support](provider-support.md). Run `go run ./cmd/invaris challenge examples/structured/invaris.json` for the offline fixture.

The forbidden-dependency fixture declares `dependencies.txt` as the exact provider scope. The engine checks returned finding paths against that scope and matches the challenged finding to the mutated target. A clean inspection count is the provider’s claim and carries no per-file coverage inventory.
