package domain

import (
	"slices"
	"testing"
)

const (
	vK = string(MutationKilled)
	vS = string(MutationSurvived)
	vI = string(MutationInvalid)
	vX = string(MutationInconclusive)
)

func executed(verdicts ...string) []AttemptOutcome {
	out := make([]AttemptOutcome, len(verdicts))
	for i, v := range verdicts {
		out[i] = AttemptOutcome{Execution: Executed, Verdict: v}
	}
	return out
}

func TestAggregate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      ExperimentKind
		attempts  []AttemptOutcome
		verdict   string
		stability Stability
	}{
		{"all killed", KindMutation, executed(vK, vK, vK), vK, Consistent},
		{"all survived", KindMutation, executed(vS, vS, vS), vS, Consistent},
		{"all invalid", KindMutation, executed(vI), vI, Consistent},
		{"killed survived killed", KindMutation, executed(vK, vS, vK), "", Mixed},
		{"inconclusive in the middle", KindMutation, executed(vK, vX, vK), "", Incomplete},
		{"incomplete wins over mixed", KindMutation, executed(vK, vS, vX), "", Incomplete},
		{"not started", KindMutation, append(executed(vK), AttemptOutcome{Execution: NotStarted}), "", Incomplete},
		{"canceled with a verdict", KindMutation, []AttemptOutcome{{Execution: Canceled, Verdict: vK}}, "", Incomplete},
		{"refused", KindGuardrail, []AttemptOutcome{{Execution: Refused}}, "", Incomplete},
		{"no attempts", KindMutation, nil, "", NotEvaluated},
		{"wrong vocabulary", KindMutation, executed(string(Proven)), "", Incomplete},
		{"proven", KindGuardrail, executed(string(Proven), string(Proven)), string(Proven), Consistent},
		{"proven unproven", KindGuardrail, executed(string(Proven), string(Unproven)), "", Mixed},
		{"killed under guardrail", KindGuardrail, executed(vK), "", Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, s, reason := Aggregate(tc.kind, tc.attempts)
			if v != tc.verdict || s != tc.stability {
				t.Fatalf("got %q %s (%s), want %q %s", v, s, reason, tc.verdict, tc.stability)
			}
			if (s == Consistent) != (reason == "") {
				t.Fatalf("reason %q for %s", reason, s)
			}
		})
	}
}

// TestAggregateNeverVotes checks every combination of three attempts: a verdict
// is returned only when all three were executed with that same verdict.
func TestAggregateNeverVotes(t *testing.T) {
	executions := []Execution{Executed, Canceled, Refused, NotStarted}
	verdicts := []string{vK, vS, vI, vX, ""}
	var options []AttemptOutcome
	for _, e := range executions {
		for _, v := range verdicts {
			options = append(options, AttemptOutcome{e, v})
		}
	}
	for _, a := range options {
		for _, b := range options {
			for _, c := range options {
				attempts := []AttemptOutcome{a, b, c}
				v, s, _ := Aggregate(KindMutation, attempts)
				unanimous := true
				for _, x := range attempts {
					unanimous = unanimous && x.Execution == Executed && x.Verdict == a.Verdict && conclusive(KindMutation, x.Verdict)
				}
				if (v != "") != unanimous || (v != "" && v != a.Verdict) || (s == Consistent) != unanimous {
					t.Fatalf("%v: got %q %s", attempts, v, s)
				}
			}
		}
	}
}

func outcome(id string, kind ExperimentKind, attempts ...AttemptOutcome) ExperimentOutcome {
	v, s, _ := Aggregate(kind, attempts)
	ran := 0
	for _, a := range attempts {
		if a.Execution == Executed || a.Execution == Canceled {
			ran++
		}
	}
	return ExperimentOutcome{ID: id, Kind: kind, Selection: Selected, Stability: s, Verdict: v, Ran: ran}
}

func filtered(id string, kind ExperimentKind) ExperimentOutcome {
	return ExperimentOutcome{ID: id, Kind: kind, Selection: FilteredOut, Stability: NotEvaluated}
}

