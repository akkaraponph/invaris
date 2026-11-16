package domain

import (
	"maps"
	"slices"
)

// Machine-readable reasons shared by proofs (`challenge`) and mutation
// experiments (`mutate`). A reason says why a verdict or an INCONCLUSIVE
// outcome was reached; the category says whose problem it is. The verdict
// vocabularies stay separate (PROVEN/UNPROVEN and KILLED/SURVIVED); only the
// failure vocabulary is common.
const (
	// Completed assessments.
	ReasonGuardDetected  = "guard_detected"   // PROVEN
	ReasonMarkerMatched  = "marker_matched"   // PROVEN, weak evidence
	ReasonGuardPassed    = "guard_passed"     // UNPROVEN
	ReasonMarkerMissing  = "marker_not_found" // UNPROVEN, weak evidence
	ReasonAssertionFail  = "assertion_failed" // KILLED
	ReasonAllTestsPassed = "all_tests_passed" // SURVIVED
	ReasonMutantInvalid  = "mutant_invalid"   // INVALID

	// The experiment as configured cannot give a meaningful answer.
	ReasonInvalidTarget       = "invalid_target"
	ReasonGuardNotFound       = "guard_not_found"
	ReasonProviderUnavailable = "provider_unavailable"
	ReasonUnhealthyBaseline   = "unhealthy_baseline"
	ReasonMutationRejected    = "mutation_rejected"
	ReasonNoTargetsInspected  = "no_targets_inspected"
	ReasonFindingMismatch     = "finding_mismatch"

	// The tools or the machine failed.
	ReasonPrepareFailed     = "workspace_prepare_failed"
	ReasonTimeout           = "timeout"
	ReasonCanceled          = "canceled"
	ReasonMissingExecutable = "missing_executable"
	ReasonOutputLimit       = "output_limit"
	ReasonExecutionFailed   = "execution_failed"
	ReasonCleanupFailed     = "cleanup_failed"

	// The evidence or the workspace could not be trusted.
	ReasonMalformedEvidence = "malformed_evidence"
	ReasonWorkspaceChanged  = "workspace_changed"
)

// Reason categories.
const (
	CategoryResult         = "result"
	CategorySetup          = "setup"
	CategoryInfrastructure = "infrastructure"
	CategoryEvidence       = "evidence"
)

var reasonCategory = map[string]string{
	ReasonGuardDetected: CategoryResult, ReasonMarkerMatched: CategoryResult, ReasonGuardPassed: CategoryResult,
	ReasonMarkerMissing: CategoryResult, ReasonAssertionFail: CategoryResult, ReasonAllTestsPassed: CategoryResult,
	ReasonMutantInvalid: CategoryResult,

	ReasonInvalidTarget: CategorySetup, ReasonGuardNotFound: CategorySetup, ReasonProviderUnavailable: CategorySetup,
	ReasonUnhealthyBaseline: CategorySetup, ReasonMutationRejected: CategorySetup,
	ReasonNoTargetsInspected: CategorySetup, ReasonFindingMismatch: CategorySetup,

	ReasonPrepareFailed: CategoryInfrastructure, ReasonTimeout: CategoryInfrastructure, ReasonCanceled: CategoryInfrastructure,
	ReasonMissingExecutable: CategoryInfrastructure, ReasonOutputLimit: CategoryInfrastructure,
	ReasonExecutionFailed: CategoryInfrastructure, ReasonCleanupFailed: CategoryInfrastructure,

	ReasonMalformedEvidence: CategoryEvidence, ReasonWorkspaceChanged: CategoryEvidence,
}

// ReasonCategory returns the category of a known reason, or "" for an unknown one.
func ReasonCategory(reason string) string { return reasonCategory[reason] }

// Reasons returns every known reason, sorted ascending.
func Reasons() []string { return slices.Sorted(maps.Keys(reasonCategory)) }
