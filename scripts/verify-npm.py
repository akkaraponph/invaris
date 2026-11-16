#!/usr/bin/env python3
"""Verify an npm tarball against release archives and exercise its native install."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64")
PROGRAMS = ("invaris", "invaris-command-provider")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("archives", type=Path)
    parser.add_argument("package", type=Path)
    args = parser.parse_args()
    version = args.version.removeprefix("v")
    package = args.package.resolve()
    expected = {"package/package.json", "package/README.md", "package/LICENSE", "package/bin/invaris.js", "package/bin/invaris-command-provider.js", "package/lib/launcher.js"}
    expected.update(f"package/platforms/{target}/{program}" for target in TARGETS for program in PROGRAMS)
    with tarfile.open(package) as npm:
        members = npm.getmembers()
        if len(members) != len(expected) or {member.name for member in members} != expected or any(not m.isfile() for m in members):
            raise ValueError("unexpected npm package contents")
        manifest = json.load(npm.extractfile("package/package.json"))
        if manifest["name"] != "invaris-cli" or manifest["version"] != version or manifest.get("repository", {}).get("url") != "git+https://github.com/refactorroom/invaris.git":
            raise ValueError("npm package name, version or repository mismatch")
        for target in TARGETS:
            with tarfile.open(args.archives / f"invaris_{version}_{target}.tar.gz") as archive:
                for program in PROGRAMS:
                    member = npm.getmember(f"package/platforms/{target}/{program}")
                    if not member.mode & 0o111 or npm.extractfile(member).read() != archive.extractfile(program).read():
                        raise ValueError(f"binary mismatch or missing executable bit: {target}/{program}")
        for name in ("package/bin/invaris.js", "package/bin/invaris-command-provider.js"):
            if not npm.getmember(name).mode & 0o111:
                raise ValueError(f"launcher is not executable: {name}")
    with tempfile.TemporaryDirectory(prefix="invaris-npm-install-") as temp:
        project = Path(temp) / "project with spaces"
        project.mkdir()
        (project / "package.json").write_text('{"private":true}\n')
        subprocess.run(["npm", "install", "--ignore-scripts", "--offline", "--no-audit", "--no-fund", str(package)], cwd=project, check=True)
        cli = project / "node_modules/.bin/invaris"
        result = subprocess.run([cli, "check", "examples/provider/invaris.json"], cwd=ROOT, text=True, capture_output=True, timeout=60, check=True)
        evidence = json.loads(result.stdout)
        if evidence["decision"] != "ACCEPT":
            raise ValueError("provider-backed result is incorrect")
        result = subprocess.run([cli, "check", "--evidence-version", "v1alpha5", "examples/structured/invaris.json"], cwd=ROOT, text=True, capture_output=True, timeout=60, check=True)
        if json.loads(result.stdout)["engineVersion"] != args.version:
            raise ValueError("installed CLI version mismatch")
        provider = project / "node_modules/.bin/invaris-command-provider"
        result = subprocess.run([provider], input="", text=True, capture_output=True, timeout=10)
        if result.returncode != 1 or "request is not a valid JSON" not in result.stderr:
            raise ValueError(f"provider exit mismatch: {result.returncode}")
        (project / "new project").mkdir()
        subprocess.run([cli, "init", "--path", str(project / 'new project')], cwd=project, text=True, capture_output=True, timeout=10, check=True)
    print(f"PASS npm package {package.name}: sha256 {hashlib.sha256(package.read_bytes()).hexdigest()}")


if __name__ == "__main__":
    main()
