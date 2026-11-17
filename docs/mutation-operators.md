# Built-in mutation operators

Built-in operators let a guard challenge declare a small, exact edit by name instead of writing `before` and `after` strings. They are available in [`invaris.dev/v1alpha4` guard harnesses](harness-spec.md#v1alpha4-built-in-operators) and are used only to challenge guards. The behavioral `mutate` action keeps its literal mutations: `replace` and `delete` (see the [single-mutant harness](harness-spec.md#single-mutant-harness)).

Each operator is a pure function of the target file's exact bytes. It does not parse source code, guess comment syntax, read other files or run anything. The same input bytes always give the same output bytes. Any change to an operator's output requires a new operator version, and version `"1"` is the only version.

## Eligible files

Every operator requires a target file that:

- matches the declared `preimageSha256` (lowercase hex SHA-256 of the whole file) when the edit is applied, after the baseline ran;
- is a nonempty, valid UTF-8 text file with no NUL bytes and at most 10 MiB, before and after the edit;
- uses LF line endings only: any carriage return (CRLF, mixed or lone CR) makes the file ineligible;
- is a regular, owner-writable file at an exact `writablePaths` entry, reached without symbolic links.

An ineligible file is not an error in the harness. The challenge is `INCONCLUSIVE` with reason `mutation_rejected`, nothing is written, and the guard is not run on a changed file. An unknown operator, version or fixture is a harness error refused before anything runs.

## Catalog

| Operator | Exact change | Ineligible when |
|---|---|---|
| `trailing-whitespace` | Finds the first nonempty, LF-terminated line whose last byte is neither a space nor a tab, and inserts one ASCII space before its LF. Nothing else changes. | No such line (for example, every line already ends in whitespace, or the only text is an unterminated last line). |
| `crlf-injection` | Replaces the first LF with CRLF: exactly one carriage return is added and the number of LFs is unchanged. | The file has no LF. |
| `secret-leak-injection` | Appends exactly `INVARIS_SYNTHETIC_SECRET=INVARIS_TEST_TOKEN_DO_NOT_USE_00000000` followed by an LF. Requires `"fixture": "invaris-test-token-v1"`. | The file does not end with an LF, or it already contains the token. |

Examples, with `\n` and `\r` standing for the bytes:

| Operator | Before | After |
|---|---|---|
| `trailing-whitespace` | `\n\nx \nz\nw\n` | `\n\nx \nz \nw\n` |
| `crlf-injection` | `a\nb\n` | `a\r\nb\n` |
| `secret-leak-injection` | `KEY=value\n` | `KEY=value\nINVARIS_SYNTHETIC_SECRET=INVARIS_TEST_TOKEN_DO_NOT_USE_00000000\n` |

## The synthetic secret

`secret-leak-injection` inserts a fixed, documented string. It is not a credential for any service, and Invaris never looks it up, verifies it or sends it anywhere. Point it at a dedicated text fixture, such as a sample `.env` file, never at executable source or a file with real credentials.

There is no universal scanner compatibility. A secret scanner detects this token only if it has a rule that recognizes it; configure that rule explicitly, with the guard's `policyId` as the reported rule. Prefer an offline scanner: Invaris does not restrict a trusted guard's network access.

## What the operators do not do

- They do not prove that a guard catches every instance of a problem, only this one edit.
- They do not check that the edited file still compiles or parses; choose a target where the edit is meaningful.
- They do not apply to the behavioral `mutate` action or to Git metadata, which challenge copies exclude.
