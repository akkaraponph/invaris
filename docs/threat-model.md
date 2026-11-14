# Threat model: proofs and workspaces

Invaris runs commands with your user permissions. Use trusted harnesses, executables, and analyzer configurations. A challenge or mutation experiment runs in a disposable project copy, which provides a place to apply and inspect one controlled change.

## What is protected

- **Engine operations use the copy.** Baselines, replacements, and evaluators run in a temporary workspace. The engine copies the source checkout without changing its files, including dirty, untracked, and read-only files. Trusted commands can still access the source through absolute paths.
- **One declared replacement is allowed.** The engine replaces exactly one occurrence in the declared file and records whole-file hashes before and after. It revalidates the target after the baseline and rejects symlinks, removed targets, and ambiguous matches. Replacement installs a new file, so existing hard links retain their original bytes.
- **Cleanup is verified.** The engine removes the copy and checks that it is absent before finalizing a result. Cleanup failure yields `INCONCLUSIVE`. Report validation rejects `PROVEN` without a recorded mutation and an `OK` cleanup stage.
- **Copy size and file types are restricted.** The copier accepts at most 2,000 entries and 10 MiB per file. It rejects symbolic links and non-regular files, skips `.git` and `.invaris`, and refuses temporary directories inside the checkout.

## Integrity guarantees shared by proofs and mutations

The engine records a workspace manifest containing paths, file modes, and content hashes. At stage boundaries it checks that the workspace root is the same directory and that the manifest matches the expected state:

- After the baseline, files must match the original copy.
- After validation and tests, or the challenged guard, files must match the copy with only the engine's replacement applied.

A persistent change yields `INCONCLUSIVE` with reason `workspace_changed`. Direct caches, reports, and build products outside the workspace. The checks detect changes visible at stage boundaries. They cannot detect changes that a command restores before returning.

If a command moves or replaces the workspace directory, cleanup becomes unverifiable. The engine checks the original path and does not follow a moved directory.

## What is not protected

- **External files and network access.** Commands can read and write absolute paths, change external state, use the network, and start detached processes. The disposable copy is not a sandbox.
- **Concurrent local writes.** Path checks and replacements are not atomic. Another process with write access to the temporary directory can race them.
- **Tool honesty.** Providers and evaluators report what they inspected, which tests ran, and what failed. Invaris checks report structure, expected identifiers, paths, and exit codes. It trusts tools to report truthfully.
- **Rule completeness.** A proof shows that the selected guard detected this change. It does not establish that the guard detects every violation. Marker proofs rely on output text and require a gate that permits weak evidence.
- **Detached descendants.** Cancellation targets the child process group on Linux and macOS. Other platforms terminate only the direct child. Detached descendants can escape group termination.

Provider findings must fall within the harness's declared scope. Inspection counts remain the provider's claim. `minInspected` sets a minimum count; `preimageSha256` requires exact target contents. Neither authenticates a tool or the origin of its inputs.

## Campaigns

A [campaign](campaigns.md) (unreleased) adds a frozen snapshot of the campaign directory. Each attempt copies the snapshot again, so attempts never share a workspace.

| Verified by the engine | Declared by the harness or trusted to tools |
| --- | --- |
| The campaign and harness files in the snapshot match the validated bytes | That the harness tests what its author intended |
| The snapshot manifest is unchanged before every attempt and before removal | That no command changed and restored the snapshot in between |
| Every attempt evaluated a copy whose manifest digest equals the recorded source identity | That commands did not read other files through absolute paths |
| Each child verdict passes the single-experiment evidence rules | That evaluators and guards reported truthfully |
| The snapshot and every attempt workspace were removed | That detached processes did not keep running |

A command that writes into the snapshot through its absolute path is detected before the next attempt; that attempt and every later one are `REFUSED` and the campaign is refused. Detection is not containment: the write has already happened. Cancellation stops the active process group on Linux and macOS, starts no further attempts and still publishes a valid partial report; detached descendants can escape. Redaction covers the configured `redactEnv` values of every referenced harness. Diagnostics can contain paths and the `PATH` search list from runner errors.

## Evidence and retained inputs

Matching `beforeSha256`, `afterSha256`, and `configDigest` against retained files verifies the recorded edit. It does not authenticate the report or identify who ran it. Dirty and untracked files are copied, so `gitRevision` alone may not recover the original target. See the [inspection procedure](evidence-schema.md#inspecting-the-actual-edit).

Retained harnesses and source files can contain secrets and are outside report redaction. Report redaction covers configured literal secret values, including prefixes cut off in excerpts. Encoded, transformed, and undeclared secrets can remain visible.

Evaluators must wait for their children and close their streams before returning a verdict. If an evaluator exits 1 while a descendant keeps a pipe open, Go can report the exit error without a separate pipe-wait error.
