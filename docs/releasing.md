# Prepare and publish a release

Version 0.3.0 is prepared in a pull request; preparing archives does not publish a tag or a GitHub release. The [release notes](releases/0.3.0.md) are ready for the eventual release description. The previous published version is v0.2.0.

## Build and inspect archives

Use the Go version in `go.mod`, Python 3.11 or later, and Linux or macOS on amd64 or arm64. Run from the repository root:

```sh
python3 scripts/package-release.py v0.3.0 --output dist/v0.3.0
python3 scripts/verify-release.py v0.3.0 dist/v0.3.0
```

The destination must not exist. Packaging refuses to overwrite an earlier build; on failure, inspect the partial output and choose a new destination. `dist/` is ignored by Git. Build from the exact tested release commit with a clean worktree before publication.

The directory contains four `invaris_0.3.0_<os>_<arch>.tar.gz` archives (`linux` and `darwin`, each on `amd64` and `arm64`) and `SHA256SUMS`. Every archive has only `invaris`, `invaris-command-provider`, `LICENSE` and a version-specific `README.md` at its root. Builds disable cgo and stamp `engineVersion` with `v0.3.0`.

Verification checks every archive's SHA-256, exact member list, regular file types and executable permissions. It extracts the native archive into a new folder with a space in its name, then runs the installed CLI and provider, checks the stamped version, reads text and compact output, runs an operator campaign, initializes an empty project and rehearses migration with an exact backup. Other architecture binaries are cross-built and inspected, but are not executed on that runner.

## Review CI artifacts before publishing

The Release checks workflow runs only on pushes to `main`, including merge commits. PRs and staging pushes do not start builds; use the local checks before merging. Both Go jobs package all four platforms and verify installation on their native platform. The Dependency-Cruiser job runs the adapter and real integration fixtures. Every job must pass for the intended main release revision.

On a successful run, download one `release-<runner-os>-<commit-sha>` artifact from the run's **Artifacts** section. Each artifact contains all four archives and `SHA256SUMS`; use a single artifact's complete set, since independently built archive checksums can differ. They are retained for 14 days. Verify the downloaded set from a checkout of that commit:

```sh
python3 scripts/verify-release.py v0.3.0 /absolute/path/to/downloaded-artifact
```

Artifact checksums detect changed bytes, not a compromised build or a substituted manifest. These artifacts come from main builds; they are not a published release.

## Publication gates

1. Merge the feature PR into `staging`, then promote `staging` to `main` through a PR as described in [Contributing](../CONTRIBUTING.md). Preserve the released mainline history when updating staging.
2. Require Linux, macOS and Dependency-Cruiser CI to pass on the final main commit after merging. Review any changes since the candidate artifacts were built.
3. Build and verify from that exact clean main commit, or use one complete artifact set from its successful workflow. Recheck the four archives, `SHA256SUMS`, license and stamped version. Do not reuse candidate artifacts if the source changed.
4. After explicit publication authorization, create `v0.3.0` at the tested main commit and publish a GitHub release with the [prepared notes](releases/0.3.0.md), all four archives and `SHA256SUMS`. Never move an already published tag to different code.
5. Download an archive and manifest through the [installation guide](installation.md), verify its checksum, and run the examples with the installed executables. Check that the release, source-tag and changelog links resolve and that GitHub Pages shows 0.3.0.
6. Once publication is verified, remove the preparation notices from the README, landing page, installation guide and changelog in a follow-up documentation change.

Stop publication if a required job fails, checksums disagree, the native installation check fails, the version stamp is wrong, or the downloads do not match the intended release commit. Publication commands are deliberately left to the maintainer after the PR and final CI gates.

## Compatibility and rollback

Existing harnesses retain their default report versions and exit codes. The new v1alpha4 harness requires a 0.3.0 binary and always emits v1alpha5 evidence; opt-in v1alpha5 reports require consumers that understand that schema. Keep the older input files and report consumers when evaluating an upgrade.

To roll back the CLI, reinstall the v0.2.0 archive into a separate directory and put it first on `PATH`. Use v1alpha1, v1alpha2 or v1alpha3 harnesses with that binary; v1alpha4 harnesses and new flags are unsupported. If a harness was migrated, inspect its `.bak` and restore it manually only after checking for later edits. There is no service or database migration. If a published release is defective, mark it as such and ship a new patch version rather than rewriting the tag or replacing files under the same checksums.
