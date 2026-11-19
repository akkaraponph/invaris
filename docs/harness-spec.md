# Harness configuration

## Configuration location

The CLI defaults to `.invaris/invaris.json`, falling back to `invaris.json` in the current directory only when the workspace file is absent. An explicit path overrides discovery. For a harness directly inside `.invaris`, references to the “harness directory” below mean its parent project directory: all commands and project paths use that root. Other harness locations continue to use their containing directory. `invaris init` creates the workspace configuration; see [authoring](authoring.md#workspace-layout).

A harness is a JSON file that defines commands and experiments. `run`, `check`, and `challenge` accept `invaris.dev/v1alpha1` or `invaris.dev/v1alpha2` and emit v1alpha2 reports, or [`invaris.dev/v1alpha4`](#v1alpha4-built-in-operators) with built-in operators, which emits v1alpha5. `mutate` uses [v1alpha3 input](#single-mutant-harness) and output. A [campaign file](#campaign-file) groups existing harnesses for the `campaign` action. See the [evidence schema](evidence-schema.md). To create a starter harness, see [`invaris init`](authoring.md#create-a-harness-with-init).

## Common fields and validation

A harness is one UTF-8 JSON object, at most 1 MiB, with `version` set to `invaris.dev/v1alpha1` and a nonblank `name`. Field names are case-sensitive. Duplicate, unknown and null fields, null array elements, and trailing JSON values are rejected. The same semantic validation applies to `Load` and direct calls to `Run`, before commands execute.

`steps` and `guards` contain commands with a unique `id` and a nonblank `program`. Optional `args` are literal arguments (including empty strings), not shell expressions; a harness can explicitly invoke a shell. `timeoutSeconds` is an integer from 0 to 300; zero selects the 30-second default. `envAllowlist` names inherited variables. At most 128 steps, guards and challenges may be declared in total, and their IDs share a namespace.

### Challenges

A challenge names a guard and a relative `file`, plus distinct, nonempty `before` and `after` strings. Legacy guards require `expectedOutput`; structured guards use `policyId` and the [inspection format](inspection.md).

The engine runs the baseline and challenged check with the same guard in one disposable copy. The guard must leave the workspace unchanged. After the baseline, the target must remain a regular, writable file containing exactly one occurrence of `before`. Workspace changes yield `INCONCLUSIVE` with reason `workspace_changed`; `PROVEN` also requires verified cleanup.

Paths use forward slashes. Absolute paths, empty segments, `.` or `..` segments, backslashes, and drive prefixes are rejected. Reports record whole-file digests. [Retain the harness and target bytes](evidence-schema.md#inspecting-the-actual-edit) to inspect the edit later.

### Policies and execution settings

Optional `policies` declares permitted policy IDs. Optional `networkPolicy` currently accepts only `unrestricted`; it does not provide network isolation. Optional `writablePaths` restricts challenge targets to the listed exact paths: omission retains legacy behavior, whereas an explicit empty list permits no mutation. These declarations do not confine trusted commands. `requiredAdapters` is valid only in v1alpha2 (see provider negotiation below).

The existing v1alpha1 examples remain valid; [`invaris migrate`](authoring.md#upgrade-a-v1alpha1-harness-with-migrate) converts one to v1alpha2 without changing its behavior. Invalid configuration is refused before execution. Reports are now emitted as `invaris.evidence/v1alpha2` for both input versions; the v1alpha1 report shape is no longer produced (see the migration notes in [evidence-schema.md](evidence-schema.md#output-migration)).

For `run`, optional `failFast` and `alwaysRunGuards` booleans both default to true. When `failFast` is false, later steps are attempted after a failed step. When `alwaysRunGuards` is false, guards are skipped after a step failure. Skipped steps and guards appear as `NOT_RUN` with a reason; a skipped guard cannot produce a passing action. Cancellation also records unstarted work as `NOT_RUN`.

## Output formats

```sh
invaris [--report new-file.json] check [--format json|text|compact] [--evidence-version v1alpha5] [harness.json]
invaris [--report new-file.json] challenge [--format json|text|compact] [--evidence-version v1alpha5] [harness.json]
```

The default is `json` on every terminal and in CI. `text` prints each guard (status, policy, inspected count, findings, exit, evidence ID, duration and diagnostics) or each proof (guard, target, expected policy and path or `marker (weak evidence)`, observed findings, baseline and challenged status, verdict, reason, evidence ID, duration and failed stages), then `Decision:` with counts. `compact` prints one line per guard or proof and a final decision line, with no JSON. Flags go after the action and before the optional harness file; each may appear once. An unknown flag or format, a repeated or empty flag, a flag after the harness file, or an extra argument exits `2` before anything runs. `run` and `mutate` print JSON only and reject `--format`. Every action accepts `--evidence-version v1alpha5` to request the [portable evidence profile](evidence-schema.md#portable-profile-v1alpha5); no other value is accepted.

Text and compact output are rendered from the validated, redacted report after any `--report` file is published; they never recompute a decision, and the exit code is the report's. The `--report` file is always the JSON report. Output has no color or other terminal control sequences: control characters in names, IDs and messages are escaped, values with spaces or `=` are quoted, diagnostic messages are cut at 160 bytes with `... (truncated)`, and IDs are never truncated. A missing measurement prints `n/a`.

## v1alpha2 additions

`invaris.dev/v1alpha2` keeps the command, guard, challenge and scheduling fields above and makes trust and gating explicit. Its published schema is [`schemas/harness-v1alpha2.schema.json`](../schemas/harness-v1alpha2.schema.json) (Draft 2020-12); the schema checks shape, and the Go loader additionally enforces duplicate keys, ID uniqueness, gate references, path safety and size limits. Unknown, duplicate and null fields are rejected exactly as in v1alpha1.

| Field | Contract |
| --- | --- |
| `trustMode` | Required; only `trusted-local`. The local backend is not a sandbox. |
| `networkPolicy` | Required; only `unrestricted`. It is a declaration, not isolation. |
| `writablePaths` | Optional; omission means **no** mutation (v1alpha1 derived write paths from challenges). |
| `policies` | Objects `{"id": ..., "allowEmpty": false}`. A bare string is rejected. |
| `gate` | `{requiredGuards, requiredProofs, precedence, allowWeakEvidence}`; references must name declared guards and challenges. `precedence` is `failure-first` (default) or `refusal-first`; `allowWeakEvidence` defaults to false. |
| `redactEnv` | Names of environment variables whose values are redacted from evidence. Values are never serialized. |
| `storeSensitiveOutput` | Default false; raises the per-stream excerpt bound from 4 KiB to 64 KiB. |
| `adapters` | `{id, program, args, envAllowlist, timeoutSeconds}`: external provider processes speaking [the adapter protocol](adapter-protocol.md). IDs are their own namespace. |
| `requiredAdapters` | Adapter IDs that must be discoverable and available, or the run is refused before anything executes. Refused outright in v1alpha1. |

### Guard forms

Each guard uses one of two forms. Mixing their fields is invalid.

- **Command:** `program`, optional `args`, and optional `policyId` for structured inspection.
- **Provider:** `adapterId`, `policyId`, a nonempty `scope`, and provider-specific `options`.

A provider guard names a declared adapter. If the harness declares `policies`, the guard's policy must appear there. Scope entries use canonical relative paths; `.` is allowed only as the whole-project root. The engine passes `options` to the provider unchanged.

Challenges can reference either form. For provider guards, the policy ID is also the expected native rule ID. The same guard runs before and after the replacement.

The [Go project example](../examples/go-project/README.md) runs real `go test` tests through a small evaluator and a structured import guard.

### Declared inspection floor

A structured command guard (one with `policyId`) may declare `minInspected`, a positive integer. The engine refuses the check if the guard reports fewer inspected targets. The floor comes from the harness, so a guard that silently narrows its own scope is caught. It is still compared with the guard's self-reported count: the engine cannot verify that the guard inspected what it claims. `minInspected` is refused on steps, on guards without `policyId`, and on provider guards (which declare `scope` instead).

### Provider negotiation

Invaris discovers each used provider **once** before dispatch. For `run` and `check`, a provider is used if a guard names it or `requiredAdapters` lists it. Discovery includes the guard options when the provider’s participating guards share the same options. Then:

- A `requiredAdapters` provider that cannot be discovered or reports itself unavailable refuses the run.
- Each provider guard required by the gate (all guards when `gate` is omitted) needs a supported provider: the provider must be available, advertise the `check` operation and list the guard's `policyId`. Otherwise the run is refused **before any step runs**; steps and guards are recorded `NOT_RUN` with the reason. Nothing falls back to a local command.
- A provider guard outside the gate whose provider cannot run it is recorded `NOT_RUN` with the reason and does not block acceptance.

Provider and protocol failures during a check, and a provider whose identity differs from the one discovered, yield `REFUSE`.

Guards run in this order: command guards, then provider guards, each in declaration order.

The `challenge` action discovers providers named by the challenged guards and `requiredAdapters`. An unavailable required provider or unsupported challenged guard makes the affected proof inconclusive before a workspace is copied.

Provider executables and their dependencies must be reachable from the disposable workspace. See [Dependency-Cruiser example](dependency-cruiser-example.md) for a runnable example. The generic structured-command provider is `invaris-command-provider` (see [adapter-protocol.md](adapter-protocol.md#the-generic-command-provider)); `examples/provider/invaris.json` shows its use.

### Gate semantics

The gate defines the results required by an action:

- `run` and `check` use `requiredGuards`.
- `challenge` uses `requiredProofs`.
- Entries for other actions are ignored. The report records the gate that was applied.

Without a configured gate, every declared guard or challenge is required. An action with no required work and no steps is refused. `ACCEPT` requires every step to pass, every required result to be present and acceptable, and no failing or refusing result.

v1alpha1 inputs use a derived gate that permits weak marker proofs. v1alpha2 accepts weak `PROVEN` results only when `allowWeakEvidence` is true.

The CLI emits the same [v1alpha2 evidence format](evidence-schema.md) for both harness versions. Run `sh scripts/smoke.sh` to verify the bundled offline configurations.

## v1alpha4 built-in operators

`invaris.dev/v1alpha4` is the v1alpha2 guard harness plus built-in challenge mutations. It is a separate branch from the v1alpha3 behavioral mutation harness and accepts `run`, `check` and `challenge`. Its schema is [`schemas/harness-v1alpha4.schema.json`](../schemas/harness-v1alpha4.schema.json). Every v1alpha2 rule applies, including explicit `trustMode` and `networkPolicy`, exact `writablePaths` (omitted means no mutation) and the gate. The only difference in defaults: without a configured gate, **weak marker evidence is not permitted** (as in v1alpha2). Upgrading a v1alpha2 file is a manual edit; [`migrate`](authoring.md#upgrade-a-v1alpha1-harness-with-migrate) only produces v1alpha2.

A challenge has either the literal form (`before`, `after`, optional `expectedOutput`) or a `mutation` object, decided by which fields are present, even as empty strings. Mixing them, `"mutation": null` and `"mutation": {}` are refused before anything runs.

```json
{"id": "spacing", "guard": "whitespace", "file": "notes.txt",
 "mutation": {"operator": "trailing-whitespace", "version": "1", "preimageSha256": "<64 lowercase hex>"}}
```

| Field | Requirement |
| --- | --- |
| `operator` | `trailing-whitespace`, `crlf-injection` or `secret-leak-injection` ([catalog](mutation-operators.md)) |
| `version` | `"1"` |
| `preimageSha256` | Lowercase hex SHA-256 of the whole target file as authored. The edit is refused (`INCONCLUSIVE`, `mutation_rejected`) when the copied file no longer matches. |
| `fixture` | Required as `invaris-test-token-v1` for `secret-leak-injection`, and forbidden on the others |

No other options, seeds, globs or file references exist. The challenge's guard must declare a `policyId` (a structured command guard or a provider guard): a built-in challenge cannot be proven by an output marker, and the finding must match that policy as its rule at the target path. A built-in challenge runs through the same lifecycle as a literal one: fresh copy, healthy baseline with inspected targets, then the planned edit installed with the preimage pinned and re-validated, the same guard over the same scope, classification, integrity check and verified cleanup. A guard that reports the expected finding is `PROVEN`; one that still passes is `UNPROVEN`; a wrong or extra finding, a missing tool, a timeout or a cleanup failure is `INCONCLUSIVE`.

A v1alpha4 harness always produces [v1alpha5 evidence](evidence-schema.md#portable-profile-v1alpha5), including on early failure. See the runnable [operator example](../examples/operators/README.md).

## Single-mutant harness

`invaris.dev/v1alpha3` is the input version for `mutate`. It requires a name, `trustMode: "trusted-local"`, `networkPolicy: "unrestricted"`, a nonempty `writablePaths` list, and one `experiment`. See the [v1alpha3 schema](../schemas/harness-v1alpha3.schema.json).

### Experiment fields

| Field | Requirement |
| --- | --- |
| `id` | Nonblank experiment ID without surrounding whitespace |
| `mutation` | `id`, `operator` (`"replace"` or `"delete"`), `path`, `before`, and for `replace` a nonempty `after` |
| `validation`, `test` | Commands for validity assessment and selected tests |
| `testIds` | 1 to 128 unique, case-sensitive IDs without surrounding whitespace |

The mutation ID follows the same ID rules. Its `path` must match an entry in `writablePaths`. For `replace`, the strings must be nonempty and distinct. `delete` removes the single occurrence of `before`, which is the usual way to drop a guard line: include the line's trailing newline in `before`, and omit `after` or set it to `""`. See [`examples/mutation/deleted.json`](../examples/mutation/deleted.json). The engine requires exactly one literal occurrence of `before`, counting overlapping matches, and changes only the declared target.

Set optional `preimageSha256` to 64 lowercase hexadecimal characters to require exact target contents. If the target's SHA-256 differs at mutation time, the result is `mutation_rejected` and the engine writes nothing. The digest identifies contents, not their origin.

[Retain the harness and original target](evidence-schema.md#inspecting-the-actual-edit) to inspect the edit later; reports contain digests rather than replacement text.

### Commands and evaluator output

Commands use `id`, `program`, optional `args`, `envAllowlist`, and `timeoutSeconds`. Their IDs must be distinct. Omitted timeouts and zero use 30 seconds. An omitted or empty environment allowlist uses the default PATH and inherits no ambient variables. `redactEnv` and `storeSensitiveOutput` are optional. Required fields cannot be omitted, and null, unknown, and duplicate fields are rejected.

The same test command and case IDs apply to the baseline and mutant. The test command writes one JSON object to stdout, for example `{"version":"invaris.test-result/v1alpha1","tests":[{"id":"covered","status":"PASS"}]}`. The object must list exactly the harness's `testIds`, no more and no fewer; any other set of tests makes the experiment inconclusive with `malformed_evidence`.

Each selected case appears exactly once, in any order. Exit 0 requires all `PASS` and exit 1 requires at least one `ASSERTION_FAILED`.

Validation emits `{"version":"invaris.validity-result/v1alpha1","status":"VALID"}` with exit 0 or `INVALID` with exit 1.

### TypeScript validity helper

`invaris validity-result tsc <executable> <tsconfig.json>` runs a trusted TypeScript
compiler with plain diagnostics and no emitted source. For example, point a
mutation harness at a locally installed compiler:

```json
{"id":"typecheck","program":"invaris","args":["validity-result","tsc","./node_modules/.bin/tsc","tsconfig.json"],"envAllowlist":["PATH"]}
```

The helper exits `0` with `VALID` for a clean type check and `1` with `INVALID`
when the compiler reports recognized diagnostics in existing source files.
Configuration errors, dependency diagnostics, missing tools, timeouts and
unrecognized output exit `2` with diagnostics on stderr. Ambiguous diagnostics
such as missing declarations stay inconclusive. The helper uses a fresh
incremental cache outside the workspace, and its compiler receives `PATH`.
Link `node_modules` through the harness workspace when needed. Use
`invaris validity-result --help` for the command synopsis.

### Test-result converters

`invaris test-result <format> [--test-id ID]... [FILE|-]` turns a test reporter's output into this object, so a test command needs no hand-written converter. It reads FILE, or stdin when FILE is omitted or `-`, prints one object, and exits `0` when every test passed, `1` when at least one assertion failed, and `2` when the output cannot be converted.

Each `--test-id` keeps only that test, in the order given, so the whole suite can run while the object lists exactly the harness's `testIds`. This matters for suites whose tests share state, where running one test alone fails at baseline. The exit status then reflects only the selected tests, so an unrelated failing test does not count. A selected ID that is not in the output (not run, skipped or spelled differently) or selected twice exits `2`. Without `--test-id` every converted test is printed, which suits a harness whose `testIds` list the whole run. A test command runs it through a shell and passes the IDs as arguments, for example:

```json
{"program": "sh", "args": ["-c", "go test -json ./... | invaris test-result go-test-json --test-id \"$1\"", "sh", "example.com/app.TestOwnership"]}
```

| Format | Reporter | Test ID |
| --- | --- | --- |
| `go-test-json` | `go test -json` | `<package>.<test>`, subtests as `<package>.TestX/sub` |
| `jest-json` | `jest --json`, `vitest run --reporter=json` | `<test file relative to the working directory>::<full test name>` |
| `junit-xml` | `pytest --junitxml=FILE` | `<classname>::<name>` |

Use these IDs in `testIds`. Skipped, pending and todo tests are left out, so a selected test that was skipped makes the experiment inconclusive. A package that does not build, a package or test file that fails without a failed test (a timeout, `TestMain` exiting nonzero, a test file that fails to load or whose `afterAll` throws), a pytest setup error, duplicate IDs or no tests at all exit `2` and never count as a killed mutant. Reporter output is limited to 16 MiB. With `jest-json` or `junit-xml`, write the report to a file excluded by `workspace.exclude`, or to stdout, so it is not a workspace change.

Evaluators reserve these statuses for completed assessments. Infrastructure failures can report diagnostics on stderr and exit 2. Malformed or incomplete stdout, conflicting exits, and runner errors make the experiment inconclusive.

## Workspace options

`mutate` and `challenge` run in a disposable copy of the harness directory. By default the copier refuses symbolic links and stops at 2000 entries, so a Node project with `node_modules` cannot be copied as is. A v1alpha3 mutation harness and a v1alpha4 challenge harness accept an optional `workspace` block, with every path relative to the harness directory:

```json
"workspace": {
  "dependencies": ["node_modules"],
  "exclude": [".next", "coverage"],
  "maxEntries": 10000
}
```

| Field | Meaning |
| --- | --- |
| `dependencies` | Top-level directory names that are not copied. Each one is linked beside the copy, in its parent directory, and points at the same directory in the source. Resolvers that walk up the tree, such as Node and Vite, find it; the copy holds no link of its own, and a cache a tool creates at `node_modules/.vite` inside the copy stays in the copy. Each dependency must exist as a directory. |
| `exclude` | Canonical relative paths that are not copied and are left out of every snapshot. Anything a command creates or changes at or below them is not a `workspace_changed`. Use this for build output and tool caches, never for sources a test reads. |
| `maxEntries` | Entry bound for the copy and its snapshots, from 1 to 200000. The default is 2000. |

A mutation target or challenge file cannot sit in an excluded path or a dependency. Dependencies and exclusions are outside the integrity check: a command can change them, or write through a dependency link into the source, without the run noticing. This trades part of the integrity guarantee for the ability to run the project, so keep the lists narrow. The harness digest in every report pins these options.

When a stage changes the workspace, the stage detail lists up to 20 changed paths, marked `+` (added), `-` (removed) or `~` (changed). For example, `workspace manifest changed: +node_modules/.vite/deps` points straight at the cache to exclude.

In a campaign, the snapshot leaves out every harness's exclusions and dependencies, each taken relative to its harness directory, and uses the largest `maxEntries`. The report records that list as `sourceIdentity.workspaceExclude` and a raised bound as `sourceIdentity.workspaceMaxEntries`, and `invaris explain` names the excluded paths as a limit. Attempts link dependencies from the original harness directory, because the snapshot does not contain them.

## Campaign file

See [Campaigns](campaigns.md) for behavior and examples.

`invaris campaign` reads an `invaris.campaign/v1alpha1` file. The published schema is [`schemas/campaign-v1alpha1.schema.json`](../schemas/campaign-v1alpha1.schema.json). The file is one UTF-8 JSON object of at most 1 MiB; unknown, duplicate and `null` fields and trailing values are rejected, as for harnesses.

| Field | Requirement |
| --- | --- |
| `version` | `invaris.campaign/v1alpha1` |
| `name` | Nonblank |
| `trustMode`, `networkPolicy` | Only `trusted-local` and `unrestricted`. Neither adds isolation. |
| `experiments` | 1 to 64 entries, run in this order |
| `acceptance` | The acceptance policy below |
| `tools` | Optional; at most 16 version commands, `{id, program, args, envAllowlist, timeoutSeconds}`, run once in the snapshot. IDs follow the experiment ID rules. See [tool versions](campaigns.md#tool-versions). |

Each experiment entry:

| Field | Requirement |
| --- | --- |
| `id` | Unique; 1 to 128 characters from `A-Z a-z 0-9 . _ -`, starting with a letter or digit. Used by `--only`. |
| `kind` | `mutation` or `guardrail` |
| `harness` | A relative slash path inside the campaign directory, for example `api/mutation.json`. Absolute paths, empty, `.` and `..` segments, backslashes, `:`, and paths through `.git` are rejected. A harness may be directly inside the project’s `.invaris` directory; deeper paths through `.invaris` are rejected. Every directory on the path must be a real directory and the file a regular file: symbolic links are not followed. It cannot be the campaign file itself. The experiment evaluates the harness's execution root (the parent project for `.invaris/invaris.json`). For a campaign inside `.invaris`, harness references are relative to `.invaris`. |
| `experimentId` | For `mutation` only: the `experiment.id` of a v1alpha3 harness |
| `challengeId` | For `guardrail` only: a challenge ID in a v1alpha2 harness whose guard declares a `policyId` (structured command guard or provider guard) |
| `repeat` | Integer from 1 to 5 |

The same harness and reference may appear only once. Every referenced harness is fully validated before anything runs; an invalid harness invalidates the campaign. v1alpha1 harnesses and output-marker guards are rejected, so campaign proofs are always strong evidence. The harness `gate` is ignored.

`acceptance` must contain the booleans `requireNoSurvivors`, `requireAllGuardrailsProven`, `requireNoInconclusive` and `requireNoMixed`. Optional `requireNoInvalid` is a boolean and optional `minMutationScore` is a number greater than 0 and at most 1. At least one requirement must be enabled. A mutation requirement (`requireNoSurvivors`, `requireNoInvalid`, `minMutationScore`) needs at least one mutation entry, and `requireAllGuardrailsProven` needs at least one guardrail entry. See [acceptance policy](campaigns.md#acceptance-policy) for their meaning.

