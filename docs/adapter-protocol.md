# Adapter protocol: `invaris.adapter/v1alpha1`

A provider is an external process that evaluates a policy with some tool and reports typed evidence. Invaris starts one process per request, writes one JSON request to its stdin, and reads one JSON response from its stdout. Providers may be written in any language and need no Invaris code. Providers run as trusted local commands; this protocol is not a sandbox.

Schema: [`schemas/adapter-v1alpha1.schema.json`](../schemas/adapter-v1alpha1.schema.json). Go client: `adapter.Call` in `internal/adapter`. Working example: [`adapters/example/main.py`](../adapters/example/main.py) (Python standard library only).

## Transport

- Request: one UTF-8 JSON object plus a newline on stdin, at most 1 MiB. stdin is then closed.
- Response: exactly one UTF-8 JSON value on stdout (surrounding whitespace is fine). More than one value, trailing text, invalid UTF-8, an empty body, duplicate object keys at any depth, unknown fields, and `null` where a list is required are all protocol violations.
- stdout and stderr are each limited to 64 KiB by the shared runner; exceeding it is a transport failure. stderr is diagnostic only and is never parsed.
- The process exit status is the **transport** status. A valid response, including a policy `FAIL`, is sent with exit **0**. A nonzero exit, timeout, cancellation, missing executable or truncated output means no response is trusted and the engine refuses. The timeout is the command's `timeoutSeconds` (default 30 s, maximum 300 s).
- The working directory is the project root. The environment is only the allowlisted variables plus a default `PATH`.

## Request

```json
{"version":"invaris.adapter/v1alpha1","requestId":"7f3c...","operation":"check",
 "policyIds":["no-forbidden-import"],"scope":["src"],"options":{"configPath":"rules.cjs"}}
```

`operation` is `discover`, `check`, `mutate` or (optionally) `doctor`. `requestId` is unique per request. `scope` entries are canonical relative paths (forward slashes; no absolute paths, `..`, `.` segments, empty segments, backslashes or drive prefixes); `.` is allowed only as a whole-project scope root. `check` and `mutate` require at least one policy ID and one scope entry. `options` is opaque, non-null JSON validated by the provider. The client rejects an invalid request without starting the provider.

## Response

Every response carries `version`, `requestId` (must equal the request's), and `provider` `{name, version, toolName, toolVersion}`; name and version are required, tool fields are nullable. Exactly one payload follows, matching the operation:

| Operation | Payload |
| --- | --- |
| `discover`, `doctor` | `capabilities` `{languages, policyIds, operations, proofTypes, reportFormats, available, prerequisites}`; every list present, entries unique and non-empty, operations from the four above. |
| `check` | `check`, a typed check (below). |
| `mutate` | `mutation` `{faultId, path, beforeSha256, afterSha256}`: path inside the scope, lowercase SHA-256 hashes that differ. |
| any | `error` `{code, message}`. A provider that cannot do something says so; it never returns a passing check for it. |

### Check semantics and exit status

`check` has the shape of the evidence-schema check: `id`, `policyId` (one of the requested policies), `providerId` (must equal `provider.name`), `status`, `inspected`, `findings`, `diagnostics`, `exitCode`. `exitCode` is the **native tool's** exit status; v1alpha1 permits only 0 (clean) and 1 (violation):

| `status` | Required |
| --- | --- |
| `PASS` | `inspected` > 0, `exitCode` 0, no findings. |
| `FAIL` | `inspected` > 0, `exitCode` 1, at least one finding. |
| `REFUSE` | At least one diagnostic, no findings; any `exitCode` (for example a crashed tool). Never accepts. |

`NOT_RUN` is not allowed from a provider. Evidence that contradicts the native status (for example `PASS` with exit 127, or `FAIL` with no findings) is a protocol violation. Each finding needs the requested `policyId`, a non-empty native `ruleId`, a canonical relative `path` inside the requested scope (an entry itself or beneath it; `src` does not cover `src2`), and a `message` (may be empty). Rule-specific proof correlation (does this finding match the fault that was injected?) is done by the proof stage, which knows the expected rule.

### Error codes

`INVALID_REQUEST`, `UNSUPPORTED_PROTOCOL`, `UNSUPPORTED_CAPABILITY`, `TOOL_UNAVAILABLE`, `EVIDENCE_UNAVAILABLE`, `EXECUTION_TIMEOUT`, `MALFORMED_OUTPUT`, `INTERNAL_PROVIDER_FAILURE`. An unknown code is a protocol violation. If the request itself cannot be parsed, the provider has no request ID to echo; it should exit nonzero.

### Policy violation versus failure

| Situation | Provider behavior | Client result |
| --- | --- | --- |
| Rule violated | exit 0, `check.status` `FAIL` with findings | success: a typed FAIL for the gate |
| Tool crashed, config invalid | exit 0, `check.status` `REFUSE` with diagnostics, or an `error` | refusal, never accepted |
| Provider process failed, timed out, output too large | nonzero exit or no output | `ErrTransport` |
| Response breaks a rule above | any | `ErrInvalidResponse`; no check is returned |
| Provider returned an `error` | exit 0 | the `*ProtocolError` itself |

`adapter.Call` returns `(Response, error)`; any non-nil error means refuse. Use `errors.Is(err, adapter.ErrTransport)`, `adapter.ErrInvalidResponse`, `adapter.ErrInvalidRequest`, or `errors.As(err, &*adapter.ProtocolError)`.

## Testing a third-party provider

Put the provider's argv in a JSON file (an array of strings, never a shell string) and run the conformance kit from a checkout of this repository:

```bash
echo '["/usr/bin/node", "/abs/path/to/provider.mjs"]' > /tmp/provider.json
INVARIS_CONFORMANCE_PROVIDER=/tmp/provider.json go test ./internal/adapter -run TestProviderConformance -count=1 -v
```

The kit needs no fixtures. It sends discover, then hostile requests (unsupported protocol version, unknown operation, garbage input, escaping or absolute scope, unknown policy, and `mutate` when you do not advertise it), and requires each to be refused with a typed `error` or a nonzero exit, never a check or mutation. `TestConformance` (no provider needed) documents how the client rejects bad responses; read its table to see every violation a response must avoid. Validate your own responses against the JSON Schema as well. To also check clean and violating output on your own fixtures, call `adapter.Call` in a small Go test as `TestExampleProvider` does for the bundled example.

Without the variable, the kit runs the bundled Python example; it is skipped, with a logged reason, when `python3` is absent. The example implements the check-only policy `no-forbidden-marker` (a file under scope containing `FORBIDDEN_MARKER`), reports `mutate` as `UNSUPPORTED_CAPABILITY`, and refuses empty evidence with `EVIDENCE_UNAVAILABLE`.

## Status

The protocol and client are implemented and tested. The engine does not yet launch providers: provider adapters and provider guards in `invaris.dev/v1alpha2` harnesses are still refused until negotiated providers are wired in.

See [Contributing](../CONTRIBUTING.md) for the release checks and security disclosure process.
