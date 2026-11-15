# Prepare and publish a release

Version 0.3.0 is prepared for release; pushing its tag starts the [automated release](#automated-release). The [release notes](releases/0.3.0.md) are ready for the eventual release description. The previous published version is v0.2.0.

## Build and inspect archives

Use the Go version in `go.mod`, Python 3.11 or later, and Linux or macOS on amd64 or arm64. Run from the repository root:

```sh
python3 scripts/package-release.py v0.3.0 --output dist/v0.3.0
python3 scripts/verify-release.py v0.3.0 dist/v0.3.0
```

The destination must not exist. Packaging refuses to overwrite an earlier build; on failure, inspect the partial output and choose a new destination. `dist/` is ignored by Git. Build from the exact tested release commit with a clean worktree before publication.

The directory contains four `invaris_0.3.0_<os>_<arch>.tar.gz` archives (`linux` and `darwin`, each on `amd64` and `arm64`) and `SHA256SUMS`. Every archive has only `invaris`, `invaris-command-provider`, `LICENSE` and a version-specific `README.md` at its root. Builds disable cgo and stamp `engineVersion` with `v0.3.0`.

Verification checks every archive's SHA-256, exact member list, regular file types and executable permissions. It extracts the native archive into a new folder with a space in its name, then runs the installed CLI and provider, checks the stamped version, reads text and compact output, runs an operator campaign, initializes an empty project and rehearses migration with an exact backup. Other architecture binaries are cross-built and inspected, but are not executed on that runner.

## Automated release

Releases are published by the [Release workflow](../.github/workflows/release.yml) when a tag `vMAJOR.MINOR.PATCH` is pushed. It:

1. checks that the tag points at a commit on `main`, that `docs/releases/MAJOR.MINOR.PATCH.md` exists and that `CHANGELOG.md` has a `## MAJOR.MINOR.PATCH` section;
2. runs the full [CI workflow](../.github/workflows/ci.yml) on the tagged commit;
3. builds the four archives and `SHA256SUMS` once, stamped with the tag version, and verifies that exact set natively on Linux amd64, Linux arm64 and macOS arm64 (darwin/amd64 is checked for checksum and contents only);
4. publishes a GitHub release named `Invaris vX.Y.Z` with the archives, `SHA256SUMS` and the release notes file as its description.

If any step fails, nothing is published; fix the problem on `main` and tag a new patch version. The publish job uses a GitHub environment named `release`; add required reviewers to it in the repository settings if you want a manual approval before publication. The verified archives are also kept as a workflow artifact `release-vX.Y.Z` for 30 days.

Every CI run also rehearses packaging with the placeholder version `v0.0.0` and keeps the verified set as `snapshot-<commit>` for 7 days. Those archives are for inspection only, never for distribution.

## Publication gates

1. Merge the feature PR into `staging`, then promote `staging` to `main` through a PR as described in [Contributing](../CONTRIBUTING.md). CI must pass on both pull requests and on the resulting push to `main`.
2. Prepare `docs/releases/X.Y.Z.md` and the `## X.Y.Z` changelog section in that promotion, and remove "prepared for release" wording that will be wrong once published.
3. After explicit publication authorization, tag the tested `main` commit and push only that tag:

   ```sh
   git switch main && git pull --ff-only
   git tag -a v0.3.0 -m "Invaris v0.3.0"
   git push origin v0.3.0
   ```

4. Watch the Release workflow. When it succeeds, download an archive and `SHA256SUMS` from the release page through the [installation guide](installation.md), verify the checksum and run the examples with the installed executables. Check that the release, source-tag and changelog links resolve and that GitHub Pages shows the new version.

Never move or reuse a published tag. To build and verify archives by hand, for example while debugging packaging, use the commands in [Build and inspect archives](#build-and-inspect-archives).

## Compatibility and rollback

Existing harnesses retain their default report versions and exit codes. The new v1alpha4 harness requires a 0.3.0 binary and always emits v1alpha5 evidence; opt-in v1alpha5 reports require consumers that understand that schema. Keep the older input files and report consumers when evaluating an upgrade.

To roll back the CLI, reinstall the v0.2.0 archive into a separate directory and put it first on `PATH`. Use v1alpha1, v1alpha2 or v1alpha3 harnesses with that binary; v1alpha4 harnesses and new flags are unsupported. If a harness was migrated, inspect its `.bak` and restore it manually only after checking for later edits. There is no service or database migration. If a published release is defective, mark it as such and ship a new patch version rather than rewriting the tag or replacing files under the same checksums.
