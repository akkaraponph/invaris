# Go project example

One small Go module, two kinds of experiment. Both run in a disposable copy of this directory; the source here is only read.

- **Guardrail challenge** (`guardrail.json`): a structured guard (`tools/importguard`) enforces the policy `no-network-imports`. Invaris injects `_ "net/http"` into `internal/discount/format.go` and checks that the guard reports that policy at that path.
- **Mutation tests** (`mutation-*.json`): Invaris changes `internal/discount/discount.go` and runs the ordinary Go tests `TestFullPrice` and `TestHalfOff` through `tools/goeval`. The suite is deliberately incomplete: nothing tests the 100% clamp.

Requirements: POSIX shell, a Go toolchain (the module declares `go 1.22`), and no network. The helpers run `go` with `GOTOOLCHAIN=local GOPROXY=off`. Harnesses allowlist only `PATH` and `HOME`; the Go build cache lives under `HOME`, outside the workspace, and the first run compiles the standard library (timeouts are 120 s). Windows is not supported.

## Run it

From the repository root:

```sh
go build -o /tmp/invaris ./cmd/invaris
cd examples/go-project
go test ./...                                  # the original code passes
/tmp/invaris check guardrail.json              # clean source: ACCEPT, exit 0
/tmp/invaris challenge guardrail.json          # PROVEN, exit 0
/tmp/invaris mutate mutation-killed.json       # KILLED, exit 0
/tmp/invaris mutate mutation-survived.json     # SURVIVED, exit 1
/tmp/invaris mutate mutation-invalid.json      # INVALID, exit 2
```

Two harness files would cover the guardrail and one mutant; a harness holds one mutation, so there is one file per mutant.

| Harness | Change | Expected |
| --- | --- | --- |
| `mutation-killed.json` | `cents*percent/100` to `cents*percent/10`, with `preimageSha256` pinning `discount.go` | `TestHalfOff` fails: KILLED, `assertion_failed` |
| `mutation-survived.json` | clamp `return 0` to `return 1` | selected tests still pass: SURVIVED, `all_tests_passed` |
| `mutation-invalid.json` | `percent <= 0` to `percent <= "0"` | `go build` fails: INVALID, `mutant_invalid`, tests never run |

## The five questions

Read the answers from the JSON report (`--report new.json` saves it). Field paths are for `mutate` (`experiment.*`) and `challenge` (`proofs[0].*`).

| Question | Where the report answers it |
| --- | --- |
| 1. Was the unmodified project healthy? | `experiment.baseline.tests` (all `PASS`); `proofs[0].baseline.status` and `inspected`. An unhealthy baseline gives INCONCLUSIVE `unhealthy_baseline`, never a verdict. |
| 2. What exactly changed? | `experiment.mutationId`, `operator`, `target`, `mutation.beforeSha256` / `afterSha256`; `proofs[0].mutation`. Exactly one occurrence of the marker must exist and a stale `preimageSha256` is rejected (`mutation_rejected`). |
| 3. Was it detected by the right thing? | Mutation: `experiment.tests.tests` names the selected `testIds` with `ASSERTION_FAILED`. Guardrail: `proofs[0].challenged.findings` holds one finding whose `policyId` is `no-network-imports` and `path` is `internal/discount/format.go`. |
| 4. Is the evidence complete rather than a failed execution? | Exit status must match the structured results; a compile error is INVALID only through the validation stage, and a build failure inside the test stage, a skipped test, a timeout or a missing `go` is INCONCLUSIVE. The guard must inspect at least `minInspected: 2` files. `stages` must end with `cleanup` `OK`. |
| 5. What does the result not prove? | See below. |

## What this does not prove

- `goeval` and `importguard` are trusted. Invaris checks that their output is well formed, names the expected tests, policy and path, and agrees with the exit status; it cannot tell whether they report truthfully. `minInspected` and `preimageSha256` record what the author expects. They do not authenticate the tool or the content.
- SURVIVED means the selected tests passed. It does not mean the mutant is equivalent: here it exposes the missing test for `percent >= 100`.
- A guard result shows that this guard noticed this change, not that its rules are complete.
- The workspace copy is not a sandbox; the commands can reach the network and absolute paths.
- Helper limits: subtests are not mapped, and a test that panics is reported by Go as a failure, so it reads as `ASSERTION_FAILED`. A stricter adapter would be needed to separate the two.
- Change `discount.go` and the pinned `preimageSha256` goes stale: update it with `shasum -a 256 internal/discount/discount.go`. The CLI test checks this.
