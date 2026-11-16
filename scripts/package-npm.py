#!/usr/bin/env python3
"""Pack verified Invaris release archives as an npm package."""
import argparse
import json
from pathlib import Path
import re
import shutil
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
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must have the form vMAJOR.MINOR.PATCH")
    archives = args.archives.resolve()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    # Verification validates checksums, members, permissions and native behavior.
    subprocess.run(["python3", str(ROOT / "scripts/verify-release.py"), args.version, str(archives)], check=True)
    with tempfile.TemporaryDirectory(prefix="invaris-npm-") as temp:
        stage = Path(temp) / "package"
        shutil.copytree(ROOT / "npm", stage, ignore=shutil.ignore_patterns("*.test.js"))
        shutil.copyfile(ROOT / "LICENSE", stage / "LICENSE")
        manifest_path = stage / "package.json"
        manifest = json.loads(manifest_path.read_text())
        manifest["version"] = args.version[1:]
        manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
        for target in TARGETS:
            dest = stage / "platforms" / target
            dest.mkdir(parents=True)
            archive = archives / f"invaris_{args.version[1:]}_{target}.tar.gz"
            with tarfile.open(archive) as tar:
                for program in PROGRAMS:
                    member = tar.getmember(program)
                    with tar.extractfile(member) as source, (dest / program).open("wb") as binary:
                        shutil.copyfileobj(source, binary)
                    (dest / program).chmod(member.mode & 0o777)
        subprocess.run(["npm", "pack", "--ignore-scripts", "--pack-destination", str(output)], cwd=stage, check=True)


if __name__ == "__main__":
    main()
