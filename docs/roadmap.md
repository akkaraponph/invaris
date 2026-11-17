# Roadmap after v0.5.0

Proposed on 2026-11-15 and revised on 2026-11-17 after v0.5.0. Versions below
are planning targets, not scheduled or approved implementation commitments.
Promote a milestone only after its exit criteria are met; split or defer it when
evidence changes the scope.

## Current baseline

Version 0.5.0 was released on 2026-11-17 and is the baseline for this roadmap.
See its [release notes](releases/0.5.0.md) and the
[current implementation](current-state.md) for its scope.

The CLI runs trusted local checks, challenges guards with deliberate edits,
tests single behavioral mutations (replacing or deleting a line), and combines
experiments into campaigns with fresh baselines, repeats and typed acceptance
policies. It also provides starter harnesses, v1alpha1 migration, `doctor` and
`explain` diagnostics, human-readable summaries, test-report conversion for
`go test`, jest/vitest and pytest, three byte-level guard operators and
portable v1alpha5 evidence. Dependency-Cruiser is the documented real-analyzer
integration, and Evaris 0.4.0 can run `check` and `campaign` as an optional
verifier.

These capabilities establish experiment infrastructure. A passing check alone
does not show that it catches faults; a proven challenge establishes detection
of that specific edit, not complete rule coverage. Commands remain trusted and
unsandboxed, and reports do not authenticate who executed them.

## Delivered milestones

| Version | Outcome | Status of exit criteria |
| --- | --- | --- |
| v0.4.0 (2026-11-16) | Make a first useful experiment approachable | `doctor` and `explain` were added from the [self-check experiment](experiments/self-check.md) friction log. A second operator has not yet repeated the workflow, so this milestone's exit criteria are only partly met. |
| v0.5.0 (2026-11-17) | Close gaps found on a real Node project | Replaced the planned evidence-verification milestone, which moves to v0.6.0. Added `--help`, `--version`, the `delete` mutation operator, `test-result` converters, finding `line` and recorded workspace exclusions. The self-check experiment has not been repeated with these features. |

## Release sequence

| Target | Intended outcome | Exit criteria |
| --- | --- | --- |
| v0.5.x, as needed | Maintain a dependable baseline | Fix confirmed defects while preserving published report shapes and outcome semantics. Documented examples, downloaded-archive installation and npm package checks pass. Historical fixtures still load and retain their intended decisions; any necessary contract change gets an explicit compatibility decision. |
| v0.6.0 | Make retained evidence independently inspectable | Verify supported reports against their internal relationships and, when supplied, exact retained harness/source bytes. Distinguish malformed evidence, mismatched inputs and unavailable inputs. Cover literal edits, built-in operators and campaign children. Separate offline verification from command re-execution and report authentication. |
| v0.7.0 | Make campaign changes useful in CI | Compare explicitly matched experiments across runs, showing scope, policy, operator, tool and configuration changes where recorded. Show denominators, exclusions, mixed and incomplete outcomes. Refuse unsupported comparisons instead of presenting a score delta as a regression. Demonstrate the workflow on a real consumer. |
| v1.0.0, after adoption evidence | Offer a durable CLI and evidence contract | Publish the supported command/schema matrix and compatibility policy; test historical reports and harnesses; provide migration or explicit refusal paths, recovery guidance and repeatable releases. Resolve material findings from actual users and state the precise limits of stability. |

Maintenance can run alongside workflow discovery. Later milestones depend on
lessons from earlier ones; version numbers may change. No owner or calendar
deadline is assigned by this proposal. The working audience is developers
running trusted tools locally and in CI; untrusted execution would require a
separate product and security decision.

## Next work: first useful experiment

1. Select one real repository and a concrete concern: a forbidden dependency,
   missed behavioral fault or another rule already supported by its tools.
   Define the expected finding or test failure before running the experiment.
2. Establish a healthy baseline and one deliberate edit with an independently
   understood expected result. Include a missed-detection control so that the
   workflow demonstrates a useful distinction rather than only a passing demo.
3. Adapt the existing [Go example](../examples/go-project/README.md) or
   [Dependency-Cruiser workflow](dependency-cruiser-example.md). Record where
   harness authoring, tool discovery, environment configuration, workspace
   restrictions or report interpretation require assistance.
