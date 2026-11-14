#!/usr/bin/env python3
"""Validate the published JSON Schemas against real CLI output.

Builds the CLI into a temporary directory, runs it on the shipped examples and
on temporary harnesses, checks the exit codes, validates every emitted report
against schemas/evidence-v1alpha2.schema.json, validates the v1alpha2 example
harness, and requires deliberate counterexamples to be rejected.

Requires the pinned validator: pip install -r scripts/requirements-schema.txt
Exit status: 0 all checks passed, 1 a check failed, 2 the environment is unusable.
"""
import copy
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

try:
    from jsonschema import Draft202012Validator
except ImportError:
    print("jsonschema is not installed; run: pip install -r scripts/requirements-schema.txt", file=sys.stderr)
    sys.exit(2)

ROOT = Path(__file__).resolve().parent.parent
failures = []
checks = 0


def check(condition, label):
    global checks
    checks += 1
    if not condition:
        failures.append(label)
        print("FAIL", label)


def load(name):
    return json.loads((ROOT / "schemas" / name).read_text())


def validator(schema):
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema)


evidence_schema, harness_schema = load("evidence-v1alpha2.schema.json"), load("harness-v1alpha2.schema.json")
evidence, harness = validator(evidence_schema), validator(harness_schema)


def valid(v, doc):
    return v.is_valid(doc)


def run_cli(binary, cwd, *args):
    # A clean environment: the CLI must not depend on the caller's variables.
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(cwd)}
    p = subprocess.run([str(binary), *args], cwd=cwd, env=env, capture_output=True, text=True, timeout=120)
    return p.returncode, p.stdout, p.stderr


def adapter_checks():
    """Golden responses, the Python example's real output, and counterexamples."""
    schema = load("adapter-v1alpha1.schema.json")
    v = validator(schema)
    data = ROOT / "internal" / "adapter" / "testdata"
    golden = {n: json.loads((data / n).read_text().replace("$ID", "req-1")) for n in ("discover.json", "clean.json", "violation.json")}
    for name, doc in golden.items():
        check(valid(v, doc), f"adapter golden {name} violates schema")
    request = {"version": "invaris.adapter/v1alpha1", "requestId": "r", "operation": "check", "policyIds": ["p"], "scope": ["src", "."]}
    check(valid(v, request), "adapter request should satisfy schema")

    # The independent Python provider's real stdout must satisfy the schema.
    example = ROOT / "adapters" / "example" / "main.py"
    with tempfile.TemporaryDirectory(prefix="invaris-adapter-") as tmp:
        (Path(tmp) / "src").mkdir()
        (Path(tmp) / "src" / "a.txt").write_text("ok")
        for label, req in (
            ("discover", {**request, "operation": "discover", "policyIds": [], "scope": []}),
            ("clean check", {**request, "policyIds": ["no-forbidden-marker"], "scope": ["src"]}),
            ("unsupported mutate", {**request, "operation": "mutate", "policyIds": ["no-forbidden-marker"], "scope": ["src"]}),
        ):
            p = subprocess.run([sys.executable, str(example)], input=json.dumps(req), cwd=tmp, capture_output=True, text=True, timeout=30)
            check(p.returncode == 0, f"example provider {label}: exit {p.returncode}")
            try:
                doc = json.loads(p.stdout)
            except ValueError:
                check(False, f"example provider {label}: stdout is not JSON")
                continue
            check(valid(v, doc) and doc.get("requestId") == "r", f"example provider {label}: response violates schema")

    def mutated(base, edit):
        doc = copy.deepcopy(base)
        edit(doc)
        return doc

    clean, violation = golden["clean.json"], golden["violation.json"]
    negatives = {
        "wrong version": (clean, lambda d: d.update(version="invaris.adapter/v2")),
        "missing provider": (clean, lambda d: d.pop("provider")),
        "blank provider name": (clean, lambda d: d["provider"].update(name=" ")),
        "unknown field": (clean, lambda d: d.update(extra=1)),
        "unknown nested field": (clean, lambda d: d["check"].update(extra=1)),
        "no payload": (clean, lambda d: d.pop("check")),
        "two payloads": (clean, lambda d: d.update(error={"code": "INVALID_REQUEST", "message": ""})),
        "unknown error code": (clean, lambda d: (d.pop("check"), d.update(error={"code": "WEIRD", "message": ""}))),
        "PASS with zero targets": (clean, lambda d: d["check"].update(inspected=0)),
        "PASS with exit 1": (clean, lambda d: d["check"].update(exitCode=1)),
        "PASS with crash exit": (clean, lambda d: d["check"].update(exitCode=127)),
        "PASS with a finding": (violation, lambda d: d["check"].update(status="PASS", exitCode=0)),
        "FAIL with exit 0": (violation, lambda d: d["check"].update(exitCode=0)),
        "FAIL without findings": (violation, lambda d: d["check"].update(findings=[])),
        "NOT_RUN from provider": (clean, lambda d: d["check"].update(status="NOT_RUN")),
        "REFUSE without diagnostics": (clean, lambda d: d["check"].update(status="REFUSE")),
        "absolute finding path": (violation, lambda d: d["check"]["findings"][0].update(path="/etc/passwd")),
        "escaping finding path": (violation, lambda d: d["check"]["findings"][0].update(path="a/../b")),
        "finding without rule": (violation, lambda d: d["check"]["findings"][0].update(ruleId="")),
        "unknown operation capability": (golden["discover.json"], lambda d: d["capabilities"].update(operations=["explode"])),
        "missing capability list": (golden["discover.json"], lambda d: d["capabilities"].pop("prerequisites")),
        "request: unknown operation": (request, lambda d: d.update(operation="explode")),
        "request: check without policies": (request, lambda d: d.update(policyIds=[])),
        "request: check without scope": (request, lambda d: d.update(scope=[])),
        "request: escaping scope": (request, lambda d: d.update(scope=["../x"])),
        "request: absolute scope": (request, lambda d: d.update(scope=["/x"])),
        "request: unknown field": (request, lambda d: d.update(extra=1)),
    }
    for label, (base, edit) in negatives.items():
        check(not valid(v, mutated(base, edit)), f"adapter counterexample accepted: {label}")


