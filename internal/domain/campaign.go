package domain

import (
	"slices"
	"strconv"
	"strings"
)

// Campaign rules. A campaign runs existing experiments; it never changes how a
// single attempt is judged. These functions only combine attempt verdicts into
// per-experiment results, summaries and a final acceptance decision.

// ExperimentKind names the experiment category a campaign entry references.
type ExperimentKind string

const (
	KindMutation  ExperimentKind = "mutation"
	KindGuardrail ExperimentKind = "guardrail"
)

// Selection says whether an experiment is in the scope of this run.
type Selection string

const (
	Selected    Selection = "SELECTED"
	FilteredOut Selection = "FILTERED_OUT"
)

// Execution says what happened to one attempt. Only EXECUTED attempts can
// carry a conclusive verdict.
type Execution string

const (
	Executed   Execution = "EXECUTED"    // the experiment lifecycle ran to its end
	Canceled   Execution = "CANCELED"    // the lifecycle started and was interrupted
	Refused    Execution = "REFUSED"     // the engine would not start it (tainted snapshot)
	NotStarted Execution = "NOT_STARTED" // never started (cancellation, failed preparation)
)

// Stability classifies the attempts of one experiment.
type Stability string

const (
	Consistent   Stability = "CONSISTENT"    // every attempt has the same conclusive verdict
	Mixed        Stability = "MIXED"         // every attempt is conclusive, but they disagree
	Incomplete   Stability = "INCOMPLETE"    // at least one attempt has no conclusive verdict
	NotEvaluated Stability = "NOT_EVALUATED" // no attempts (filtered out)
)

// Requirement statuses in an acceptance evaluation.
const (
	RequirementSatisfied     = "SATISFIED"
	RequirementViolated      = "VIOLATED"
	RequirementUnsupported   = "UNSUPPORTED"
	RequirementNotApplicable = "NOT_APPLICABLE"
)

// Requirement names, equal to the JSON field names of AcceptancePolicy.
const (
	ReqNoSurvivors         = "requireNoSurvivors"
	ReqAllGuardrailsProven = "requireAllGuardrailsProven"
	ReqNoInconclusive      = "requireNoInconclusive"
	ReqNoMixed             = "requireNoMixed"
	ReqNoInvalid           = "requireNoInvalid"
	ReqMinMutationScore    = "minMutationScore"
)

// AttemptOutcome is one attempt as far as aggregation is concerned. Verdict is
// empty when the attempt produced no child evidence.
type AttemptOutcome struct {
	Execution Execution
	Verdict   string
}

// ExperimentOutcome is one campaign experiment after aggregation. Verdict is
// empty unless Stability is Consistent. Ran counts attempts that were Executed
// or Canceled.
type ExperimentOutcome struct {
	ID        string
	Kind      ExperimentKind
	Selection Selection
	Stability Stability
	Verdict   string
	Ran       int
}

// AcceptancePolicy is the typed campaign acceptance policy. It is not a
// language: each field is one fixed rule.
type AcceptancePolicy struct {
	RequireNoSurvivors         bool     `json:"requireNoSurvivors"`
	RequireAllGuardrailsProven bool     `json:"requireAllGuardrailsProven"`
	RequireNoInconclusive      bool     `json:"requireNoInconclusive"`
	RequireNoMixed             bool     `json:"requireNoMixed"`
	RequireNoInvalid           bool     `json:"requireNoInvalid"`
	MinMutationScore           *float64 `json:"minMutationScore"`
}

// RequirementResult is the outcome of one enabled requirement. Experiments
// lists, in declaration order, the experiments that made it VIOLATED or
// UNSUPPORTED.
type RequirementResult struct {
	Requirement string   `json:"requirement"`
	Status      string   `json:"status"`
	Experiments []string `json:"experiments"`
}

// AcceptanceEvaluation records whether the campaign ran to completion and the
// result of every enabled requirement in policy field order.
type AcceptanceEvaluation struct {
	Complete     bool                `json:"complete"`
	Requirements []RequirementResult `json:"requirements"`
}

// MutationSummary counts mutation experiments (not attempts). Every experiment
// is in exactly one of the first seven buckets. Score is killed / eligible over
// consistent KILLED and SURVIVED experiments, and null when nothing is eligible.
type MutationSummary struct {
	Killed       int      `json:"killed"`
	Survived     int      `json:"survived"`
	Invalid      int      `json:"invalid"`
	Inconclusive int      `json:"inconclusive"`
	Mixed        int      `json:"mixed"`
	NotExecuted  int      `json:"notExecuted"`
	Filtered     int      `json:"filtered"`
	Eligible     int      `json:"eligible"`
	Excluded     int      `json:"excluded"`
	Score        *float64 `json:"score"`
}

