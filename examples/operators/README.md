# Built-in operator example

This project challenges three offline guards with the [built-in operators](../../docs/mutation-operators.md), using an `invaris.dev/v1alpha4` harness. It needs only a POSIX shell, `grep` and `printf`.

| Challenge | Operator | Target | Guard policy |
|---|---|---|---|
| `spacing` | `trailing-whitespace` | `notes.txt` | `trailing-whitespace` |
| `line-ending` | `crlf-injection` | `lines.txt` | `line-endings` |
| `leaked-token` | `secret-leak-injection` | `sample.env` | `synthetic-secret` |

`scan.sh` is the guard: it prints one `invaris.inspection/v1alpha1` document for the policy and file it is given and exits 1 when it finds a violation. The secret policy recognizes only the documented synthetic token, never a real credential.

From this directory, with `invaris` on `PATH` (or `go run ../../cmd/invaris` from a checkout):

```sh
invaris check
invaris challenge --format text
invaris campaign --format compact
```

`check` passes on the unmodified files. `challenge` reports each challenge `PROVEN`: the baseline passed, the operator made its one edit in a disposable copy, and the guard reported exactly the expected policy at the target. `campaign` repeats `spacing` twice and reports a 100% proof rate. All three emit `invaris.evidence/v1alpha5` JSON by default; the [portable profile](../../docs/evidence-schema.md#portable-profile-v1alpha5) withholds paths, arguments and output.

Each challenge pins `preimageSha256` to the target's exact bytes. If you edit a target, the challenge becomes `INCONCLUSIVE` with reason `mutation_rejected` until you update the digest:

```sh
shasum -a 256 notes.txt
```