func TestMutationSummary(t *testing.T) {
	xs := []ExperimentOutcome{
		outcome("k", KindMutation, executed(vK, vK, vK)...), // repeat 3 counts once
		outcome("s", KindMutation, executed(vS)...),
		outcome("i", KindMutation, executed(vI)...),
		outcome("m", KindMutation, executed(vK, vS)...),
		outcome("x", KindMutation, executed(vX)...),
		outcome("n", KindMutation, AttemptOutcome{Execution: NotStarted}),
		filtered("f", KindMutation),
		outcome("g", KindGuardrail, executed(string(Proven))...),
	}
	s := SummarizeMutations(xs)
	want := MutationSummary{Killed: 1, Survived: 1, Invalid: 1, Inconclusive: 1, Mixed: 1, NotExecuted: 1, Filtered: 1, Eligible: 2, Excluded: 4}
	if s.Score == nil || *s.Score != 0.5 {
		t.Fatalf("score %v, want 0.5", s.Score)
	}
	s.Score = nil
	if s != want {
		t.Fatalf("got %+v, want %+v", s, want)
	}
	if s := SummarizeMutations([]ExperimentOutcome{outcome("i", KindMutation, executed(vI)...)}); s.Score != nil || s.Eligible != 0 || s.Excluded != 1 {
		t.Fatalf("only invalid: %+v", s)
	}
	if s := SummarizeMutations(nil); s.Score != nil {
		t.Fatal("empty campaign has a score")
	}
	g := SummarizeGuardrails(append(xs,
		outcome("u", KindGuardrail, executed(string(Unproven))...),
		outcome("gm", KindGuardrail, executed(string(Proven), string(Unproven))...),
		outcome("gx", KindGuardrail, executed(string(Inconclusive))...),
		filtered("gf", KindGuardrail)))
	if g != (GuardrailSummary{Proven: 1, Unproven: 1, Mixed: 1, Inconclusive: 1, Filtered: 1}) {
		t.Fatalf("guardrails: %+v", g)
	}
}