// GuardrailSummary counts guardrail experiments. ProofRate is proven / eligible
// over consistent PROVEN and UNPROVEN experiments, and null when nothing is
// eligible. Campaigns accept only strong proofs, so every PROVEN counted here
// matched the expected policy, rule and target. The rate is never combined
// with the mutation score.
type GuardrailSummary struct {
	Proven       int      `json:"proven"`
	Unproven     int      `json:"unproven"`
	Inconclusive int      `json:"inconclusive"`
	Mixed        int      `json:"mixed"`
	NotExecuted  int      `json:"notExecuted"`
	Filtered     int      `json:"filtered"`
	Eligible     int      `json:"eligible"`
	Excluded     int      `json:"excluded"`
	ProofRate    *float64 `json:"proofRate"`
}

func conclusive(kind ExperimentKind, verdict string) bool {
	switch kind {
	case KindMutation:
		return verdict == string(MutationKilled) || verdict == string(MutationSurvived) || verdict == string(MutationInvalid)
	case KindGuardrail:
		return verdict == string(Proven) || verdict == string(Unproven)
	}
	return false
}

// Aggregate combines the attempts of one experiment. It never votes: a verdict
// is returned only when every attempt was executed and reached the same
// conclusive verdict. Any attempt without one makes the evidence INCOMPLETE,
// which takes precedence over disagreement (MIXED).
func Aggregate(kind ExperimentKind, attempts []AttemptOutcome) (string, Stability, string) {
	if len(attempts) == 0 {
		return "", NotEvaluated, "no attempts"
	}
	var missing, distinct []string
	for i, a := range attempts {
		if a.Execution != Executed || !conclusive(kind, a.Verdict) {
			missing = append(missing, strconv.Itoa(i+1))
		} else if !slices.Contains(distinct, a.Verdict) {
			distinct = append(distinct, a.Verdict)
		}
	}
	switch {
	case len(missing) > 0:
		return "", Incomplete, "attempts without a conclusive verdict: " + strings.Join(missing, ", ")
	case len(distinct) == 1:
		return distinct[0], Consistent, ""
	default:
		return "", Mixed, "attempts disagree: " + strings.Join(distinct, ", ")
	}
}

// SummarizeMutations counts the mutation experiments in xs.
func SummarizeMutations(xs []ExperimentOutcome) MutationSummary {
	var s MutationSummary
	for _, x := range xs {
		if x.Kind != KindMutation {
			continue
		}
		switch {
		case x.Selection != Selected:
			s.Filtered++
		case x.Stability == Consistent && x.Verdict == string(MutationKilled):
			s.Killed++
		case x.Stability == Consistent && x.Verdict == string(MutationSurvived):
			s.Survived++
		case x.Stability == Consistent && x.Verdict == string(MutationInvalid):
			s.Invalid++
		case x.Stability == Mixed:
			s.Mixed++
		case x.Ran > 0:
			s.Inconclusive++
		default:
			s.NotExecuted++
		}
	}
	s.Eligible = s.Killed + s.Survived
	s.Excluded = s.Invalid + s.Inconclusive + s.Mixed + s.NotExecuted
	if s.Eligible > 0 {
		score := float64(s.Killed) / float64(s.Eligible)
		s.Score = &score
	}
	return s
}

// SummarizeGuardrails counts the guardrail experiments in xs.
func SummarizeGuardrails(xs []ExperimentOutcome) GuardrailSummary {
	var s GuardrailSummary
	for _, x := range xs {
		if x.Kind != KindGuardrail {
			continue
		}
		switch {
		case x.Selection != Selected:
			s.Filtered++
		case x.Stability == Consistent && x.Verdict == string(Proven):
			s.Proven++
		case x.Stability == Consistent && x.Verdict == string(Unproven):
			s.Unproven++
		case x.Stability == Mixed:
			s.Mixed++
		case x.Ran > 0:
			s.Inconclusive++
		default:
			s.NotExecuted++
		}
	}
	s.Eligible = s.Proven + s.Unproven
	s.Excluded = s.Inconclusive + s.Mixed + s.NotExecuted
	if s.Eligible > 0 {
		rate := float64(s.Proven) / float64(s.Eligible)
		s.ProofRate = &rate
	}
	return s
}

