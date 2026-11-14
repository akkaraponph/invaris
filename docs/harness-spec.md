# Harness contract: v1alpha1 and v1alpha2

This page specifies `invaris.dev/v1alpha1` first and then the additions of `invaris.dev/v1alpha2` (see [v1alpha2 additions](#v1alpha2-additions)). Both versions emit `invaris.evidence/v1alpha2`; see [evidence-schema.md](evidence-schema.md).

A harness is one UTF-8 JSON object, at most 1 MiB, with `version` set to `invaris.dev/v1alpha1` and a nonblank `name`. Field names are case-sensitive. Duplicate, unknown and null fields, null array elements, and trailing JSON values are rejected. The same semantic validation applies to `Load` and direct calls to `Run`, before commands execute.

`steps` and `guards` contain commands with a unique `id` and a nonblank `program`. Optional `args` are literal arguments (including empty strings), not shell expressions; a harness can explicitly invoke a shell. `timeoutSeconds` is an integer from 0 to 300; zero selects the 30-second default. `envAllowlist` names inherited variables. At most 128 steps, guards and challenges may be declared in total, and their IDs share a namespace.

A `challenge` names a guard and a relative `file`, and supplies distinct nonempty `before` and `after` strings. Legacy guards also require `expectedOutput`; structured guards use `policyId` and the [inspection contract](inspection.md). Paths use forward slashes and cannot be absolute, contain empty, `.` or `..` segments, backslashes, or drive prefixes.

Optional `policies` declares permitted policy IDs. Optional `networkPolicy` currently accepts only `unrestricted`; it does not provide network isolation. Optional `writablePaths` restricts challenge targets to the listed exact paths: omission retains legacy behavior, whereas an explicit empty list permits no mutation. These declarations do not confine trusted commands. Nonempty `requiredAdapters` is refused because adapter negotiation is not implemented.

The existing v1alpha1 examples remain valid. Invalid configuration is refused before execution. Reports are now emitted as `invaris.evidence/v1alpha2` for both input versions; the v1alpha1 report shape is no longer produced (see the migration notes in [evidence-schema.md](evidence-schema.md#migrating-from-invarisevidencev1alpha1)).

For `run`, optional `failFast` and `alwaysRunGuards` booleans both default to true. When `failFast` is false, later steps are attempted after a failed step. When `alwaysRunGuards` is false, guards are skipped after a step failure. Skipped steps and guards appear as `NOT_RUN` with a reason; a skipped guard cannot produce a passing action. Cancellation also records unstarted work as `NOT_RUN`.

## v1alpha2 additions

`invaris.dev/v1alpha2` keeps the command, guard, challenge and scheduling fields above and makes trust and gating explicit. Its published schema is [`schemas/harness-v1alpha2.schema.json`](../schemas/harness-v1alpha2.schema.json) (Draft 2020-12); the schema checks shape, and the Go loader additionally enforces duplicate keys, ID uniqueness, gate references, path safety and size limits. Unknown, duplicate and null fields are rejected exactly as in v1alpha1.

| Field | Contract |
| --- | --- |
| `trustMode` | Required; only `trusted-local`. The local backend is not a sandbox. |
| `networkPolicy` | Required; only `unrestricted`. It is a declaration, not isolation. |
| `writablePaths` | Optional; omission means **no** mutation (v1alpha1 derived write paths from challenges). |
| `policies` | Objects `{"id": ..., "allowEmpty": false}`. A bare string is rejected. |
| `gate` | `{requiredGuards, requiredProofs, precedence, allowWeakEvidence}`; references must name declared guards and challenges. `precedence` is `failure-first` (default) or `refusal-first`; `allowWeakEvidence` defaults to false. |
| `redactEnv` | Names of environment variables whose values are redacted from evidence. Values are never serialized. |
| `storeSensitiveOutput` | Default false; raises the per-stream excerpt bound from 4 KiB to 64 KiB. |
| `adapters`, provider `guards`, `requiredAdapters` | Declared in the schema so the contract is published; **refused at load time** until negotiated providers are executable. An unmet provider requirement never falls back to a local command. |

A guard is a strict union: the command form (`program`, optional `args`, `policyId`) or the provider form (`adapterId`, `policyId`, `scope`, opaque `options`). Mixing fields is invalid.

### Gate semantics

The gate is applied per action to the results that action can produce: `run` and `check` evaluate `requiredGuards`, `challenge` evaluates `requiredProofs`. Entries the action cannot produce are not applied to it, and the report records the gate actually applied. An action whose applied gate requires nothing and which has no steps is refused as empty work. Without a configured gate every declared guard (or challenge) is required. The decision is ACCEPT only when every step passed, every required result is present and satisfying, and no failing or refusing result exists. v1alpha1 inputs get a derived gate that permits weak (marker-based) proofs for compatibility; v1alpha2 rejects a weak PROVEN proof unless `allowWeakEvidence` is true.
