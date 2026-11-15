#!/usr/bin/env python3
"""Verify all release archives and exercise the installed native executables."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64")
MEMBERS = {"invaris", "invaris-command-provider", "LICENSE", "README.md"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must have the form vMAJOR.MINOR.PATCH")
    directory = args.directory.resolve()
    expected = {f"invaris_{args.version[1:]}_{target}.tar.gz" for target in TARGETS}
    manifest = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (\S+)", line)
        if not match or match[2] in manifest:
            raise ValueError("invalid or duplicate checksum entry")
        manifest[match[2]] = match[1]
    if set(manifest) != expected or {p.name for p in directory.iterdir()} != expected | {"SHA256SUMS"}:
        raise ValueError("expected exactly four platform archives and SHA256SUMS")
    for name, digest in manifest.items():
        with (directory / name).open("rb") as source:
            if hashlib.file_digest(source, "sha256").hexdigest() != digest:
                raise ValueError(f"checksum mismatch: {name}")
        with tarfile.open(directory / name) as tar:
            members = tar.getmembers()
            if len(members) != len(MEMBERS) or {m.name for m in members} != MEMBERS:
                raise ValueError(f"unexpected archive entries: {name}")
            if any(not m.isfile() for m in members):
                raise ValueError(f"non-regular archive entry: {name}")
            for program in ("invaris", "invaris-command-provider"):
                if tar.getmember(program).mode & 0o111 == 0:
                    raise ValueError(f"{name}: {program} is not executable")
        print(f"PASS checksum and contents: {name}", flush=True)

    goos = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
    goarch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "AMD64": "amd64"}.get(platform.machine())
    if goos is None or goarch is None:
        raise ValueError("native installation checks require Linux or macOS on amd64 or arm64")
    with tempfile.TemporaryDirectory(prefix="invaris-install-") as tmp:
        install = Path(tmp) / "installed cli"
        install.mkdir()
        name = f"invaris_{args.version[1:]}_{goos}_{goarch}.tar.gz"
        with tarfile.open(directory / name) as tar:
            for member in tar.getmembers():
                with tar.extractfile(member) as source, (install / member.name).open("wb") as dest:
                    shutil.copyfileobj(source, dest)
                (install / member.name).chmod(member.mode & 0o777)
        env = dict(os.environ, PATH=str(install) + os.pathsep + os.environ.get("PATH", ""))

        def run(*command, cwd=ROOT, exit_code=0, report=True):
            result = subprocess.run([str(install / "invaris"), *command], cwd=cwd,
                                    env=env, capture_output=True, text=True, timeout=60)
            if result.returncode != exit_code:
                raise ValueError(f"{command}: exit {result.returncode}: {result.stderr}")
            print("PASS installed CLI: " + " ".join(command), flush=True)
            return json.loads(result.stdout) if report else result.stdout

        evidence = run("check", "--evidence-version", "v1alpha5", "examples/structured/invaris.json")
        if evidence["engineVersion"] != args.version or evidence["decision"] != "ACCEPT":
            raise ValueError("installed CLI has incorrect version or decision")
        run("check", "examples/provider/invaris.json")
        run("challenge", "--format", "text", "examples/structured/invaris.json", report=False)
        run("campaign", "--format", "compact", cwd=ROOT / "examples/operators", report=False)
        project = Path(tmp) / "new project"
        project.mkdir()
        run("init", "--path", str(project), report=False)
        run("check", cwd=project, exit_code=2)
        legacy = Path(tmp) / "legacy project"
        legacy.mkdir()
        for filename in ("invaris.json", "source.txt"):
            shutil.copyfile(ROOT / "examples" / filename, legacy / filename)
        original = (legacy / "invaris.json").read_bytes()
        run("migrate", cwd=legacy, report=False)
        if (legacy / "invaris.json.bak").read_bytes() != original:
            raise ValueError("migration backup differs from the source")
        run("check", cwd=legacy)
    print(f"PASS installed {args.version} on {goos}/{goarch}")


if __name__ == "__main__":
    main()
