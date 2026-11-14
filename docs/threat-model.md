# Threat model: proofs and workspaces

Invaris runs harness commands as trusted local processes. A proof (`challenge`) or mutation test (`mutate`) adds one controlled change to a disposable copy of the checkout; this page states what that protects and what it does not.

## What is protected

- **The source checkout is only read.** Baselines, the mutation and the challenged check run in a temporary copy. Dirty, untracked and read-only files in the checkout keep their bytes and modes.
- **The mutation is the engine's, and only one file changes.** The engine replaces one occurrence of the declared marker in the declared file. The target is revalidated after the baseline: a baseline that replaced it with a symlink (to a file or a directory) or removed or reshaped it makes the proof inconclusive and nothing is written through the link. The workspace manifest (paths, modes, content hashes) is compared before and after the replacement and across each evaluator, and any other difference is an error. The before and after hashes are recorded.
- **Cleanup is part of the proof.** The copy is removed and its absence verified before a proof or mutation verdict is final. A cleanup failure makes the proof inconclusive; a report validator also rejects `PROVEN` without an `OK` cleanup stage.
- **The copier is restrictive.** It refuses symbolic links and non-regular files, at most 2,000 entries and 10 MiB per file, skips `.git` and `.invaris`, and refuses a temporary directory inside the checkout.

## Integrity guarantees shared by proofs and mutations

Both lifecycles verify the workspace root identity and compare the manifest (paths, modes, content hashes) at stage boundaries: across the baseline, and after the final evaluator, where only the engine's one declared replacement may differ. For a mutation experiment this covers the baseline, validity check and mutant tests; for a proof, the baseline and the challenged check. A violation makes the result `INCONCLUSIVE` with reason `workspace_changed`, never a proof or a verdict. A guard that writes caches, reports or build products into the workspace therefore cannot certify a proof: direct them outside it. The evaluators remain separate; only these guarantees are shared. They detect persistent changes visible at stage boundaries, not changes restored within a stage, external state, or reads of absolute paths.

## What is not protected

- **No sandbox.** A baseline or guard command runs with the user's permissions and can read or write absolute paths (including the source checkout), use the network or start detached processes. The manifest check covers the mutation operation, not those commands.
- **Local races.** The path checks in the mutation are not atomic. A concurrent local process that can write the temporary directory could race them.
- **Trusted analyzers.** A guard that reports a finding at the right rule and path for the wrong reason still proves detection of that finding; the proof shows the guard noticed this change, not that its rules are complete.
- **Marker proofs are weak.** Without a `policyId`, `PROVEN` rests on an output marker and is accepted only when the gate allows weak evidence.
- **Platform boundaries.** The runner targets the child process group on Linux and macOS. Other platforms only have direct-child termination. See [current state](current-state.md) for release verification.

Provider scope comes from the harness. The engine checks finding paths against it and trusts the provider’s report of how many targets it inspected.


Mutation experiments compare the workspace manifest across the baseline, validity check, and mutant tests. Commands must place build products and caches outside the workspace. The checks detect persistent changes at stage boundaries. Trusted commands can access other paths, change external state, or temporarily alter and restore files. The replacement installs a new file at the target, so an existing hard link does not receive the replacement bytes. A moved or replaced workspace makes cleanup unverifiable. The engine checks the original path and does not chase a moved directory.

Content identity is not authenticity: matching `beforeSha256`, `afterSha256` and `configDigest` against retained files shows the recorded edit is reproducible from those bytes, not who produced them. A `challenge` baseline can change the target before the replacement, and dirty or untracked files are copied, so `gitRevision` alone does not recover the preimage. Retained raw harness and source are not redacted and can hold secrets ([inspection procedure](evidence-schema.md#inspecting-the-actual-edit)).

Trust boundary for evidence: an adapter or evaluator reports what it inspected, which tests ran and what failed. The engine checks that the report is well formed, names the expected policy, path and test IDs, and agrees with the exit status. It cannot establish that the report is truthful. `minInspected` and `preimageSha256` constrain what the harness author expects; they do not authenticate the tool or the content's origin.

Mutation evaluators are trusted to report completed test cases, assertion failures, and validity assessments truthfully. Exit and evidence checks establish consistency with the declared case IDs. Digests identify the recorded contents. Report redaction covers configured literal secrets, including truncated excerpt prefixes, and cannot recognize transformed or undeclared secrets.

Evaluators must wait for their children and close their streams before issuing a verdict. If an evaluator exits 1 while a descendant keeps a pipe open, Go can report the exit error without a separate pipe-wait error.