// EvaluateAcceptance applies the policy to the selected experiments.
//
// Precedence: a campaign that did not run to completion is REFUSE. Otherwise
// any UNSUPPORTED requirement (missing, incomplete or contradictory evidence)
// is REFUSE, then any VIOLATED requirement is REJECT, and ACCEPT needs at least
// one SATISFIED requirement. Requirements about a kind with no selected
// experiment are NOT_APPLICABLE; they never satisfy anything.
func EvaluateAcceptance(p AcceptancePolicy, xs []ExperimentOutcome, complete bool) (AcceptanceEvaluation, Decision) {
	var mutations, all []ExperimentOutcome
	var guardrails []ExperimentOutcome
	for _, x := range xs {
		if x.Selection != Selected {
			continue
		}
		all = append(all, x)
		switch x.Kind {
		case KindMutation:
			mutations = append(mutations, x)
		case KindGuardrail:
			guardrails = append(guardrails, x)
		}
	}
	eval := AcceptanceEvaluation{Complete: complete, Requirements: []RequirementResult{}}
	ids := func(set []ExperimentOutcome, match func(ExperimentOutcome) bool) []string {
		out := []string{}
		for _, x := range set {
			if match(x) {
				out = append(out, x.ID)
			}
		}
		return out
	}
	notConsistent := func(x ExperimentOutcome) bool { return x.Stability != Consistent }
	// forbid: VIOLATED by each consistent experiment with the bad verdict, else
	// UNSUPPORTED by each experiment whose verdict is unknown, else SATISFIED.
	forbid := func(name string, set []ExperimentOutcome, bad string) {
		r := RequirementResult{Requirement: name, Status: RequirementSatisfied, Experiments: []string{}}
		if len(set) == 0 {
			r.Status = RequirementNotApplicable
		} else if v := ids(set, func(x ExperimentOutcome) bool { return x.Stability == Consistent && x.Verdict == bad }); len(v) > 0 {
			r.Status, r.Experiments = RequirementViolated, v
		} else if u := ids(set, notConsistent); len(u) > 0 {
			r.Status, r.Experiments = RequirementUnsupported, u
		}
		eval.Requirements = append(eval.Requirements, r)
	}
	unsupportedBy := func(name string, match func(ExperimentOutcome) bool) {
		r := RequirementResult{Requirement: name, Status: RequirementSatisfied, Experiments: []string{}}
		if u := ids(all, match); len(u) > 0 {
			r.Status, r.Experiments = RequirementUnsupported, u
		}
		eval.Requirements = append(eval.Requirements, r)
	}

	if p.RequireNoSurvivors {
		forbid(ReqNoSurvivors, mutations, string(MutationSurvived))
	}
	if p.RequireAllGuardrailsProven {
		forbid(ReqAllGuardrailsProven, guardrails, string(Unproven))
	}
	if p.RequireNoInconclusive {
		unsupportedBy(ReqNoInconclusive, func(x ExperimentOutcome) bool { return x.Stability == Incomplete || x.Stability == NotEvaluated })
	}
	if p.RequireNoMixed {
		unsupportedBy(ReqNoMixed, func(x ExperimentOutcome) bool { return x.Stability == Mixed })
	}
	if p.RequireNoInvalid {
		forbid(ReqNoInvalid, mutations, string(MutationInvalid))
	}
	if p.MinMutationScore != nil {
		r := RequirementResult{Requirement: ReqMinMutationScore, Status: RequirementSatisfied, Experiments: []string{}}
		s := SummarizeMutations(mutations)
		switch {
		case len(mutations) == 0:
			r.Status = RequirementNotApplicable
		case s.Score == nil:
			r.Status = RequirementUnsupported
		case *s.Score < *p.MinMutationScore:
			r.Status = RequirementViolated
			r.Experiments = ids(mutations, func(x ExperimentOutcome) bool {
				return x.Stability == Consistent && x.Verdict == string(MutationSurvived)
			})
		}
		eval.Requirements = append(eval.Requirements, r)
	}

	if !complete {
		return eval, RefuseDecision
	}
	var violated, unsupported, satisfied bool
	for _, r := range eval.Requirements {
		switch r.Status {
		case RequirementViolated:
			violated = true
		case RequirementUnsupported:
			unsupported = true
		case RequirementSatisfied:
			satisfied = true
		}
	}
	switch {
	case unsupported:
		return eval, RefuseDecision
	case violated:
		return eval, Reject
	case satisfied:
		return eval, Accept
	}
	return eval, RefuseDecision
}
