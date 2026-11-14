# Invaris architecture — v0 foundation

## Purpose

Invaris provides a tool-neutral execution and evidence boundary. A harness specifies a task, checks, and intentional failure experiments. The engine executes them, preserves result provenance, and never promotes absence of evidence to a pass.

## Current modules

- `cmd/invaris`: command-line interface and exit-code mapping.
- `internal/engine`: versioned JSON contract, command execution, proof experiments and structured reports.
- `examples`: runnable local demonstration with a deliberately injected violation.

The implementation is a single local process. There is no daemon, database, agent loop, or background worker.

## Result semantics

| Concept | Meaning |
| --- | --- |
| Check PASS | Command completed with exit 0. |
| Check FAIL | Command completed with nonzero exit. |
| Check REFUSE | Execution/evidence could not be trusted (e.g., timeout or executable missing). |
| Proof PROVEN | Healthy baseline passed; injected violation produced the expected failing guard output. |
| Proof UNPROVEN | Guard did not produce the expected failure evidence. |
| Proof INCONCLUSIVE | Proof experiment could not be reliably completed. |

The top-level action status is `PASS`, `FAIL` or `INCONCLUSIVE`/`REFUSE`. Proof status and action status are intentionally distinct.

## Next milestones

1. Introduce a versioned out-of-process adapter protocol (`discover`, `check`, `challenge`, `report`) with capabilities and structured rule IDs. Differentiate actual language support from mere command availability.
2. Replace the temporary-directory copier with safe worktree/container isolation, bounded resource accounting, process-group cleanup and explicit network/secret policies.
3. Add output and artifact digests, source revision references, redaction controls, and structured reports compatible with SARIF where applicable.
4. Build thin adapters for Dependency-Cruiser, Stryker and Promptfoo. Delegate native analysis rather than reimplementing it.
5. Add portable AI harness fixtures with blind evaluation cases, explicit model/tool provenance and nondeterminism reporting.

## Design decisions

- **Go core:** single executable and straightforward process orchestration.
- **JSON first:** built-in strict decoder; future configuration formats must compile to the same contract.
- **No implicit shell:** commands are arrays, though trusted configurations may explicitly request a shell.
- **No automatic agent authority:** a caller must explicitly opt into execution; skill instructions do not grant execution permissions.
- **No universal parser:** tool adapters retain responsibility for language-specific semantics.
