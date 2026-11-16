// Package explain holds the authoritative meaning and next step for every
// reason the engine can record.
package explain

import "github.com/akkaraponph/invaris/internal/domain"

// Guidance explains one recorded reason. Meaning states what the record says
// happened; Next names a concrete follow-up action. Both are single lines.
type Guidance struct{ Meaning, Next string }

// GuidanceFor returns the guidance for a known reason.
func GuidanceFor(reason string) (Guidance, bool) {
	g, ok := guidance[reason]
	return g, ok
}

var guidance = map[string]Guidance{
	domain.ReasonGuardDetected: {
		Meaning: "The guard ran on the edited copy and reported the violation.",
		Next:    "No action needed for this edit; keep the guard and review the other recorded edits in docs/campaigns.md.",
	},
	domain.ReasonMarkerMatched: {
		Meaning: "The expected text marker appeared in the guard output; this is weak evidence, a text match and not structured rule detection.",
		Next:    "Prefer a guard adapter that reports structured findings (see docs/adapter-protocol.md), or confirm the marker is specific to the rule.",
	},
	domain.ReasonGuardPassed: {
		Meaning: "The guard ran on the edited copy and found nothing.",
		Next:    "The guard does not cover this edit. Extend the guard or accept the gap; this is not proof the rule is enforced.",
	},
	domain.ReasonMarkerMissing: {
		Meaning: "The guard ran but the expected text marker was not in its output; this is weak evidence, a text match and not structured rule detection.",
		Next:    "Check that the marker matches what the guard prints, then extend the guard if the edit should be caught (see docs/authoring.md).",
	},
	domain.ReasonAssertionFail: {
		Meaning: "At least one test failed on the edited copy, so the edit was caught.",
		Next:    "No action needed for this edit; the tests cover it.",
	},
	domain.ReasonAllTestsPassed: {
		Meaning: "Every test passed on the edited copy, so no test caught the edit.",
		Next:    "Add or strengthen a test that fails on this edit, or accept the gap.",
	},
	domain.ReasonMutantInvalid: {
		Meaning: "The edited copy did not build or load, so it could not be judged as a test result.",
		Next:    "Review the mutation operator and target (see docs/mutation-operators.md); an edit that breaks the build says nothing about the tests.",
	},
	domain.ReasonInvalidTarget: {
		Meaning: "The configured target is not valid, so no edit was attempted.",
		Next:    "Correct the target in the configuration (see docs/authoring.md) and run again.",
	},
	domain.ReasonGuardNotFound: {
		Meaning: "The challenge names a guard that is not defined in the configuration, so no guard was run.",
		Next:    "Make the challenge's guard ID match a configured guard (see docs/authoring.md) and run again.",
	},
	domain.ReasonProviderUnavailable: {
		Meaning: "The provider needed for this experiment was not available, so the check was not performed.",
		Next:    "Check the provider setup (see docs/adapter-protocol.md) and run again; this says nothing about the guard.",
	},
	domain.ReasonUnhealthyBaseline: {
		Meaning: "The unmodified copy already failed, so the edit could not be judged.",
		Next:    "Fix the baseline before reading any challenge result.",
	},
	domain.ReasonMutationRejected: {
		Meaning: "The edit could not be applied to the copy, so no verdict was reached.",
		Next:    "Check that the target text still exists and matches the operator (see docs/mutation-operators.md).",
	},
	domain.ReasonNoTargetsInspected: {
		Meaning: "The guard ran but inspected no targets, so it could neither confirm nor refute the rule.",
		Next:    "Check the paths and target selection the guard is given (see docs/authoring.md).",
	},
	domain.ReasonFindingMismatch: {
		Meaning: "The guard did not report exactly one finding with the expected policy, rule and mutated path, so the result could not be attributed to the intended rule.",
		Next:    "Compare the expected finding with the recorded evidence (see docs/evidence-schema.md) and fix the expectation or the edit.",
	},
	domain.ReasonPrepareFailed: {
		Meaning: "The workspace copy could not be prepared, so the experiment did not run.",
		Next:    "Check disk space and read permissions on the project, then run again.",
	},
	domain.ReasonTimeout: {
		Meaning: "A step exceeded its time limit and was stopped, so the check did not finish.",
		Next:    "Check whether the command hangs, or raise its timeout in the configuration if it is legitimately slow.",
	},
	domain.ReasonCanceled: {
		Meaning: "The run was canceled before the check finished.",
		Next:    "Run again and let it finish; this says nothing about the guard.",
	},
	domain.ReasonMissingExecutable: {
		Meaning: "The command's program could not be found, so the command did not run.",
		Next:    "Check that the command's program is installed and that its envAllowlist includes PATH; `invaris doctor` is planned to inspect this once available.",
	},
	domain.ReasonOutputLimit: {
		Meaning: "A command produced more output than the allowed limit and was stopped, so the check did not finish.",
		Next:    "Reduce the command's output; each stream is capped at 64 KiB and the limit is not configurable.",
	},
	domain.ReasonExecutionFailed: {
		Meaning: "A command could not be executed for a reason other than a missing program, so the check did not complete.",
		Next:    "Inspect the recorded command error in the evidence (see docs/evidence-schema.md) and fix the harness setup.",
	},
	domain.ReasonCleanupFailed: {
		Meaning: "The workspace copy could not be fully removed after the run.",
		Next:    "Remove the leftover copy manually and check its permissions; treat the recorded result with care.",
	},
	domain.ReasonMalformedEvidence: {
		Meaning: "The evidence produced by a tool could not be parsed, so it was not trusted.",
		Next:    "Check the tool's output against docs/evidence-schema.md and docs/adapter-protocol.md.",
	},
	domain.ReasonWorkspaceChanged: {
		Meaning: "The workspace changed during the run in a way the experiment did not make, or no longer matched the snapshot, so the result was not trusted.",
		Next:    "Make sure the guard and test commands do not write into the project, do not edit the project during a run, then run again (see docs/threat-model.md).",
	},
}
