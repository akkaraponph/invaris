# Campaigns

Campaigns were added in 0.2.0.

A campaign runs several existing experiments one after another and makes one decision about the whole set. It answers two questions: which deliberately introduced faults were detected, and does the set of results meet the policy you declared?

## Why campaigns exist

`mutate` and `challenge` test one change at a time. A real project needs several: some code mutations for the test suite and some violations for each guard. Running them by hand gives separate reports, separate exit codes, and no common view of the source that was tested. A campaign:

- runs every experiment against one frozen copy of the source, and records its digest;
- repeats experiments when you ask, and keeps every attempt;
- separates the mutation score from the guardrail results;
- applies an explicit acceptance policy and explains the decision.

## Campaigns and single experiments

A campaign does not define experiments. Each entry points to an existing harness and to one experiment or challenge inside it. That harness is still validated and executed by the same code as `invaris mutate` or `invaris challenge`, and its verdict rules are unchanged:

| Kind | Harness | Reference field | Verdicts |
| --- | --- | --- | --- |
| `mutation` | [`invaris.dev/v1alpha3`](harness-spec.md#single-mutant-harness) | `experimentId` | `KILLED`, `SURVIVED`, `INVALID`, `INCONCLUSIVE` |
| `guardrail` | [`invaris.dev/v1alpha2`](harness-spec.md#v1alpha2-additions) or [`invaris.dev/v1alpha4`](harness-spec.md#v1alpha4-built-in-operators) | `challengeId` | `PROVEN`, `UNPROVEN`, `INCONCLUSIVE` |

A guardrail entry must use a challenge whose guard declares a `policyId` (a structured command guard or a provider guard). Campaigns therefore accept only strong proof evidence; v1alpha1 harnesses and output-marker guards are rejected when the campaign is loaded. The harness `gate` is not used: the campaign policy replaces it. A campaign that references any v1alpha4 harness, even through an experiment filtered out with `--only`, emits [v1alpha5 evidence](evidence-schema.md#portable-profile-v1alpha5); `--evidence-version v1alpha5` requests it for any campaign.

## Define a campaign

A campaign is an `invaris.campaign/v1alpha1` JSON file. The published schema is [`schemas/campaign-v1alpha1.schema.json`](../schemas/campaign-v1alpha1.schema.json); the [harness specification](harness-spec.md#campaign-file) lists every field and limit. This is the Go example's `campaign-accept.json`:

```json
{
  "version": "invaris.campaign/v1alpha1",
  "name": "go-project-accept",
  "trustMode": "trusted-local",
  "networkPolicy": "unrestricted",
  "experiments": [
    {"id": "mutation-killed", "kind": "mutation", "harness": "mutation-killed.json", "experimentId": "go-mutation-killed", "repeat": 1},
    {"id": "guardrail-proven", "kind": "guardrail", "harness": "guardrail.json", "challengeId": "inject-net-http", "repeat": 1}
  ],
  "acceptance": {
    "requireNoSurvivors": true,
    "requireAllGuardrailsProven": true,
    "requireNoInconclusive": true,
    "requireNoMixed": true
  }
}
```

The campaign file's directory is the source the campaign copies. A referenced harness is a relative path inside it, such as `mutation-killed.json` or `services/api/mutation.json`. Each experiment evaluates its harness's own directory, exactly as `invaris mutate` or `invaris challenge` would when run on that harness, so a harness in `services/api` sees only the files under `services/api`. Before anything runs, Invaris rejects unknown or duplicate fields, `null` values, unsupported versions, duplicate experiment IDs, a `repeat` outside 1 to 5, more than 64 experiments, symbolic links anywhere on a harness path, paths through `.git` or `.invaris`, unknown or wrongly typed references, two entries for the same harness and reference, and acceptance requirements that apply to no declared experiment.

### Tool versions

The optional `tools` list records the versions of the tools your evaluators use. Each entry is a command with `id`, `program`, and optional `args`, `envAllowlist` and `timeoutSeconds`, for example:

```json
"tools": [{"id": "go", "program": "go", "args": ["version"], "envAllowlist": ["PATH", "HOME"], "timeoutSeconds": 30}]
```

Each command runs once in the snapshot before the first attempt. The report keeps its output, exit code and executable hash under `tools`. A failing command is recorded with a diagnostic and does not change the decision: the output describes the environment, it is not evidence about any experiment. Invaris cannot check that the tool that answered is the one the evaluators run later.

## Lifecycle

1. Load and validate the campaign and every referenced harness. Nothing runs if any of them is invalid.
2. Copy the campaign directory once into a temporary **snapshot** and record its manifest digest, plus one digest for each directory an experiment evaluates. The campaign file and every harness inside the snapshot must match the bytes that were validated.
3. Run the `tools` version commands, if any.
4. Discover the providers that selected guardrails need. If a required provider is missing or unsupported, no attempt starts.
5. For each selected experiment, in declaration order, and for each repeat: check that the snapshot is unchanged, then run the normal mutation or challenge lifecycle with the experiment's directory in the snapshot as its source. That lifecycle makes its own fresh copy, runs its baseline, applies exactly one change, evaluates it, and removes and verifies its copy.
6. Check the snapshot once more, remove it, and verify that it is gone.
7. Aggregate the attempts, compute the summaries, evaluate the policy, and write the report.

Attempts never share a workspace and baselines are not reused, so one experiment cannot see another experiment's change and a repeat never runs on an already mutated copy. Every attempt, including every repeat, runs its own baseline: a flaky baseline stays visible instead of being cached away.

### Parallel attempts

By default attempts run one at a time. `--parallel N` (1 to 8) runs up to N attempts at once. Workers take attempts in declaration order and the report keeps declaration order, so the report shape does not depend on timing. Each attempt still has its own workspace. Commands that share external state, such as a port, a database, a cache that is not safe for concurrent use, or a counter file like the mixed-result fixture's, can affect each other; run those campaigns with the default. A change to the snapshot refuses every attempt that starts after it is detected; attempts already running keep their own copies, and each is still checked against the recorded digest.

## Repeated attempts

`repeat` runs an experiment up to five times, each from a fresh copy of the same snapshot. Every attempt is kept in the report. The experiment's `stability` combines them without voting:

| Attempts | Stability | Aggregate verdict |
| --- | --- | --- |
| All have the same verdict, for example `KILLED, KILLED, KILLED` | `CONSISTENT` | That verdict |
| All have a verdict, but they differ, for example `KILLED, SURVIVED, KILLED` | `MIXED` | `null` |
| At least one has no verdict (`INCONCLUSIVE`, canceled, refused or not started) | `INCOMPLETE` | `null` |
| No attempts (filtered out) | `NOT_EVALUATED` | `null` |

`INCOMPLETE` takes precedence over `MIXED`. Consistent results are not proof that a test or guard is deterministic; they only show that the attempts agreed.

## Scores and summaries

The report keeps the two experiment kinds apart. There is no combined quality score.

- **Mutation score** is `killed / (killed + survived)` over `CONSISTENT` mutation experiments. An experiment counts once, however many attempts it has. `INVALID`, `MIXED`, inconclusive, not executed and filtered experiments are excluded and counted separately. When nothing is eligible the score is `null`, never 0 or 100%.
- **Guardrail proof rate** is `proven / (proven + unproven)` over `CONSISTENT` guardrail experiments, counted once each. Inconclusive, mixed and not executed challenges are excluded and counted separately, and filtered ones are listed apart. Campaigns accept only strong proofs, so every counted `PROVEN` matched the expected policy, native rule and target path. The rate is `null` when no challenge is eligible.

## Acceptance policy

The acceptance policy is a fixed set of typed rules. The first four are required in the file; the last two are optional.

| Requirement | Meaning |
| --- | --- |
| `requireNoSurvivors` | VIOLATED by each consistent `SURVIVED` mutation; UNSUPPORTED by each mutation without a consistent verdict |
| `requireAllGuardrailsProven` | VIOLATED by each consistent `UNPROVEN` guardrail; UNSUPPORTED by each guardrail without a consistent verdict |
| `requireNoInconclusive` | UNSUPPORTED by each experiment whose attempts are incomplete |
| `requireNoMixed` | UNSUPPORTED by each experiment whose attempts disagree |
| `requireNoInvalid` | VIOLATED by each consistent `INVALID` mutation; UNSUPPORTED by each mutation without a consistent verdict |
| `minMutationScore` | A number in (0, 1]. VIOLATED below the threshold; UNSUPPORTED when no mutant is eligible |

A requirement about a kind that has no selected experiment is `NOT_APPLICABLE`. The decision is:

1. `REFUSE` if the campaign did not run to completion: the snapshot failed or could not be removed, or any selected attempt did not execute.
2. Otherwise `REFUSE` if any requirement is `UNSUPPORTED`. Missing, incomplete or contradictory evidence wins over a violation.
3. Otherwise `REJECT` if any requirement is `VIOLATED`.
4. Otherwise `ACCEPT` if at least one requirement is `SATISFIED`; if none applies, `REFUSE`.

This order differs from the single-experiment gate, whose default is failure-first. A campaign does not reject when part of its evidence is missing, because the missing part could hide more problems.

## Failure, refusal and cancellation

| Situation | Attempt record | Effect |
| --- | --- | --- |
| The snapshot cannot be prepared | `NOT_STARTED`, `workspace_prepare_failed` | `REFUSE` |
| A campaign or harness file changed after loading | `NOT_STARTED`, `workspace_changed` | `REFUSE` |
| A required provider is unavailable | `NOT_STARTED`, `provider_unavailable` | `REFUSE` |
| A command changed the snapshot | That and every later attempt `REFUSED`, `workspace_changed` | `REFUSE` |
| An attempt is interrupted (Ctrl-C, SIGTERM) | `CANCELED`, `canceled`, with its partial evidence; with `--parallel`, every running attempt | `REFUSE` |
| Work after the interruption | `NOT_STARTED`, `canceled` | `REFUSE` |
| The snapshot cannot be removed | Cleanup stage `FAILED` | `REFUSE`, even if every verdict succeeded |

A failure to prepare or run an experiment is never reported as a surviving mutant or an unproven guard. Completed attempts keep their evidence when later work fails. On cancellation each active command's process group is stopped through the normal runner, no further attempt starts, the snapshot is removed, and a valid partial report is still written. Detached processes can escape process-group termination; see the [threat model](threat-model.md).

## Effectiveness (v1alpha5)

A [v1alpha5 campaign report](evidence-schema.md#portable-profile-v1alpha5) adds `effectiveness`, which breaks the guardrail summary down by guard and by operator. Behavioral mutation experiments never appear in it, and it never changes the mutation score, the proof rate, the acceptance evaluation or the decision.

- `byGuard` has one row per guard, identified by harness path and guard ID (`checks/invaris.json#whitespace`), so equal guard IDs in two harnesses stay separate.
- `byOperator` has one row per built-in operator and version (`trailing-whitespace` with `operatorVersion` `"1"`), plus a `replace` row, without `operatorVersion`, for literal `before`/`after` challenges.

Rows are sorted by ID (then version). Each row lists its `experiments` and counts them once each, after the same aggregation as the summary: repeats count once, a failed repeat makes the experiment inconclusive, and disagreeing repeats stay `mixed` with no vote. `planned` is the sum of `proven`, `unproven`, `inconclusive`, `mixed`, `notExecuted` and `filtered`; `executed` counts selected experiments with at least one attempt that ran; `eligible` is `proven + unproven`; `excluded` is `inconclusive + mixed + notExecuted`; filtered experiments are in neither. `proofRate` is `proven / eligible` as a fraction from 0 to 1, or `null` when nothing is eligible. `reasons` lists infrastructure reasons such as `timeout` and `canceled` **per attempt**, sorted by reason, so they can exceed the number of experiments; they are not added to any bucket. The validator recomputes every row from the experiments it lists. `--format text` prints the rows as percentages.

## CLI

```sh
invaris [--report new-file.json] campaign [--only ID]... [--format json|text|compact] [--parallel N] [--evidence-version v1alpha5] [campaign.json]
```

- The default file is `campaign.json` in the current directory.
- `--only ID` selects an experiment by exact ID; repeat it to select several. The other experiments are recorded as `FILTERED_OUT` with no attempts, the report's `selection.mode` is `filtered`, and requirements about kinds that are no longer selected become `NOT_APPLICABLE`. An unknown or repeated ID exits 2 before anything runs.
- `--format text` prints the summary below instead of JSON; `--format compact` prints one line per experiment (result, ID, kind and attempts) and a final line with the decision, mutation score and proof rate. The default is JSON whatever the terminal. The `--report` file is always JSON.
- `--parallel N` runs up to N attempts at once (1 to 8, default 1). See [parallel attempts](#parallel-attempts).
- Exit codes: `0` ACCEPT, `1` REJECT, `2` REFUSE.

Exit 2 has two meanings. When stdout holds a report, the campaign ran and was refused. When stdout is empty and stderr has a message, Invaris could not produce a report: invalid arguments, an invalid campaign or harness, an unpublishable `--report` destination, or a report that failed validation. Nothing is printed on stdout until the report has been validated and, with `--report`, saved.

## Example

From `examples/go-project` (see its [README](../examples/go-project/README.md)), `invaris campaign --format text campaign.json` prints:

```text
Invaris campaign: go-project-verification

Experiments: 6
Selected:    6
Filtered:    0

Behavioral mutations
  Killed:        1
  Survived:      1
  Invalid:       1
  Inconclusive:  0
  Mixed:         0
  Not executed:  0
  Score:         50.0% (1 of 2 eligible; 1 excluded)

Guardrail challenges
  Proven:        1
  Unproven:      1
  Inconclusive:  1
  Mixed:         0
  Not executed:  0
  Proof rate:    50.0% (1 of 2 eligible; 1 excluded)

Results
  mutation-killed          mutation  KILLED        KILLED (assertion_failed)
  mutation-survived        mutation  SURVIVED      SURVIVED (all_tests_passed)
  mutation-invalid         mutation  INVALID       INVALID (mutant_invalid)
  guardrail-proven         guardrail PROVEN        PROVEN (guard_detected)
  guardrail-unproven       guardrail UNPROVEN      UNPROVEN (guard_passed)
  guardrail-inconclusive   guardrail INCOMPLETE    INCONCLUSIVE (malformed_evidence)

Acceptance: REFUSE

Reasons:
  - requireNoSurvivors: VIOLATED (mutation-survived)
  - requireAllGuardrailsProven: VIOLATED (guardrail-unproven)
  - requireNoInconclusive: UNSUPPORTED (guardrail-inconclusive)
```

The score is 50% because one of the two eligible mutants was killed; the invalid mutant is excluded. The proof rate is also 50%: one of the two eligible challenges was proven, and the inconclusive one is excluded. The campaign has a survivor and an unproven guard, but the inconclusive guard means the evidence is incomplete, so the decision is `REFUSE` rather than `REJECT`. With `--only mutation-survived` the same file gives `REJECT` (exit 1).

An excerpt of the JSON report from the same run, which also records `go version` through `tools`:

```json
{
  "schemaVersion": "invaris.evidence/v1alpha4",
  "action": "campaign",
  "campaign": "go-project-verification",
  "sourceIdentity": {
    "snapshotSha256": "2013b2cfea95cd379772cb590ee0e9da606682203be0459b250f66fc239c50e2",
    "files": 26,
    "roots": [
      {
        "path": ".",
        "sha256": "2013b2cfea95cd379772cb590ee0e9da606682203be0459b250f66fc239c50e2",
        "files": 26
      }
    ],
    "gitRevision": "ee92f3c00c1b59b2f576bad05569fb6eafb83097",
    "gitDirty": true,
    "excluded": [".git", ".invaris"]
  },
  "parallelism": 1,
  "tools": [
    {
      "id": "tool:go",
      "program": "go",
      "args": ["version"],
      "exitCode": 0,
      "stdout": "go version go1.27.1 darwin/arm64\n"
    }
  ],
  "mutationSummary": {
    "killed": 1,
    "survived": 1,
    "invalid": 1,
    "inconclusive": 0,
    "mixed": 0,
    "notExecuted": 0,
    "filtered": 0,
    "eligible": 2,
    "excluded": 1,
    "score": 0.5
  },
  "guardrailSummary": {
    "proven": 1,
    "unproven": 1,
    "inconclusive": 1,
    "mixed": 0,
    "notExecuted": 0,
    "filtered": 0,
    "eligible": 2,
    "excluded": 1,
    "proofRate": 0.5
  },
  "acceptanceEvaluation": {
    "complete": true,
    "requirements": [
      {"requirement": "requireNoSurvivors", "status": "VIOLATED", "experiments": ["mutation-survived"]},
      {
        "requirement": "requireAllGuardrailsProven",
        "status": "VIOLATED",
        "experiments": ["guardrail-unproven"]
      },
      {
        "requirement": "requireNoInconclusive",
        "status": "UNSUPPORTED",
        "experiments": ["guardrail-inconclusive"]
      },
      {"requirement": "requireNoMixed", "status": "SATISFIED", "experiments": []}
    ]
  },
  "decision": "REFUSE"
}
```

Each experiment in `experiments[]` holds its attempts, and each attempt holds the complete child record: the v1alpha3 `mutation` experiment or the v1alpha2 `proof`, with its stages and observations. See [campaign evidence](evidence-schema.md#campaign-reports) for every field.

## Security limits

A campaign adds no isolation. Everything in the [threat model](threat-model.md) still applies:

- Commands run with your permissions and can read and write absolute paths, use the network, read credentials available to your user, and start detached processes. Isolation is deliberately not provided; see the [design note](decisions/0001-campaign-engine.md#10-trusted-local-is-not-a-sandbox).
- With `--parallel`, commands run at the same time and can interfere through shared external state.
- The snapshot check detects a command that leaves a change in the snapshot. It does not prevent the change, and it cannot see a change that is restored before the check.
- Evaluators and guards are trusted to report truthfully. Invaris checks that their output is complete and consistent; a dishonest tool can still produce valid-looking evidence.
- Digests identify content. They are not signatures and do not prove who ran the campaign or where.
- Redaction covers only the literal values of variables listed in each harness's `redactEnv`.
