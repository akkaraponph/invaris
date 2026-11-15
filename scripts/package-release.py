#!/usr/bin/env python3
"""Build version-stamped Linux/macOS archives and their SHA256SUMS manifest."""

import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="release version, for example v0.3.0")
    parser.add_argument("--output", required=True, type=Path, help="new output directory")
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must have the form vMAJOR.MINOR.PATCH")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    readme = f"""Invaris {args.version}

This archive contains invaris and invaris-command-provider for this platform.
Keep both executables together and add their directory to PATH.
Verify this archive against SHA256SUMS before extracting it.

Run `invaris init --detect` to inspect a project, or `invaris init` to create
a starter harness. Read the documentation before running trusted commands:
https://github.com/akkaraponph/invaris/blob/{args.version}/docs/installation.md

Commands run with your user permissions. Disposable copies are not sandboxes.
External analyzers and example harnesses are not included. See LICENSE.
"""
    checksums = []
    with tempfile.TemporaryDirectory(prefix="invaris-package-") as tmp:
        for target in TARGETS:
            goos, goarch = target.split("_")
            stage = Path(tmp) / target
            stage.mkdir()
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
            for program in ("invaris", "invaris-command-provider"):
                subprocess.run(
                    ["go", "build", "-trimpath", "-ldflags",
                     f"-X github.com/akkaraponph/invaris/internal/engine.releaseVersion={args.version}",
                     "-o", str(stage / program), f"./cmd/{program}"],
                    cwd=ROOT, env=env, check=True,
                )
            (stage / "README.md").write_text(readme)
            (stage / "LICENSE").write_bytes((ROOT / "LICENSE").read_bytes())
            name = f"invaris_{args.version[1:]}_{target}.tar.gz"
            archive = output / name
            with tarfile.open(archive, "w:gz") as tar:
                for member in ("invaris", "invaris-command-provider", "LICENSE", "README.md"):
                    tar.add(stage / member, arcname=member)
            with archive.open("rb") as source:
                digest = hashlib.file_digest(source, "sha256").hexdigest()
            checksums.append(f"{digest}  {name}\n")
            print(f"Built {name}", flush=True)
    (output / "SHA256SUMS").write_text("".join(checksums))
    print(f"Release artifacts: {output}")


if __name__ == "__main__":
    main()
