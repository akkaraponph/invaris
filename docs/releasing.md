# Prepare and publish a release

The latest GitHub release is [v0.4.0](https://github.com/refactorroom/invaris/releases/tag/v0.4.0); see its [release notes](releases/0.4.0.md). The examples below use v0.4.0; substitute the version you are preparing.

## Build and inspect archives

Use the Go version in `go.mod`, Python 3.11 or later, and Linux or macOS on amd64 or arm64. Run from the repository root:

```sh
python3 scripts/package-release.py v0.4.0 --output dist/v0.4.0
python3 scripts/verify-release.py v0.4.0 dist/v0.4.0
```

The destination must not exist. Packaging refuses to overwrite an earlier build; on failure, inspect the partial output and choose a new destination. `dist/` is ignored by Git. Build from the exact tested release commit with a clean worktree before publication.

The directory contains four `invaris_0.4.0_<os>_<arch>.tar.gz` archives (`linux` and `darwin`, each on `amd64` and `arm64`) and `SHA256SUMS`. Every archive has only `invaris`, `invaris-command-provider`, `LICENSE` and a version-specific `README.md` at its root. Builds disable cgo and stamp `engineVersion` with `v0.4.0`.

Verification checks every archive's SHA-256, exact member list, regular file types and executable permissions. It extracts the native archive into a new folder with a space in its name, then runs the installed CLI and provider, checks the stamped version, reads text and compact output, runs an operator campaign, initializes an empty project and rehearses migration with an exact backup. Other architecture binaries are cross-built and inspected, but are not executed on that runner.

## Automated release

Releases are published by the [Release workflow](../.github/workflows/release.yml) when a tag `vMAJOR.MINOR.PATCH` is pushed. It:

1. checks that the tag points at a commit on `main`, that `docs/releases/MAJOR.MINOR.PATCH.md` exists and that `CHANGELOG.md` has a `## MAJOR.MINOR.PATCH` section;
2. runs the full [CI workflow](../.github/workflows/ci.yml) on the tagged commit;
3. builds the four archives and `SHA256SUMS` once, stamped with the tag version, and verifies that exact set natively on Linux amd64, Linux arm64 and macOS arm64 (darwin/amd64 is checked for checksum and contents only);
4. publishes a GitHub release named `Invaris vX.Y.Z` with the archives, `SHA256SUMS` and the release notes file as its description.

Archive and npm package verification must pass before publication. The GitHub release is published first, followed by the npm package; publication is not atomic. See [npm publication and recovery](#npm-publication-and-recovery). The publish job uses a GitHub environment named `release`; add required reviewers to it in the repository settings if you want a manual approval before publication. The verified archives are also kept as a workflow artifact `release-vX.Y.Z` for 30 days.

Releases and npm packages come only from `main`. GitHub Pages is served from the `/docs` folder on `dev`. CI builds only `main` (pull requests to `main` and pushes to `main`), and those runs also rehearse packaging with the placeholder version `v0.0.0` and keeps the verified set as `snapshot-<commit>` for 7 days. Those archives are for inspection only, never for distribution.

## npm publication and recovery

The first npm package, `invaris-cli@0.4.0`, was published manually after the GitHub release. For a new package name, before its first release confirm the `invaris-cli` npm name is available and that the maintainer has an npm account with publishing access and 2FA. If the name is unavailable, stop and choose a package name before publishing. npm requires a package to exist before a trusted publisher can be configured. For the first release only, after explicit publication authorization and successful package verification, download the `release-vX.Y.Z-npm` and `release-vX.Y.Z` workflow artifacts, check `SHA256SUMS`, and run `python3 scripts/verify-npm.py vX.Y.Z ARCHIVE_DIRECTORY NPM_DIRECTORY/invaris-cli-X.Y.Z.tgz` from the matching release checkout. Substitute the actual version and paths. After the GitHub release is published, use an authenticated interactive npm session to publish that exact verified tarball with `npm publish NPM_DIRECTORY/invaris-cli-X.Y.Z.tgz --access public --ignore-scripts`. Do not publish a placeholder or reuse the same version. The automatic npm job for that first release cannot succeed before this bootstrap; do not rerun it after the manual publication.

Before the next npm release, configure an npm trusted publisher for GitHub owner `refactorroom`, repository `invaris`, workflow `release.yml`, and environment `release`, allowing direct publishing. A new configuration expires if its first successful trusted publication does not occur within two days, so configure it shortly before the next release. Follow [npm trusted publishing](https://docs.npmjs.com/trusted-publishers/). Subsequent publishing jobs use OIDC, Node 26.8.2 and npm 11.19.1; no long-lived npm token is stored in the repository.

The packaging workflow builds the npm tarball once from the verified archive set, records its SHA-256, and verifies installation and binary equality on Linux amd64, Linux arm64 and macOS arm64. macOS amd64 is checked for package contents and matching binary bytes, but is not executed by this runner matrix. The release job downloads the same artifact, rechecks its digest and contents, then publishes it after the GitHub release. CI uses version `v0.0.0` only for a non-published rehearsal.

If GitHub publication succeeds but npm fails, inspect the workflow and check whether the exact npm version exists before retrying the failed npm job; retry only when it is absent. Do not rerun a successful publish or replace a published version. If npm publication succeeds but a later verification fails, deprecate a defective npm version when appropriate and ship a new patch; retain the GitHub release and never move the tag. Users can install a prior published npm version explicitly or use a prior archive. Verify the published npm package and update the site after each release.

## Publication gates

1. Merge the feature PR into `staging`, then promote `staging` to `main` through a PR as described in [Contributing](../CONTRIBUTING.md). CI builds only the promotion PR and the resulting push to `main`; run the local checks before merging into `staging`. CI must pass on the promotion PR and on the resulting push to `main`.
2. Prepare `docs/releases/X.Y.Z.md` and the `## X.Y.Z` changelog section in that promotion, and remove "prepared for release" wording that will be wrong once published.
3. After explicit publication authorization, tag the tested `main` commit and push only that tag:

   ```sh
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "Invaris vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. Watch the Release workflow and complete the one-time npm bootstrap above if needed. Once both distributions are published, verify the npm package in a clean project, then download an archive and `SHA256SUMS` from the release page through the [installation guide](installation.md), verify the checksum and run the examples with the installed executables. Check that the release, source-tag and changelog links resolve and that GitHub Pages shows the new version.

Never move or reuse a published tag. To build and verify archives by hand, for example while debugging packaging, use the commands in [Build and inspect archives](#build-and-inspect-archives).

## Compatibility and rollback

Existing harnesses retain their default report versions and exit codes. The new v1alpha4 harness requires a 0.3.0 binary and always emits v1alpha5 evidence; opt-in v1alpha5 reports require consumers that understand that schema. Keep the older input files and report consumers when evaluating an upgrade.

To roll back the CLI, reinstall the v0.2.0 archive into a separate directory and put it first on `PATH`. Use v1alpha1, v1alpha2 or v1alpha3 harnesses with that binary; v1alpha4 harnesses and new flags are unsupported. If a harness was migrated, inspect its `.bak` and restore it manually only after checking for later edits. There is no service or database migration. If a published release is defective, mark it as such and ship a new patch version rather than rewriting the tag or replacing files under the same checksums.
