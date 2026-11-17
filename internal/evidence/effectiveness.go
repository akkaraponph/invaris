package evidence

import (
	"reflect"
	"slices"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

// ReplaceOperator is the operator row of literal before/after challenges. It
// has no operator version: none was ever promised for it.
const ReplaceOperator = "replace"

// Effectiveness summarizes guardrail experiments per guard and per operator
// (ProfileV5 campaigns). Behavioral mutation experiments are never included.
type Effectiveness struct {
	ByGuard    []EffectivenessRow `json:"byGuard"`
	ByOperator []EffectivenessRow `json:"byOperator"`
}

// EffectivenessRow counts the guardrail experiments of one guard or operator,
// once each, after the per-experiment aggregation. The six terminal buckets
// (proven, unproven, inconclusive, mixed, notExecuted, filtered) sum to
// planned; eligible is proven+unproven and excluded is
// inconclusive+mixed+notExecuted; filtered is separate from both. ProofRate is
// proven/eligible, a fraction, and null when nothing is eligible. Reasons
// count infrastructure reasons (timeout, canceled, ...) per attempt, so they
// can exceed the number of experiments and are not part of any bucket.
type EffectivenessRow struct {
	ID              string        `json:"id"`
	OperatorVersion string        `json:"operatorVersion,omitempty"`
	Experiments     []string      `json:"experiments"`
	Planned         int           `json:"planned"`
	Executed        int           `json:"executed"`
	Proven          int           `json:"proven"`
	Unproven        int           `json:"unproven"`
	Inconclusive    int           `json:"inconclusive"`
	Mixed           int           `json:"mixed"`
	NotExecuted     int           `json:"notExecuted"`
	Filtered        int           `json:"filtered"`
	Eligible        int           `json:"eligible"`
	Excluded        int           `json:"excluded"`
	ProofRate       *float64      `json:"proofRate"`
	Reasons         []ReasonCount `json:"reasons"`
}

// ReasonCount is the number of attempts that ended with one reason.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Membership places one guardrail experiment in its rows. Guard is the
// harness-scoped guard ID "<harness>#<guardId>", so equal guard IDs in two
// harnesses stay apart. Operator is ReplaceOperator for literal challenges.
type Membership struct {
	Guard, Operator, OperatorVersion string
}

// GuardRowID scopes a guard ID with its harness path.
func GuardRowID(harness, guardID string) string { return harness + "#" + guardID }

// BuildEffectiveness groups the report's guardrail experiments by guard and
// by operator. members must cover every guardrail experiment.
func BuildEffectiveness(r CampaignReport, members map[string]Membership) Effectiveness {
	type key struct{ id, version string }
	guards, operators := map[key][]string{}, map[key][]string{}
	for _, x := range r.Experiments {
		m, ok := members[x.ID]
		if x.Kind != domain.KindGuardrail || !ok {
			continue
		}
		guards[key{m.Guard, ""}] = append(guards[key{m.Guard, ""}], x.ID)
		operators[key{m.Operator, m.OperatorVersion}] = append(operators[key{m.Operator, m.OperatorVersion}], x.ID)
	}
	rows := func(groups map[key][]string) []EffectivenessRow {
		out := []EffectivenessRow{}
		for k, ids := range groups {
			out = append(out, effectivenessRow(r, k.id, k.version, ids))
		}
		slices.SortFunc(out, func(a, b EffectivenessRow) int {
			if c := strings.Compare(a.ID, b.ID); c != 0 {
				return c
			}
			return strings.Compare(a.OperatorVersion, b.OperatorVersion)
		})
		return out
	}
	return Effectiveness{ByGuard: rows(guards), ByOperator: rows(operators)}
}

// effectivenessRow derives one row from the named experiments using the
// report's own aggregation, exactly as the global guardrail summary does.
func effectivenessRow(r CampaignReport, id, version string, experiments []string) EffectivenessRow {
	in := map[string]bool{}
	for _, e := range experiments {
		in[e] = true
	}
	var outcomes []domain.ExperimentOutcome
	reasons := map[string]int{}
	row := EffectivenessRow{ID: id, OperatorVersion: version, Reasons: []ReasonCount{}}
	all := r.Outcomes()
	for i, x := range r.Experiments {
		if !in[x.ID] {
			continue
		}
		row.Experiments = append(row.Experiments, x.ID)
		o := all[i]
		outcomes = append(outcomes, o)
		if o.Selection == domain.Selected && o.Ran > 0 {
			row.Executed++
		}
		for _, a := range x.Attempts {
			if domain.ReasonCategory(a.Reason) == domain.CategoryInfrastructure {
				reasons[a.Reason]++
			}
		}
	}
	s := domain.SummarizeGuardrails(outcomes)
	row.Proven, row.Unproven, row.Inconclusive, row.Mixed = s.Proven, s.Unproven, s.Inconclusive, s.Mixed
	row.NotExecuted, row.Filtered, row.Eligible, row.Excluded, row.ProofRate = s.NotExecuted, s.Filtered, s.Eligible, s.Excluded, s.ProofRate
	row.Planned = s.Proven + s.Unproven + s.Inconclusive + s.Mixed + s.NotExecuted + s.Filtered
	for reason, n := range reasons {
		row.Reasons = append(row.Reasons, ReasonCount{Reason: reason, Count: n})
	}
	slices.SortFunc(row.Reasons, func(a, b ReasonCount) int { return strings.Compare(a.Reason, b.Reason) })
	if row.Experiments == nil {
		row.Experiments = []string{}
	}
	return row
}

func normalizeEffectiveness(e Effectiveness) Effectiveness {
	norm := func(rows []EffectivenessRow) []EffectivenessRow {
		out := make([]EffectivenessRow, len(rows))
		for i, r := range rows {
			if r.Experiments == nil {
				r.Experiments = []string{}
			}
			if r.Reasons == nil {
				r.Reasons = []ReasonCount{}
			}
			out[i] = r
		}
		return out
	}
	e.ByGuard, e.ByOperator = norm(e.ByGuard), norm(e.ByOperator)
	return e
}

// validateEffectiveness recomputes every row from the experiments it lists
// and checks that each guardrail experiment is in exactly one row of each
// list, that no behavioral experiment is, and that membership agrees with
// what the report shows: the harness and, once an attempt started, the guard
// and operator of its evidence.
func validateEffectiveness(r CampaignReport, bad func(string, ...any)) {
	if r.Effectiveness == nil {
		bad("a %s campaign requires effectiveness", ProfileV5)
		return
	}
	e := normalizeEffectiveness(*r.Effectiveness)
	experiments := map[string]CampaignExperiment{}
	for _, x := range r.Experiments {
		experiments[x.ID] = x
	}
	operatorOf := map[string]MutationMetadata{} // experiment ID -> any entry of its attempts
	for _, m := range r.MutationMetadata {
		if i := strings.LastIndex(m.EvidenceID, "#"); i > 0 {
			operatorOf[m.EvidenceID[:i]] = m
		}
	}
	check := func(list string, rows []EffectivenessRow, member func(EffectivenessRow, CampaignExperiment) bool) {
		seen := map[string]bool{}
		for i, row := range rows {
			if i > 0 {
				prev := rows[i-1]
				if prev.ID > row.ID || prev.ID == row.ID && prev.OperatorVersion >= row.OperatorVersion {
					bad("%s rows are not sorted or repeat %q", list, row.ID)
				}
			}
			if len(row.Experiments) == 0 {
				bad("%s row %q lists no experiments", list, row.ID)
			}
			for _, id := range row.Experiments {
				x, ok := experiments[id]
				if !ok || seen[id] || x.Kind != domain.KindGuardrail {
					bad("%s row %q lists %q, which is unknown, repeated or not a guardrail experiment", list, row.ID, id)
					continue
				}
				seen[id] = true
				if !member(row, x) {
					bad("%s row %q does not match experiment %q", list, row.ID, id)
				}
			}
			want := effectivenessRow(r, row.ID, row.OperatorVersion, row.Experiments)
			if !reflect.DeepEqual(row, want) {
				bad("%s row %q does not match its experiments", list, row.ID)
			}
		}
		for _, x := range r.Experiments {
			if x.Kind == domain.KindGuardrail && !seen[x.ID] {
				bad("guardrail experiment %q is missing from %s", x.ID, list)
			}
		}
	}
	check("byGuard", e.ByGuard, func(row EffectivenessRow, x CampaignExperiment) bool {
		guard, ok := strings.CutPrefix(row.ID, x.Harness+"#")
		if !ok || guard == "" || row.OperatorVersion != "" {
			return false
		}
		for _, a := range x.Attempts {
			if a.Proof != nil && a.Proof.GuardID != guard {
				return false
			}
		}
		return true
	})
	check("byOperator", e.ByOperator, func(row EffectivenessRow, x CampaignExperiment) bool {
		if m, ok := operatorOf[x.ID]; ok {
			return row.ID == m.Operator && row.OperatorVersion == m.OperatorVersion
		}
		if row.ID == ReplaceOperator {
			return row.OperatorVersion == ""
		}
		// A built-in experiment whose attempts never started has no metadata;
		// one that started always has.
		for _, a := range x.Attempts {
			if a.Proof != nil {
				return false
			}
		}
		return row.OperatorVersion != ""
	})
}
