# Harness contract: v1alpha1

A harness is one UTF-8 JSON object, at most 1 MiB, with `version` set to `invaris.dev/v1alpha1` and a nonblank `name`. Field names are case-sensitive. Duplicate, unknown and null fields, null array elements, and trailing JSON values are rejected. The same semantic validation applies to `Load` and direct calls to `Run`, before commands execute.

`steps` and `guards` contain commands with a unique `id` and a nonblank `program`. Optional `args` are literal arguments (including empty strings), not shell expressions; a harness can explicitly invoke a shell. `timeoutSeconds` is an integer from 0 to 300; zero selects the 30-second default. `envAllowlist` names inherited variables. At most 128 steps, guards and challenges may be declared in total, and their IDs share a namespace.

A `challenge` names a guard and a relative `file`, and supplies distinct nonempty `before` and `after` strings. Legacy guards also require `expectedOutput`; structured guards use `policyId` and the [inspection contract](inspection.md). Paths use forward slashes and cannot be absolute, contain empty, `.` or `..` segments, backslashes, or drive prefixes.

Optional `policies` declares permitted policy IDs. Optional `networkPolicy` currently accepts only `unrestricted`; it does not provide network isolation. Optional `writablePaths` restricts challenge targets to the listed exact paths: omission retains legacy behavior, whereas an explicit empty list permits no mutation. These declarations do not confine trusted commands. Nonempty `requiredAdapters` is refused because adapter negotiation is not implemented.

The existing v1alpha1 examples remain valid. Invalid configuration is refused before execution. Reports currently use `invaris.evidence/v1alpha1`; the planned v1alpha2 evidence format is not implemented.

For `run`, optional `failFast` and `alwaysRunGuards` booleans both default to true. When `failFast` is false, later steps are attempted after a failed step. When `alwaysRunGuards` is false, guards are skipped after a step failure. Skipped steps and guards appear as `NOT_RUN` with a reason; a skipped guard cannot produce a passing action. Cancellation also records unstarted work as `NOT_RUN`.