def main():
    if shutil.which("go") is None:
        print("go toolchain not found", file=sys.stderr)
        return 2
    with tempfile.TemporaryDirectory(prefix="invaris-schema-") as tmp:
        tmp = Path(tmp)
        binary = tmp / "invaris"
        build = subprocess.run(["go", "build", "-o", str(binary), "./cmd/invaris"], cwd=ROOT, capture_output=True, text=True)
        if build.returncode != 0:
            print(build.stderr, file=sys.stderr)
            return 2

        reports = {}

        def emit(label, cwd, action, config, want_exit, want_decision):
            code, out, err = run_cli(binary, cwd, action, config)
            check(code == want_exit, f"{label}: exit {code}, want {want_exit} ({err.strip()})")
            try:
                doc = json.loads(out)
            except ValueError:
                check(False, f"{label}: stdout is not JSON")
                return
            errors = sorted(evidence.iter_errors(doc), key=lambda e: list(e.path))
            check(not errors, f"{label}: report violates schema: {errors[0].message if errors else ''}")
            check(doc.get("decision") == want_decision, f"{label}: decision {doc.get('decision')}, want {want_decision}")
            check(doc.get("schemaVersion") == "invaris.evidence/v1alpha2", f"{label}: schemaVersion")
            reports[label] = doc

        ex = ROOT / "examples"
        for sub, name in (("", "v1alpha1 command"), ("structured", "v1alpha1 structured"), ("v1alpha2", "v1alpha2 structured")):
            d = ex / sub if sub else ex
            for action in ("check", "challenge"):
                emit(f"{name} {action}", d, action, "invaris.json", 0, "ACCEPT")

        # The provider example, with the freshly built provider injected by absolute path.
        provider_bin = tmp / "invaris-command-provider"
        build = subprocess.run(["go", "build", "-o", str(provider_bin), "./cmd/invaris-command-provider"], cwd=ROOT, capture_output=True, text=True)
        if build.returncode != 0:
            print(build.stderr, file=sys.stderr)
            return 2
        project = tmp / "provider-project"
        shutil.copytree(ex / "provider", project)
        cfg = json.loads((project / "invaris.json").read_text())
        check(valid(harness, cfg), "example provider harness violates schema")
        cfg["adapters"][0]["program"] = str(provider_bin)
        (project / "invaris.json").write_text(json.dumps(cfg))
        emit("provider example check", project, "check", "invaris.json", 0, "ACCEPT")
        kinds = [o["kind"] for o in reports.get("provider example check", {}).get("observations", [])]
        check(kinds.count("discover") == 1, f"provider example: expected one discovery observation, saw {kinds}")
        (project / "source.txt").write_text("FORBIDDEN")
        emit("provider example violation", project, "check", "invaris.json", 1, "REJECT")
        cfg["adapters"][0]["program"] = str(tmp / "missing-provider")
        (project / "invaris.json").write_text(json.dumps(cfg))
        emit("provider example missing provider", project, "check", "invaris.json", 2, "REFUSE")

        v2 = {"version": "invaris.dev/v1alpha2", "name": "negative", "trustMode": "trusted-local", "networkPolicy": "unrestricted"}
        fails = {"id": "fails", "program": "sh", "args": ["-c", "exit 1"]}
        refuses = {"id": "refuses", "program": "invaris-missing-executable-4242"}
        for label, gate, code, decision in (
            ("failure-first", {"requiredGuards": ["fails", "refuses"], "precedence": "failure-first"}, 1, "REJECT"),
            ("refusal-first", {"requiredGuards": ["fails", "refuses"], "precedence": "refusal-first"}, 2, "REFUSE"),
        ):
            cfg = tmp / f"{label}.json"
            cfg.write_text(json.dumps({**v2, "guards": [fails, refuses], "gate": gate}))
            emit(f"precedence {label}", tmp, "check", str(cfg), code, decision)
        bad = tmp / "invalid.json"
        bad.write_text('{"version":"invaris.dev/v1alpha2"}')
        code, out, err = run_cli(binary, tmp, "check", str(bad))
        check(code == 2 and out == "" and err.strip() != "", "invalid harness must exit 2 with no stdout and a stderr diagnostic")

        # The v1alpha2 example harness must satisfy the harness schema.
        example = json.loads((ex / "v1alpha2" / "invaris.json").read_text())
        check(valid(harness, example), "example v1alpha2 harness violates schema")
        provider = {**v2, "adapters": [{"id": "a", "program": "p", "args": []}],
                    "guards": [{"id": "g", "adapterId": "a", "policyId": "p", "scope": ["src", "."], "options": {"k": [1]}}]}
        check(valid(harness, provider), "provider guard form should satisfy the harness schema")

        def mutated(base, edit):
            doc = copy.deepcopy(base)
            edit(doc)
            return doc

        harness_negatives = {
            "missing trustMode": lambda d: d.pop("trustMode"),
            "missing networkPolicy": lambda d: d.pop("networkPolicy"),
            "sandbox trust": lambda d: d.update(trustMode="sandboxed"),
            "restricted network": lambda d: d.update(networkPolicy="none"),
            "wrong version": lambda d: d.update(version="invaris.dev/v1alpha1"),
            "unknown field": lambda d: d.update(extra=1),
            "unsafe writable path": lambda d: d.update(writablePaths=["../x"]),
            "absolute writable path": lambda d: d.update(writablePaths=["/x"]),
            "bad precedence": lambda d: d["gate"].update(precedence="whatever"),
            "policy as string": lambda d: d.update(policies=["a"]),
            "timeout 301": lambda d: d["guards"][0].update(timeoutSeconds=301),
            "guard mixes forms": lambda d: d["guards"][0].update(adapterId="a"),
            "challenge without file": lambda d: d["challenges"][0].pop("file"),
        }
        for label, edit in harness_negatives.items():
            check(not valid(harness, mutated(example, edit)), f"harness counterexample accepted: {label}")
        check(not valid(harness, mutated(provider, lambda d: d["guards"][0].pop("scope"))), "harness counterexample accepted: provider guard without scope")
        check(not valid(harness, mutated(provider, lambda d: d["guards"][0].update(program="x"))), "harness counterexample accepted: provider guard with program")

        # Evidence counterexamples derived from a real accepted report.
        accepted = reports.get("v1alpha2 structured challenge")
        if accepted is None:
            check(False, "no accepted v1alpha2 report to derive counterexamples from")
        else:
            for key in evidence_schema["required"]:
                check(not valid(evidence, mutated(accepted, lambda d, k=key: d.pop(k))), f"evidence missing {key} accepted")
            negatives = {
                "unknown top-level field": lambda d: d.update(extra=1),
                "old schemaVersion": lambda d: d.update(schemaVersion="invaris.evidence/v1alpha1"),
                "invalid decision": lambda d: d.update(decision="PASS"),
                "invalid action": lambda d: d.update(action="verify"),
                "short runId": lambda d: d.update(runId="abc"),
                "malformed digest": lambda d: d.update(configDigest="md5:abc"),
                "ACCEPT without digest": lambda d: d.update(configDigest=None),
                "ACCEPT without target count": lambda d: d.update(targetCount=None),
                "ACCEPT with zero target count": lambda d: d.update(targetCount=0),
                "invalid proof status": lambda d: d["proofs"][0].update(status="PASS"),
                "invalid proof strength": lambda d: d["proofs"][0].update(evidenceStrength="medium"),
                "invalid check status": lambda d: d["proofs"][0]["baseline"].update(status="PROVEN"),
                "proof missing baseline key": lambda d: d["proofs"][0].pop("baseline"),
                "proof missing mutation key": lambda d: d["proofs"][0].pop("mutation"),
                "proof missing stages key": lambda d: d["proofs"][0].pop("stages"),
                "PROVEN without mutation": lambda d: d["proofs"][0].update(mutation=None),
                "mutation bad hash": lambda d: d["proofs"][0]["mutation"].update(afterSha256="xyz"),
                "mutation unknown field": lambda d: d["proofs"][0]["mutation"].update(extra=1),
                "PROVEN with failed cleanup": lambda d: d["proofs"][0]["stages"][-1].update(status="FAILED"),
                "PROVEN without cleanup stage": lambda d: d["proofs"][0].update(stages=[s for s in d["proofs"][0]["stages"] if s["name"] != "cleanup"]),
                "unknown stage name": lambda d: d["proofs"][0]["stages"][0].update(name="setup"),
                "negative stage duration": lambda d: d["proofs"][0]["stages"][0].update(durationMs=-1),
                "artifact bad hash": lambda d: d.update(artifacts=[{"path": "a", "sha256": "xyz", "bytes": 1}]),
                "ACCEPT artifact without bytes": lambda d: d.update(artifacts=[{"path": "a", "sha256": "0" * 64, "bytes": 0}]),
                "invalid precedence": lambda d: d["gate"].update(precedence=""),
                "invalid compatibility": lambda d: d["compatibility"].update(status="unknown"),
            }
            for label, edit in negatives.items():
                check(not valid(evidence, mutated(accepted, edit)), f"evidence counterexample accepted: {label}")
            # A non-ACCEPT report may carry explicit nulls.
            refused = reports.get("precedence refusal-first")
            if refused:
                check(valid(evidence, mutated(refused, lambda d: d.update(configDigest=None, targetCount=None))), "REFUSE report with null unknowns rejected")

    adapter_checks()
    print(f"{checks} checks, {len(failures)} failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
