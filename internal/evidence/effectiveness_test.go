package evidence

import (
	"errors"
	"fmt"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

// attempt builds an executed (or not-started) attempt with a proof by guard.
func attempt(id string, n int, verdict, reason, guard string) CampaignAttempt {
	a := CampaignAttempt{ID: AttemptID(id, n), Index: n, Execution: domain.Executed, Reason: reason, ReasonCategory: domain.ReasonCategory(reason)}
	if verdict == "" {
		a.Execution = domain.NotStarted
		return a
	}
	v := verdict
	a.Verdict, a.Proof = &v, &domain.Proof{ID: id, GuardID: guard, Status: domain.ProofStatus(verdict)}
	return a
}

func experiment(id, harness string, kind domain.ExperimentKind, attempts ...CampaignAttempt) CampaignExperiment {
	x := CampaignExperiment{ID: id, Kind: kind, Harness: harness, Selection: domain.Selected, Repeat: max(1, len(attempts)), Attempts: attempts}
	if len(attempts) == 0 {
		x.Selection = domain.FilteredOut
	}
	return x
}

func effectivenessFixture() (CampaignReport, map[string]Membership) {
	r := CampaignReport{Experiments: []CampaignExperiment{
		experiment("p", "a.json", domain.KindGuardrail, attempt("p", 1, "PROVEN", domain.ReasonGuardDetected, "g"), attempt("p", 2, "PROVEN", domain.ReasonGuardDetected, "g")),
		experiment("u", "a.json", domain.KindGuardrail, attempt("u", 1, "UNPROVEN", domain.ReasonGuardPassed, "g")),
		experiment("t", "a.json", domain.KindGuardrail, attempt("t", 1, "INCONCLUSIVE", domain.ReasonTimeout, "g"), attempt("t", 2, "INCONCLUSIVE", domain.ReasonTimeout, "g")),
		experiment("f", "a.json", domain.KindGuardrail, attempt("f", 1, "PROVEN", domain.ReasonGuardDetected, "g"), attempt("f", 2, "INCONCLUSIVE", domain.ReasonCanceled, "g")),
		experiment("m", "b.json", domain.KindGuardrail, attempt("m", 1, "PROVEN", domain.ReasonGuardDetected, "g"), attempt("m", 2, "UNPROVEN", domain.ReasonGuardPassed, "g")),
		experiment("filtered", "b.json", domain.KindGuardrail),
		experiment("never", "b.json", domain.KindGuardrail, attempt("never", 1, "", domain.ReasonPrepareFailed, "")),
		experiment("behavior", "m.json", domain.KindMutation, CampaignAttempt{ID: "behavior#1", Index: 1, Execution: domain.Executed}),
	}}
	r.MutationMetadata = []MutationMetadata{{EvidenceID: "p#1", Operator: "trailing-whitespace", OperatorVersion: "1"}, {EvidenceID: "p#2", Operator: "trailing-whitespace", OperatorVersion: "1"}}
	members := map[string]Membership{
		"p": {GuardRowID("a.json", "g"), "trailing-whitespace", "1"},
		"u": {GuardRowID("a.json", "g"), ReplaceOperator, ""}, "t": {GuardRowID("a.json", "g"), ReplaceOperator, ""},
		"f": {GuardRowID("a.json", "g"), ReplaceOperator, ""}, "m": {GuardRowID("b.json", "g"), ReplaceOperator, ""},
		"filtered": {GuardRowID("b.json", "g"), ReplaceOperator, ""}, "never": {GuardRowID("b.json", "g"), "crlf-injection", "1"},
	}
	e := BuildEffectiveness(r, members)
	r.Effectiveness = &e
	return r, members
}

func rowByID(rows []EffectivenessRow, id string) EffectivenessRow {
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	return EffectivenessRow{}
}

func TestEffectivenessRows(t *testing.T) {
	r, _ := effectivenessFixture()
	e := *r.Effectiveness
	a := rowByID(e.ByGuard, "a.json#g")
	// p proven (two repeats count once), u unproven, t and f inconclusive.
	if a.Planned != 4 || a.Executed != 4 || a.Proven != 1 || a.Unproven != 1 || a.Inconclusive != 2 || a.Eligible != 2 || a.Excluded != 2 || a.ProofRate == nil || *a.ProofRate != 0.5 {
		t.Fatalf("a.json#g: %+v", a)
	}
	// Reasons count attempts: two timeouts in one experiment, one canceled.
	if fmt.Sprint(a.Reasons) != "[{canceled 1} {timeout 2}]" {
		t.Fatalf("reasons %v", a.Reasons)
	}
	b := rowByID(e.ByGuard, "b.json#g")
	// The same guard ID in another harness is another row; mixed stays mixed;
	// filtered is not excluded; nothing eligible gives a null rate.
	if b.Planned != 3 || b.Mixed != 1 || b.Filtered != 1 || b.NotExecuted != 1 || b.Excluded != 2 || b.Eligible != 0 || b.ProofRate != nil || b.Executed != 1 {
		t.Fatalf("b.json#g: %+v", b)
	}
	if fmt.Sprint(b.Reasons) != "[{workspace_prepare_failed 1}]" {
		t.Fatalf("reasons %v", b.Reasons)
	}
	replace := rowByID(e.ByOperator, ReplaceOperator)
	if replace.OperatorVersion != "" || replace.Planned != 5 || len(e.ByOperator) != 3 {
		t.Fatalf("operators %+v", e.ByOperator)
	}
	for _, rows := range [][]EffectivenessRow{e.ByGuard, e.ByOperator} {
		total := 0
		for _, row := range rows {
			sum := row.Proven + row.Unproven + row.Inconclusive + row.Mixed + row.NotExecuted + row.Filtered
			if sum != row.Planned || row.Eligible != row.Proven+row.Unproven || row.Excluded != row.Inconclusive+row.Mixed+row.NotExecuted {
				t.Fatalf("buckets not conserved: %+v", row)
			}
			total += row.Planned
			for _, id := range row.Experiments {
				if id == "behavior" {
					t.Fatal("behavioral experiment in a guardrail row")
				}
			}
		}
		g := domain.SummarizeGuardrails(r.Outcomes())
		if total != g.Proven+g.Unproven+g.Inconclusive+g.Mixed+g.NotExecuted+g.Filtered {
			t.Fatalf("rows cover %d of the guardrail experiments", total)
		}
	}
	var problems []error
	validateEffectiveness(r, func(f string, a ...any) { problems = append(problems, fmt.Errorf(f, a...)) })
	if err := errors.Join(problems...); err != nil {
		t.Fatalf("valid rows rejected: %v", err)
	}
}

func TestEffectivenessForgeriesRejected(t *testing.T) {
	half, one := 0.5, 1.0
	for name, forge := range map[string]func(e *Effectiveness){
		"inflated proven":       func(e *Effectiveness) { e.ByGuard[0].Proven++ },
		"perfect rate":          func(e *Effectiveness) { e.ByGuard[0].ProofRate = &one },
		"rate for nothing":      func(e *Effectiveness) { e.ByGuard[1].ProofRate = &half },
		"filtered as excluded":  func(e *Effectiveness) { e.ByGuard[1].Excluded++; e.ByGuard[1].Filtered-- },
		"reason per experiment": func(e *Effectiveness) { e.ByGuard[0].Reasons[1].Count = 1 },
		"behavioral row member": func(e *Effectiveness) { e.ByGuard[0].Experiments = append(e.ByGuard[0].Experiments, "behavior") },
		"experiment dropped":    func(e *Effectiveness) { e.ByGuard[1].Experiments = e.ByGuard[1].Experiments[1:] },
		"experiment twice":      func(e *Effectiveness) { e.ByGuard[1].Experiments = append(e.ByGuard[1].Experiments, "p") },
		"wrong harness":         func(e *Effectiveness) { e.ByGuard[0].ID = "b.json#g" },
		"wrong operator":        func(e *Effectiveness) { e.ByOperator[len(e.ByOperator)-1].ID = "crlf-injection" },
		"unsorted":              func(e *Effectiveness) { e.ByGuard[0], e.ByGuard[1] = e.ByGuard[1], e.ByGuard[0] },
		"version on replace":    func(e *Effectiveness) { e.ByOperator[1].OperatorVersion = "1" },
		"missing":               func(e *Effectiveness) { e.ByOperator = nil },
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := effectivenessFixture()
			e := *r.Effectiveness
			e.ByGuard = cloneRows(e.ByGuard)
			e.ByOperator = cloneRows(e.ByOperator)
			forge(&e)
			r.Effectiveness = &e
			var problems []error
			validateEffectiveness(r, func(f string, a ...any) { problems = append(problems, fmt.Errorf(f, a...)) })
			if len(problems) == 0 {
				t.Fatal("forged effectiveness accepted")
			}
		})
	}
}

func cloneRows(rows []EffectivenessRow) []EffectivenessRow {
	out := make([]EffectivenessRow, len(rows))
	for i, r := range rows {
		r.Experiments = append([]string{}, r.Experiments...)
		r.Reasons = append([]ReasonCount{}, r.Reasons...)
		out[i] = r
	}
	return out
}