func TestAcceptance(t *testing.T) {
	half, full := 0.5, 1.0
	k := outcome("k", KindMutation, executed(vK)...)
	s := outcome("s", KindMutation, executed(vS)...)
	i := outcome("i", KindMutation, executed(vI)...)
	m := outcome("m", KindMutation, executed(vK, vS)...)
	x := outcome("x", KindMutation, executed(vX)...)
	p := outcome("p", KindGuardrail, executed(string(Proven))...)
	u := outcome("u", KindGuardrail, executed(string(Unproven))...)
	gx := outcome("gx", KindGuardrail, executed(string(Inconclusive))...)
	all4 := AcceptancePolicy{RequireNoSurvivors: true, RequireAllGuardrailsProven: true, RequireNoInconclusive: true, RequireNoMixed: true}

	type req struct {
		name, status string
		ids          []string
	}
	for _, tc := range []struct {
		name     string
		policy   AcceptancePolicy
		xs       []ExperimentOutcome
		complete bool
		decision Decision
		reqs     []req
	}{
		{"all satisfied", all4, []ExperimentOutcome{k, p}, true, Accept, []req{
			{ReqNoSurvivors, RequirementSatisfied, nil}, {ReqAllGuardrailsProven, RequirementSatisfied, nil},
			{ReqNoInconclusive, RequirementSatisfied, nil}, {ReqNoMixed, RequirementSatisfied, nil}}},
		{"survivor only", all4, []ExperimentOutcome{k, s, p}, true, Reject, []req{
			{ReqNoSurvivors, RequirementViolated, []string{"s"}}, {ReqAllGuardrailsProven, RequirementSatisfied, nil},
			{ReqNoInconclusive, RequirementSatisfied, nil}, {ReqNoMixed, RequirementSatisfied, nil}}},
		{"unproven", all4, []ExperimentOutcome{k, u}, true, Reject, []req{
			{ReqNoSurvivors, RequirementSatisfied, nil}, {ReqAllGuardrailsProven, RequirementViolated, []string{"u"}},
			{ReqNoInconclusive, RequirementSatisfied, nil}, {ReqNoMixed, RequirementSatisfied, nil}}},
		{"survivor and inconclusive guard refuses", all4, []ExperimentOutcome{s, gx}, true, RefuseDecision, []req{
			{ReqNoSurvivors, RequirementViolated, []string{"s"}}, {ReqAllGuardrailsProven, RequirementUnsupported, []string{"gx"}},
			{ReqNoInconclusive, RequirementUnsupported, []string{"gx"}}, {ReqNoMixed, RequirementSatisfied, nil}}},
		{"incomplete campaign with a survivor", all4, []ExperimentOutcome{s, p}, false, RefuseDecision, nil},
		{"mixed refuses", all4, []ExperimentOutcome{m, p}, true, RefuseDecision, []req{
			{ReqNoSurvivors, RequirementUnsupported, []string{"m"}}, {ReqAllGuardrailsProven, RequirementSatisfied, nil},
			{ReqNoInconclusive, RequirementSatisfied, nil}, {ReqNoMixed, RequirementUnsupported, []string{"m"}}}},
		{"no survivors cannot rest on an inconclusive mutant", AcceptancePolicy{RequireNoSurvivors: true}, []ExperimentOutcome{k, x}, true, RefuseDecision, []req{
			{ReqNoSurvivors, RequirementUnsupported, []string{"x"}}}},
		{"filtered to mutations", all4, []ExperimentOutcome{k, filtered("p", KindGuardrail)}, true, Accept, []req{
			{ReqNoSurvivors, RequirementSatisfied, nil}, {ReqAllGuardrailsProven, RequirementNotApplicable, nil},
			{ReqNoInconclusive, RequirementSatisfied, nil}, {ReqNoMixed, RequirementSatisfied, nil}}},
		{"everything not applicable", AcceptancePolicy{RequireAllGuardrailsProven: true, RequireNoSurvivors: true}, []ExperimentOutcome{filtered("k", KindMutation), filtered("p", KindGuardrail)}, true, RefuseDecision, []req{
			{ReqNoSurvivors, RequirementNotApplicable, nil}, {ReqAllGuardrailsProven, RequirementNotApplicable, nil}}},
		{"no requirement enabled", AcceptancePolicy{}, []ExperimentOutcome{k}, true, RefuseDecision, nil},
		{"invalid rejected", AcceptancePolicy{RequireNoInvalid: true}, []ExperimentOutcome{k, i}, true, Reject, []req{
			{ReqNoInvalid, RequirementViolated, []string{"i"}}}},
		{"invalid allowed", AcceptancePolicy{RequireNoSurvivors: true}, []ExperimentOutcome{k, i}, true, Accept, []req{
			{ReqNoSurvivors, RequirementSatisfied, nil}}},
		{"score at threshold", AcceptancePolicy{MinMutationScore: &half}, []ExperimentOutcome{k, s}, true, Accept, []req{
			{ReqMinMutationScore, RequirementSatisfied, nil}}},
		{"score below threshold", AcceptancePolicy{MinMutationScore: &full}, []ExperimentOutcome{k, s}, true, Reject, []req{
			{ReqMinMutationScore, RequirementViolated, []string{"s"}}}},
		{"score without eligible mutants", AcceptancePolicy{MinMutationScore: &half}, []ExperimentOutcome{i}, true, RefuseDecision, []req{
			{ReqMinMutationScore, RequirementUnsupported, nil}}},
		{"score without mutations", AcceptancePolicy{MinMutationScore: &half, RequireAllGuardrailsProven: true}, []ExperimentOutcome{p}, true, Accept, []req{
			{ReqAllGuardrailsProven, RequirementSatisfied, nil}, {ReqMinMutationScore, RequirementNotApplicable, nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eval, d := EvaluateAcceptance(tc.policy, tc.xs, tc.complete)
			if d != tc.decision || eval.Complete != tc.complete {
				t.Fatalf("decision %s complete %v, want %s: %+v", d, eval.Complete, tc.decision, eval)
			}
			if tc.reqs == nil && tc.name != "no requirement enabled" {
				return
			}
			if len(eval.Requirements) != len(tc.reqs) {
				t.Fatalf("requirements %+v, want %+v", eval.Requirements, tc.reqs)
			}
			for n, r := range eval.Requirements {
				want := tc.reqs[n]
				if want.ids == nil {
					want.ids = []string{}
				}
				if r.Requirement != want.name || r.Status != want.status || !slices.Equal(r.Experiments, want.ids) || r.Experiments == nil {
					t.Fatalf("requirement %d: %+v, want %+v", n, r, want)
				}
			}
		})
	}
}