4. Retain the exact harness, original target bytes, reports and available tool
   provenance. Keep incomplete and unsuccessful runs. A setup failure is not
   evidence that a guard missed a violation, and a weak marker proof must not be
   presented as structured rule detection.
5. Have another operator repeat the workflow and explain its result from the
   retained material. Record what help was needed. Publish only a reviewed,
   non-sensitive example or summary with its limitations.

The current Go preset adds a test guard but no challenge; the generic preset
declares no work. Use this workflow to decide whether better examples, authoring
assistance or a narrow preset closes the gap. Do not automatically inject a
fault into an arbitrary project merely to make onboarding appear complete.

## Scope decisions before subsequent milestones

- **Diagnostics:** `doctor` (bounded setup inspection) and `explain`
  (interpretation of recorded evidence) shipped in v0.4.0. Extend them only from
  observed confusion. State which checks execute tools; never imply that a
  diagnostic passed when it was not performed.
- **Verification and retention:** define `verify` against supported report
  versions and retained inputs. Reports currently do not archive source bytes;
  missing inputs must remain unverifiable. Portable reports intentionally
  withhold details, so document which checks remain possible. Hash matching
  establishes byte consistency, not execution authenticity. Any artifact bundle
  needs an explicit retention and sensitive-data contract.
- **Replay and comparison:** distinguish explaining an old report, recomputing
  its decision, reproducing an edit and rerunning commands. Define matching keys
  and comparability rules before historical comparisons. Equal source digests
  do not establish equal execution environments.
- **Performance:** measure real campaigns before choosing copy optimizations,
  caching or automatic test selection. Preserve the
  [no-baseline-reuse decision](decisions/0001-campaign-engine.md#5-no-baseline-reuse).
  Any selection or reuse mechanism must expose reduced scope and must not hide
  inconsistent baselines or turn incomplete evidence into acceptance.
- **Operators and integrations:** require a named consumer and discriminating
  clean, violating and unevaluable cases before adding another analyzer or
  mutation-tool bridge. Semantic operators need parser or compiler validity
  contracts; the current byte-level guard operators are not behavioral AST
  mutations. Record tool versions where available without treating tool-supplied
  strings as verified identities.
- **Compatibility:** preserve published default report shapes and native
  verdict meanings. Give incompatible contracts a new version and an explicit
  migration or refusal policy. Do not silently rewrite retained evidence.

## Release gates for every milestone

Use the [contribution checks](../CONTRIBUTING.md#local-checks) and
[release guide](releasing.md) on the exact candidate commit. Require the checks
applicable to the release, including build, vet, race tests, offline smoke and
schema validation, compatibility fixtures, operator fuzzing and real
Dependency-Cruiser integration on supported hosted platforms.

Add discriminating checks for new behavior, including malformed, missing,
canceled, mismatched and tampered evidence as applicable. Preserve the separation
between experiment outcomes and acceptance policy, and between mutation score
and guardrail proof rate. Failures must not be made acceptable by silently
changing denominators, dropping attempts or weakening the expected finding.

Review CLI/schema compatibility, update the changelog and relevant references,
run changed documented commands, and check links from the GitHub Pages landing
page. Verify archive contents, checksums, stamped versions and native
installation; retain the previous supported binary and recovery guidance.
Publication remains subject to the release guide's authorization and gates.

Each milestone starts as unverified future scope. Implementation and release
require their own evidence; earlier successful checks cannot verify later code.

## Deferred until a demonstrated need

OS-enforced isolation, native Windows support, larger-workspace support,
distributed execution, hosted dashboards, dynamic plugins, semantic operators,
automatic test selection, result caching and static-analysis SARIF export are
outside the next proposed milestone. Workspace scale and platform support need
their own representative cases and limits. Isolation must enforce and test
network, filesystem and process boundaries on every platform where it is claimed.

AI evaluation integrations or model-backed runs first need a named consumer,
a tool-independent result contract and offline fixtures. Reuse existing
evaluation infrastructure where it fits rather than assuming Invaris needs an
agent runner or judging system. The [inspiration list](provider-support.md)
does not commit to specific integrations.

Current contracts and limits are documented in [authoring](authoring.md),
[campaigns](campaigns.md), [evidence](evidence-schema.md),
[operators](mutation-operators.md), [implementation status](current-state.md)
and the [threat model](threat-model.md).
