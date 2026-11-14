#!/usr/bin/env python3
"""Example Invaris provider (invaris.adapter/v1alpha1), standard library only.

It implements one check-only policy, "no-forbidden-marker": a file under the
requested scope violates it if it contains the text FORBIDDEN_MARKER (override
with options {"marker": "..."}). It has no Invaris imports and no mutation
support. Run from the project root; it reads one JSON request from stdin and
writes one JSON response to stdout. A policy violation is a normal response
with status FAIL and exit code 0 for this process; only protocol failures use a
nonzero process exit.
"""
import json
import os
import sys

VERSION = "invaris.adapter/v1alpha1"
POLICY = "no-forbidden-marker"
RULE = "forbidden-marker"
PROVIDER = {"name": "invaris-example-marker", "version": "0.1.0", "toolName": None, "toolVersion": None}
MAX_FILE = 1 << 20


def respond(request_id, **body):
    out = {"version": VERSION, "requestId": request_id, "provider": PROVIDER}
    out.update(body)
    sys.stdout.write(json.dumps(out, sort_keys=True) + "\n")


def error(request_id, code, message):
    respond(request_id, error={"code": code, "message": message})


def safe_relative(path):
    if path == ".":
        return True
    if not path or path.startswith("/") or any(c in path for c in "\\:\x00"):
        return False
    return all(part not in ("", ".", "..") for part in path.split("/"))


def files_under(root, entry):
    """Regular files beneath one scope entry, without following symlinks."""
    start = os.path.join(root, entry)
    if os.path.islink(start):
        return
    if os.path.isfile(start):
        yield entry
        return
    for directory, dirs, names in os.walk(start, followlinks=False):
        dirs[:] = sorted(d for d in dirs if not os.path.islink(os.path.join(directory, d)))
        for name in sorted(names):
            full = os.path.join(directory, name)
            if os.path.isfile(full) and not os.path.islink(full):
                rel = os.path.relpath(full, root).replace(os.sep, "/")
                yield rel


def main():
    raw = sys.stdin.buffer.read(MAX_FILE + 1)
    try:
        request = json.loads(raw.decode("utf-8"))
        request_id = request["requestId"]
        operation = request["operation"]
        assert isinstance(request_id, str) and request_id
    except Exception:
        error("", "INVALID_REQUEST", "request is not a JSON object with requestId and operation")
        return 1
    if request.get("version") != VERSION:
        error(request_id, "UNSUPPORTED_PROTOCOL", "only " + VERSION + " is supported")
        return 0

    if operation in ("discover", "doctor"):
        respond(request_id, capabilities={
            "languages": [], "policyIds": [POLICY], "operations": ["discover", "check", "doctor"],
            "proofTypes": [], "reportFormats": [], "available": True, "prerequisites": [],
        })
        return 0
    if operation == "mutate":
        error(request_id, "UNSUPPORTED_CAPABILITY", "this provider does not support mutation")
        return 0
    if operation != "check":
        error(request_id, "INVALID_REQUEST", "unknown operation")
        return 0

    policies, scope = request.get("policyIds") or [], request.get("scope") or []
    if policies != [POLICY]:
        error(request_id, "UNSUPPORTED_CAPABILITY", "only policy " + POLICY + " is supported")
        return 0
    if not scope or not all(isinstance(s, str) and safe_relative(s) for s in scope):
        error(request_id, "INVALID_REQUEST", "scope must be non-empty canonical relative paths")
        return 0
    options = request.get("options") or {}
    marker = options.get("marker", "FORBIDDEN_MARKER") if isinstance(options, dict) else None
    if not isinstance(marker, str) or not marker:
        error(request_id, "INVALID_REQUEST", "options.marker must be a non-empty string")
        return 0

    root, inspected, findings, seen = os.getcwd(), 0, [], set()
    for entry in scope:
        for rel in files_under(root, entry):
            if rel in seen:
                continue
            seen.add(rel)
            try:
                with open(os.path.join(root, rel), "rb") as f:
                    text = f.read(MAX_FILE + 1)
                if len(text) > MAX_FILE:
                    continue
                text = text.decode("utf-8")
            except (OSError, UnicodeDecodeError):
                continue
            inspected += 1
            if marker in text:
                findings.append({"policyId": POLICY, "ruleId": RULE, "path": rel, "message": "contains " + marker})
    if inspected == 0:
        respond(request_id, error={"code": "EVIDENCE_UNAVAILABLE", "message": "no readable text files in scope"})
        return 0
    violated = bool(findings)
    respond(request_id, check={
        "id": "check-" + request_id[:8], "policyId": POLICY, "providerId": PROVIDER["name"],
        "status": "FAIL" if violated else "PASS", "inspected": inspected, "findings": findings,
        "diagnostics": [], "exitCode": 1 if violated else 0,
    })
    return 0


if __name__ == "__main__":
    sys.exit(main())
