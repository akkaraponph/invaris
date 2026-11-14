package evidence

import (
	"slices"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

const MutationSchemaVersion = "invaris.evidence/v1alpha3"

type MutationExperiment struct {
	ID                  string                 `json:"id"`
	MutationID          string                 `json:"mutationId"`
	EngineVersion       *string                `json:"engineVersion"`
	Operator            string                 `json:"operator"`
	Target              string                 `json:"target"`
	TestCommandID       string                 `json:"testCommandId"`
	ValidationCommandID string                 `json:"validationCommandId"`
	TestIDs             []string               `json:"testIds"`
	Verdict             domain.MutationVerdict `json:"verdict"`
	Reason              string                 `json:"reason"`
	Mutation            *domain.Mutation       `json:"mutation"`
	Stages              []domain.Stage         `json:"stages"`
	Baseline            *MutationCommandResult `json:"baseline"`
	Validation          *MutationCommandResult `json:"validation"`
	Tests               *MutationCommandResult `json:"tests"`
}

type MutationCommandResult struct {
	ObservationID string                  `json:"observationId"`
	Tests         []domain.TestCaseResult `json:"tests"`
	Validity      string                  `json:"validity"`
}

func validateMutationReport(r Report, bad func(string, ...any)) {
	e := r.Experiment
	if e == nil {
		bad("mutate requires experiment evidence")
		return
	}
	if len(r.Steps)+len(r.Checks)+len(r.Proofs)+len(r.Policies)+len(r.Lifecycle)+len(r.Gate.RequiredGuards)+len(r.Gate.RequiredProofs) != 0 ||
		r.Gate.AllowWeakEvidence || r.TargetCount != nil || r.Compatibility.Status != "native" || r.Compatibility.InputVersion != "invaris.dev/v1alpha3" {
		bad("mutation evidence has incompatible report fields")
	}
	if r.Decision != domain.MutationDecision(e.Verdict) {
		bad("mutation decision disagrees with verdict")
	}
	for _, id := range []string{e.ID, e.MutationID, e.TestCommandID, e.ValidationCommandID} {
		if id == "" || strings.TrimSpace(id) != id {
			bad("invalid experiment, mutation or command ID")
		}
	}
	if e.TestCommandID == e.ValidationCommandID || e.Operator != "replace" || !workspace.ValidPath(e.Target) {
		bad("invalid mutation operator, target or command IDs")
	}
	if len(e.TestIDs) == 0 || len(e.TestIDs) > 128 {
		bad("invalid test selection count")
	}
	seenIDs := make(map[string]bool, len(e.TestIDs))
	for _, id := range e.TestIDs {
		if id == "" || strings.TrimSpace(id) != id || seenIDs[id] {
			bad("invalid or duplicate selected test ID")
		}
		seenIDs[id] = true
	}

	order := []string{"prepare", "baseline", "mutate", "validation", "tests"}
	stages := make(map[string]string, len(e.Stages))
	for i, s := range e.Stages {
		switch {
		case s.Name == "cleanup":
			if i != len(e.Stages)-1 || stages["prepare"] != domain.StageOK {
				bad("cleanup stage is out of order")
			}
		case i >= len(order) || s.Name != order[i] || i > 0 && stages[order[i-1]] != domain.StageOK:
			bad("mutation stage %q is out of order", s.Name)
		}
		if _, exists := stages[s.Name]; exists || s.DurationMs < 0 ||
			s.Status != domain.StageOK && s.Status != domain.StageFailed {
			bad("invalid or repeated mutation stage %q", s.Name)
		}
		stages[s.Name] = s.Status
	}
	if stages["prepare"] == domain.StageOK && stages["cleanup"] == "" {
		bad("prepared mutation workspace requires cleanup evidence")
	}
	if m := e.Mutation; m != nil {
		if stages["mutate"] == "" || m.Path != e.Target || !hashPattern.MatchString(m.BeforeSHA256) ||
			!hashPattern.MatchString(m.AfterSHA256) || m.BeforeSHA256 == m.AfterSHA256 {
			bad("mutation evidence has an invalid target, digest or stage")
		}
	} else if stages["mutate"] == domain.StageOK {
		bad("successful mutation stage requires recorded mutation")
	}

	n := 0
	var baselineCommand *Observation
	exitFor := func(kind string, result *MutationCommandResult) int {
		if result == nil {
			if stages[kind] == domain.StageOK || kind != "baseline" && stages[kind] != "" {
				bad("%s stage requires a command result", kind)
			}
			return -1
		}
		if n >= len(r.Observations) || stages[kind] == "" {
			bad("%s command result requires a stage and observation", kind)
			return -1
		}
		o := r.Observations[n]
		n++
		switch kind {
		case "baseline":
			baselineCommand = &o
		case "tests":
			if baselineCommand == nil || o.Program != baselineCommand.Program || !slices.Equal(o.Args, baselineCommand.Args) {
				bad("baseline and mutant tests must use the same command")
			}
		}
		if result.ObservationID != e.ID+":"+kind || o.ID != result.ObservationID || o.Kind != kind ||
			o.StartedAt == nil || o.FinishedAt == nil || o.FinishedAt.Before(*o.StartedAt) {
			bad("inconsistent %s observation", kind)
		}
		exit, ok := -1, false
		if kind == "validation" {
			switch result.Validity {
			case domain.MutantValid:
				exit, ok = 0, true
			case domain.MutantInvalid:
				exit, ok = 1, true
			}
			if result.Tests != nil || result.Validity != "" && !ok {
				bad("malformed validity result")
			}
		} else {
			exit, ok = domain.TestExit(result.Tests, e.TestIDs)
			if result.Validity != "" || result.Tests != nil && !ok {
				bad("malformed %s test results", kind)
			}
		}
		if ok && (o.ExitCode == nil || *o.ExitCode != exit) {
			bad("%s result disagrees with its observation exit", kind)
		}
		if !ok {
			if stages[kind] == domain.StageOK {
				bad("successful %s stage requires parsed evidence", kind)
			}
			return -1
		}
		return exit
	}
	baseline := exitFor("baseline", e.Baseline)
	validity := exitFor("validation", e.Validation)
	tests := exitFor("tests", e.Tests)
	if n != len(r.Observations) || stages["baseline"] == domain.StageOK && baseline != 0 ||
		stages["tests"] != "" && validity != 0 {
		bad("unexpected observation, unhealthy baseline or test of an invalid mutant")
	}

	switch e.Verdict {
	case domain.MutationInconclusive:
		failedStage := ""
		for _, stage := range e.Stages {
			if stage.Status == domain.StageFailed {
				failedStage = stage.Name
			}
		}
		var validReason bool
		switch e.Reason {
		case "workspace_prepare_failed":
			validReason = failedStage == "prepare"
		case "unhealthy_baseline":
			validReason = failedStage == "baseline" && baseline == 1
		case "mutation_rejected":
			validReason = failedStage == "mutate"
		case "workspace_changed":
			validReason = failedStage == "baseline" || failedStage == "mutate" ||
				failedStage == "validation" || failedStage == "tests"
		case "malformed_evidence", "missing_executable", "output_limit", "execution_failed":
			validReason = failedStage == "baseline" && e.Baseline != nil && baseline == -1 ||
				failedStage == "validation" && validity == -1 ||
				failedStage == "tests" && tests == -1
		case "timeout", "canceled":
			validReason = failedStage != "cleanup" && failedStage != "prepare"
		case "cleanup_failed":
			validReason = failedStage == "cleanup"
		}
		if !validReason {
			bad("inconsistent inconclusive reason")
		}
	case domain.MutationKilled, domain.MutationSurvived, domain.MutationInvalid:
		if r.ConfigDigest == nil || stages["cleanup"] != domain.StageOK || stages["validation"] != domain.StageOK {
			bad("completed mutation verdict requires config digest, validation and verified cleanup")
		}
		switch e.Verdict {
		case domain.MutationKilled:
			if tests != 1 || stages["tests"] != domain.StageOK || e.Reason != "assertion_failed" {
				bad("KILLED requires completed assertion failure")
			}
		case domain.MutationSurvived:
			if tests != 0 || stages["tests"] != domain.StageOK || e.Reason != "all_tests_passed" {
				bad("SURVIVED requires completed passing tests")
			}
		case domain.MutationInvalid:
			if validity != 1 || stages["tests"] != "" || e.Reason != "mutant_invalid" {
				bad("INVALID requires an explicit validity failure before tests")
			}
		}
	default:
		bad("unknown mutation verdict %q", e.Verdict)
	}
}
