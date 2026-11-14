# Go project example

This Go module demonstrates a guardrail challenge and three mutation outcomes. Invaris runs each experiment in a disposable copy of this directory.

- **Guardrail challenge** (`guardrail.json`): a structured guard (`tools/importguard`) enforces the policy `no-network-imports`. Invaris injects `_ "net/http"` into `internal/discount/format.go` and checks that the guard reports that policy at that path.
- **Mutation tests** (`mutation-*.json`): Invaris changes `internal/discount/discount.go` and runs the ordinary Go tests `TestFullPrice` and `TestHalfOff` through `tools/goeval`. The suite is deliberately incomplete: nothing tests the 100% clamp.

## Requirements

Use Linux or macOS with a POSIX shell. Building Invaris requires the Go version in the repository root's `go.mod`; the example module declares `go 1.22`.

The helpers use `GOTOOLCHAIN=local` and `GOPROXY=off`, so the example runs offline with an installed toolchain. Harnesses allowlist `PATH` and `HOME`. The Go cache lives under `HOME`, outside the workspace. The first run compiles the standard library; command timeouts are 120 seconds.

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

Each mutation harness declares one replacement. Choose the file for the outcome you want to inspect:

| Harness | Change | Expected |
| --- | --- | --- |
| `mutation-killed.json` | `cents*percent/100` to `cents*percent/10`, with `preimageSha256` pinning `discount.go` | `TestHalfOff` fails: KILLED, `assertion_failed` |
| `mutation-survived.json` | clamp `return 0` to `return 1` | selected tests still pass: SURVIVED, `all_tests_passed` |
| `mutation-invalid.json` | `percent <= 0` to `percent <= "0"` | `go build` fails: INVALID, `mutant_invalid`, tests never run |

## Run a campaign

A campaign runs several of these harnesses in one sequence and decides on the whole set. See [Campaigns](../../docs/campaigns.md) for the rules.

```sh
/tmp/invaris campaign --format text campaign.json                     # six outcomes: REFUSE, exit 2
/tmp/invaris campaign campaign-accept.json                            # ACCEPT, exit 0
/tmp/invaris campaign --only mutation-survived campaign.json          # REJECT, exit 1
INVARIS_FIXTURE_STATE=$(mktemp -d) /tmp/invaris campaign campaign-mixed.json  # MIXED: REFUSE, exit 2
```

| Campaign file | Experiments | Expected |
| --- | --- | --- |
| `campaign.json` | the three mutation harnesses; `guardrail.json` (PROVEN); `guardrail-unproven.json`, which injects `_ "os/exec"`, an import the guard allows (UNPROVEN, `guard_passed`); `guardrail-inconclusive.json`, which injects an unterminated import that the guard cannot parse (INCONCLUSIVE, `execution_failed`) | Score 50.0% (1 of 2 eligible; the invalid mutant is excluded). The inconclusive guardrail makes the evidence incomplete, so the decision is REFUSE, exit 2, even though a survivor and an unproven guardrail are also present. |
| `campaign-accept.json` | `mutation-killed.json` and `guardrail.json` | ACCEPT, exit 0 |
| `campaign.json` with `--only mutation-survived` | only the surviving mutant; the others are `FILTERED_OUT` | REJECT, exit 1 |
| `campaign-mixed.json` | `mutation-sequence.json` three times | KILLED, SURVIVED, KILLED: MIXED, no score, REFUSE, exit 2 |

`mutation-sequence.json` uses `tools/sequence/test.sh` instead of `go test`. It is deliberately unstable but not random: on a mutant it fails on odd runs and passes on even runs, counting runs in the directory named by `INVARIS_FIXTURE_STATE`. Use a new, empty directory for each run. Without one the test exits 2 and every attempt is INCONCLUSIVE.

## Read the report

Read the answers from the JSON report (`--report new.json` saves it). Field paths are for `mutate` (`experiment.*`) and `challenge` (`proofs[0].*`).

| Question | Where the report answers it |
| --- | --- |
| Did the original checks pass? | `experiment.baseline.tests` (all `PASS`); `proofs[0].baseline.status` and `inspected`. An unhealthy baseline gives INCONCLUSIVE `unhealthy_baseline`, never a verdict. |
| What changed? | `experiment.mutationId`, `operator`, `target`, `mutation.beforeSha256` / `afterSha256`; `proofs[0].mutation`. Exactly one occurrence of the marker must exist and a stale `preimageSha256` is rejected (`mutation_rejected`). |
| Which check detected it? | Mutation: `experiment.tests.tests` names the selected `testIds` with `ASSERTION_FAILED`. Guardrail: `proofs[0].challenged.findings` holds one finding whose `policyId` is `no-network-imports` and `path` is `internal/discount/format.go`. |
| Did the experiment complete? | Exit status must match the structured results; a compile error is INVALID only through the validation stage, and a build failure inside the test stage, a skipped test, a timeout or a missing `go` is INCONCLUSIVE. The guard must inspect at least `minInspected: 2` files. `stages` must end with `cleanup` `OK`. |

## Interpret the result

- `goeval` and `importguard` are trusted. Invaris checks that their output is well formed, names the expected tests, policy and path, and agrees with the exit status; it cannot tell whether they report truthfully. `minInspected` and `preimageSha256` record what the author expects. They do not authenticate the tool or the content.
- SURVIVED means the selected tests passed. It does not mean the mutant is equivalent: here it exposes the missing test for `percent >= 100`.
- A guard result shows that this guard noticed this change, not that its rules are complete.
- The workspace copy is not a sandbox; the commands can reach the network and absolute paths.
- Helper limits: subtests are not mapped, and a test that panics is reported by Go as a failure, so it reads as `ASSERTION_FAILED`. Read Go's diagnostic output to distinguish a panic from a failed assertion.
- After changing `discount.go`, update the harness's `preimageSha256` using `shasum -a 256 internal/discount/discount.go`. A stale digest rejects the mutation.
