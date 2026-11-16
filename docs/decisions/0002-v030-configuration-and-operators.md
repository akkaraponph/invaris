# 0002: Configuration authoring, built-in operators and portable evidence

Status: accepted; released in 0.3.0. Scope: `init`, `migrate`, `--format`, `invaris.dev/v1alpha4`, built-in operators and `invaris.evidence/v1alpha5`. User documentation: [authoring](../authoring.md), [operators](../mutation-operators.md), [evidence](../evidence-schema.md#portable-profile-v1alpha5).

## 1. Separate configuration branches

**Context.** v1alpha3 is the behavioral mutation contract. Built-in guard operators need a new challenge shape, and v1alpha1 users need a supported path to v1alpha2.

**Decision.** `migrate` converts only v1alpha1 to v1alpha2. Built-in operators live in a new guard branch, `invaris.dev/v1alpha4`, which extends v1alpha2 and is not merged with v1alpha3. Upgrading to v1alpha4 is a manual authoring choice, never a migration side effect.

**Alternatives.** Add a `mutation` field to v1alpha2, which silently breaks strict readers of a published contract; one universal harness, which expands scope.

**Consequences.** One more input version. Version predicates must be explicit: the derived gate permits weak evidence for v1alpha1 only, so v1alpha4 is strong by default. Tests cover each predicate.

## 2. Reuse filesystem isolation

**Context.** Copies, fresh baselines, stage manifests and the process-group runner already protect engine-managed edits.

**Decision.** Keep the bounded copy for operators. Make it stop on cancellation. Read and write targets only through the workspace's checked functions.

**Alternatives.** Git worktrees lose dirty and untracked bytes and add Git metadata risk; copy-on-write is an optimization; an OS sandbox is a separate project and is not interchangeable with a copy.

**Consequences.** Copy cost and Git-less guards remain. Trusted commands can still write outside the copy; this is documented, tested and never called containment.

## 3. Pure, closed operators

**Context.** Operators must be deterministic and must never write files themselves.

**Decision.** Three byte-level operators in a closed switch (`internal/mutation`): pure `Validate` and `Plan` functions over the target's exact bytes. The engine reads the target with `ReadTarget`, plans, and installs with `ReplaceExpecting` pinned to the declared preimage. No plugins, options, seeds or semantic parsing.

**Alternatives.** Runtime plugins or AST engines need stronger validity contracts than this release can give.

**Consequences.** Small and auditable; LF-only text targets; the synthetic secret needs a scanner rule that recognizes it. Any change to output bytes requires a new operator version.

## 4. Old reports unchanged, new portable profile

**Context.** Strict consumers validate v1alpha2 to v1alpha4. Reports expose paths, arguments and output.

**Decision.** Keep every published report version and schema byte-shape compatible by default. Add `invaris.evidence/v1alpha5` with built-in metadata, a reproduction identity, command digests, the engine version, campaign effectiveness rows and a privacy projection. It is automatic for v1alpha4 inputs and opt-in otherwise. Go validation recomputes relationships, identities and rows; the JSON Schema checks shape.

**Alternatives.** Adding fields to old schemas breaks strict consumers; stripping old reports loses diagnostics silently.

**Consequences.** Consumers opt in. A report does not record which challenges were declared built-in, so a single deleted metadata entry is not detectable from the report alone. Effectiveness rows list their experiments so that filtered and unstarted experiments can be recomputed.

## 5. Outcomes and metrics

**Context.** Verdicts, reasons and fractional scores are public.

**Decision.** Keep uppercase verdicts and existing reasons; keep the mutation score and proof rate as separate fractions with `null` for nothing eligible. Effectiveness rows reuse the same aggregation. Only human output shows percentages. Infrastructure reasons are counted per attempt, separately from experiment buckets.

**Alternatives.** A lowercase universal taxonomy or a combined percentage would break consumers or mislead.

**Consequences.** Readers see excluded, filtered and flaky results instead of an inflated rate.
