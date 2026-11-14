# 0001: Sequential campaign engine

Status: accepted for the next release (unreleased). Scope: the `campaign` action, `invaris.campaign/v1alpha1` and `invaris.evidence/v1alpha4`. User documentation: [Campaigns](../campaigns.md).

## 1. A separate campaign contract

**Context.** v1alpha3 holds exactly one `experiment`, and v1alpha2 has its own gate. A list of experiments with repetition and a policy does not fit either shape.

**Decision.** Add a new input contract, `invaris.campaign/v1alpha1`, with its own decoder and schema, and a new report, `invaris.evidence/v1alpha4`. The v1alpha2 and v1alpha3 inputs and reports do not change.

**Alternatives.** Allow an array in v1alpha3 `experiment`; add campaign fields to v1alpha2. Both change the meaning of published contracts.

**Consequences.** Existing consumers are unaffected. Tools must learn one more input and one more report version.

## 2. Campaigns reference existing experiments

**Context.** Verdict rules, evaluator contracts and integrity checks already exist for single experiments and are tested.

**Decision.** A campaign entry names a harness file and one experiment or challenge ID. The harness is loaded with the existing loader and run with the existing lifecycle. The campaign never reinterprets a child verdict.

**Alternatives.** Inline experiment definitions in the campaign file.

**Consequences.** One source of truth for experiment semantics. A campaign cannot express an experiment that a harness cannot. Each guardrail attempt runs the full `challenge` path, including its Git lookups, which costs some time.

## 3. Sequential execution

**Context.** Experiments can share caches, ports and external state, and cancellation must stay simple to reason about.

**Decision.** Run experiments and attempts one at a time, in declaration order.

**Alternatives.** A worker pool.

**Consequences.** Predictable resource use, deterministic report order and a single active process group to cancel. Campaigns take longer; parallelism is future work.

## 4. A fresh workspace per attempt

**Context.** A mutation left in a workspace would change the result of the next experiment or repeat.

**Decision.** Copy the source once into a frozen snapshot. Each attempt runs the normal lifecycle with the snapshot as its source, which copies it again into a new workspace. The snapshot manifest is checked before every attempt and before removal.

**Alternatives.** Copy the user's tree for every attempt (the tree could change between attempts); reuse one workspace and undo the change (an undo can fail silently).

**Consequences.** Two copies per attempt. Every attempt evaluates the same recorded content, and the report can prove it by comparing each attempt's `sourceSnapshot` digest with the campaign's.

## 5. No baseline reuse

**Context.** Reusing a passing baseline would save one command per attempt.

**Decision.** Every attempt runs its own baseline.

**Alternatives.** Cache the baseline per harness when inputs look equal.

**Consequences.** Slower campaigns. Baseline reuse needs proof that source bytes, evaluator configuration, selected tests and execution context are equivalent; that proof is not implemented.

## 6. Mixed results

**Context.** Repeats can disagree. A majority vote would turn a flaky test into a kill.

**Decision.** `CONSISTENT` only when every attempt executed with the same conclusive verdict. Conclusive but different verdicts are `MIXED`. Any attempt without a verdict makes the experiment `INCOMPLETE`, which takes precedence over `MIXED`. Only `CONSISTENT` experiments have an aggregate verdict.

**Alternatives.** Majority voting; reporting the last attempt.

**Consequences.** Nondeterminism stays visible. The acceptance policy treats `MIXED` as contradictory evidence (`UNSUPPORTED`).

## 7. Score denominators

**Context.** A mutation score is only meaningful over mutants with a trustworthy verdict.

**Decision.** `score = killed / (killed + survived)` over `CONSISTENT` mutation experiments, counted once each. Invalid, mixed, incomplete, not executed and filtered experiments are excluded and reported. An empty denominator gives `null`. Guardrail results have counts only.

**Alternatives.** Count attempts; count invalid mutants as killed; report 0% for an empty campaign.

**Consequences.** Repeats cannot inflate the score, and a run with nothing to score does not look like a perfect or failing result.

## 8. Acceptance is separate from verdicts

**Context.** A detected fault is an experiment result; whether a set of results is acceptable is a policy question.

**Decision.** A small typed policy (`requireNoSurvivors`, `requireAllGuardrailsProven`, `requireNoInconclusive`, `requireNoMixed`, optional `requireNoInvalid` and `minMutationScore`). Each enabled rule is `SATISFIED`, `VIOLATED`, `UNSUPPORTED` or `NOT_APPLICABLE`. An incomplete campaign or any `UNSUPPORTED` rule gives `REFUSE`; otherwise any `VIOLATED` rule gives `REJECT`; otherwise `ACCEPT` needs at least one `SATISFIED` rule. With `--only`, rules about kinds that are not selected are `NOT_APPLICABLE`, and the report records the reduced scope.

**Alternatives.** An expression language; reusing the failure-first gate.

**Consequences.** The decision is explainable from the report. Missing evidence never hides behind a reject. A filtered run can be accepted, but its report says that it was filtered.

## 9. Source identity for dirty trees

**Context.** A Git revision does not identify uncommitted or untracked files, and Invaris copies those.

**Decision.** Record the snapshot's manifest digest (paths, entry kinds, modes and file SHA-256 values in sorted order, excluding `.git` and `.invaris`) as the campaign's source identity. Record the Git revision and dirty state separately, as `null` when they cannot be determined. The campaign and harness bytes inside the snapshot must match what was loaded.

**Alternatives.** Use the Git revision alone.

**Consequences.** The report identifies what was evaluated even in a dirty tree. The digest is an integrity identifier, not an attestation.

## 10. Trusted-local is not a sandbox

**Context.** Campaigns run more commands and can make execution look more controlled than it is.

**Decision.** Keep `trustMode: "trusted-local"` and `networkPolicy: "unrestricted"` as the only accepted values. The snapshot and workspace checks detect persistent changes at stage boundaries; they do not confine commands.

**Alternatives.** Declare a restricted network policy without enforcement.

**Consequences.** Documentation states what is verified by the engine and what is trusted to the tools. Isolation remains future work.
