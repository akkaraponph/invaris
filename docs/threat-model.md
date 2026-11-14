# Threat model: proofs and workspaces

Invaris runs harness commands as trusted local processes. A proof (`challenge`) adds one controlled change to a disposable copy of the checkout; this page states what that protects and what it does not.

## What is protected

- **The source checkout is only read.** Baselines, the mutation and the challenged check run in a temporary copy. Dirty, untracked and read-only files in the checkout keep their bytes and modes.
- **The mutation is the engine's, and only one file changes.** The engine replaces one occurrence of the declared marker in the declared file. The target is revalidated after the baseline: a baseline that replaced it with a symlink (to a file or a directory) or removed or reshaped it makes the proof inconclusive and nothing is written through the link. The workspace manifest (paths, modes, content hashes) is compared before and after the replacement and any other difference is an error. The before and after hashes are recorded.
- **Cleanup is part of the proof.** The copy is removed and its absence verified before the proof is final. A cleanup failure makes the proof inconclusive; a report validator also rejects `PROVEN` without an `OK` cleanup stage.
- **The copier is restrictive.** It refuses symbolic links and non-regular files, at most 2,000 entries and 10 MiB per file, skips `.git` and `.invaris`, and refuses a temporary directory inside the checkout.

## What is not protected

- **No sandbox.** A baseline or guard command runs with the user's permissions and can read or write absolute paths (including the source checkout), use the network or start detached processes. The manifest check covers the mutation operation, not those commands.
- **Local races.** The path checks in the mutation are not atomic. A concurrent local process that can write the temporary directory could race them.
- **Trusted analyzers.** A guard that reports a finding at the right rule and path for the wrong reason still proves detection of that finding; the proof shows the guard noticed this change, not that its rules are complete.
- **Marker proofs are weak.** Without a `policyId`, `PROVEN` rests on an output marker and is accepted only when the gate allows weak evidence.
- **Platform boundaries.** The runner targets the child process group on Linux and macOS. Other platforms only have direct-child termination. See [current state](current-state.md) for release verification.
