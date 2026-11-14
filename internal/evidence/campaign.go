package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// CampaignSchemaVersion is the report emitted by the campaign action.
const CampaignSchemaVersion = "invaris.evidence/v1alpha4"

// Campaign stage names. The snapshot is the frozen copy every attempt is
// derived from; cleanup removes it.
const (
	StageSnapshot = "snapshot"
	StageCleanup  = "cleanup"
)

// ExcludedFromSnapshot lists the directory names the workspace copier skips at
// every level. They are not part of the source identity.
var ExcludedFromSnapshot = []string{".git", ".invaris"}

// SourceIdentity identifies what the campaign evaluated: the manifest digest
// of the frozen snapshot (paths, entry kinds, modes and file SHA-256 values),
// plus the Git state of the source when it could be determined. A digest
// identifies content; it is not a signature or an attestation.
type SourceIdentity struct {
	SnapshotSHA256 *string      `json:"snapshotSha256"`
	Files          *int         `json:"files"`
	Roots          []SourceRoot `json:"roots"`
	GitRevision    *string      `json:"gitRevision"`
	GitDirty       *bool        `json:"gitDirty"`
	Excluded       []string     `json:"excluded"`
}

// SourceRoot is the manifest digest of one directory of the snapshot that an
// experiment evaluates: the directory of its harness. Path "." is the whole
// snapshot. Each attempt's copy of that directory must have this digest.
type SourceRoot struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Files  int    `json:"files"`
}

// CampaignSelection records the scope of this run. Mode is "all", or
// "filtered" when --only reduced it to the listed experiments.
type CampaignSelection struct {
	Mode     string   `json:"mode"`
	Only     []string `json:"only"`
	Selected int      `json:"selected"`
	Filtered int      `json:"filtered"`
}

// CampaignAttempt is one independent attempt of one experiment. Mutation or
// Proof holds the child evidence of the experiment's kind when the lifecycle
// started; it is null for attempts that never started.
type CampaignAttempt struct {
	ID             string              `json:"id"`
	Index          int                 `json:"index"`
	Execution      domain.Execution    `json:"execution"`
	Verdict        *string             `json:"verdict"`
	Reason         string              `json:"reason"`
	ReasonCategory string              `json:"reasonCategory"`
	Detail         string              `json:"detail"`
	StartedAt      *time.Time          `json:"startedAt"`
	FinishedAt     *time.Time          `json:"finishedAt"`
	DurationMs     int64               `json:"durationMs"`
	Mutation       *MutationExperiment `json:"mutation"`
	Proof          *domain.Proof       `json:"proof"`
	Observations   []Observation       `json:"observations"`
}

// CampaignExperiment is one declared experiment with every attempt, in order.
type CampaignExperiment struct {
	ID               string                `json:"id"`
	Kind             domain.ExperimentKind `json:"kind"`
	Harness          string                `json:"harness"`
	Root             string                `json:"root"`
	HarnessDigest    string                `json:"harnessDigest"`
	ReferenceID      string                `json:"referenceId"`
	Repeat           int                   `json:"repeat"`
	Selection        domain.Selection      `json:"selection"`
	Attempts         []CampaignAttempt     `json:"attempts"`
	AggregateVerdict *string               `json:"aggregateVerdict"`
	Stability        domain.Stability      `json:"stability"`
	StabilityReason  string                `json:"stabilityReason"`
}

// CampaignReport is invaris.evidence/v1alpha4.
type CampaignReport struct {
	SchemaVersion        string                      `json:"schemaVersion"`
	RunID                string                      `json:"runId"`
	Action               string                      `json:"action"`
	Campaign             string                      `json:"campaign"`
	CampaignConfigDigest string                      `json:"campaignConfigDigest"`
	EngineVersion        *string                     `json:"engineVersion"`
	SourceIdentity       SourceIdentity              `json:"sourceIdentity"`
	Environment          Environment                 `json:"environment"`
	Selection            CampaignSelection           `json:"selection"`
	Parallelism          int                         `json:"parallelism"`
	StartedAt            time.Time                   `json:"startedAt"`
	FinishedAt           time.Time                   `json:"finishedAt"`
	DurationMs           int64                       `json:"durationMs"`
	Stages               []domain.Stage              `json:"stages"`
	Tools                []Observation               `json:"tools"`
	Experiments          []CampaignExperiment        `json:"experiments"`
	MutationSummary      domain.MutationSummary      `json:"mutationSummary"`
	GuardrailSummary     domain.GuardrailSummary     `json:"guardrailSummary"`
	AcceptancePolicy     domain.AcceptancePolicy     `json:"acceptancePolicy"`
	AcceptanceEvaluation domain.AcceptanceEvaluation `json:"acceptanceEvaluation"`
	Decision             domain.Decision             `json:"decision"`
	Diagnostics          []domain.Diagnostic         `json:"diagnostics"`
	Artifacts            []Artifact                  `json:"artifacts"`
}

// AttemptID is the stable ID of attempt index (1-based) of an experiment.
func AttemptID(experiment string, index int) string {
	return fmt.Sprintf("%s#%d", experiment, index)
}

// Outcomes derives the aggregation input from the report's own attempts.
func (r CampaignReport) Outcomes() []domain.ExperimentOutcome {
	out := make([]domain.ExperimentOutcome, len(r.Experiments))
	for i, x := range r.Experiments {
		verdict, stability, _ := domain.Aggregate(x.Kind, attemptOutcomes(x.Attempts))
		ran := 0
		for _, a := range x.Attempts {
			if a.Execution == domain.Executed || a.Execution == domain.Canceled {
				ran++
			}
		}
		out[i] = domain.ExperimentOutcome{ID: x.ID, Kind: x.Kind, Selection: x.Selection, Stability: stability, Verdict: verdict, Ran: ran}
	}
	return out
}

func attemptOutcomes(attempts []CampaignAttempt) []domain.AttemptOutcome {
	out := make([]domain.AttemptOutcome, len(attempts))
	for i, a := range attempts {
		out[i].Execution = a.Execution
		if a.Verdict != nil {
			out[i].Verdict = *a.Verdict
		}
	}
	return out
}

// Complete reports whether the campaign ran to completion: the snapshot was
// prepared and removed, and every selected attempt executed.
func (r CampaignReport) Complete() bool {
	if len(r.Stages) != 2 || r.Stages[0].Name != StageSnapshot || r.Stages[0].Status != domain.StageOK ||
		r.Stages[1].Name != StageCleanup || r.Stages[1].Status != domain.StageOK {
		return false
	}
	for _, x := range r.Experiments {
		if x.Selection != domain.Selected {
			continue
		}
		for _, a := range x.Attempts {
			if a.Execution != domain.Executed {
				return false
			}
		}
	}
	return true
}

// ValidateCampaign checks the structure of a campaign report and recomputes
// every derived value from its attempts: per-experiment aggregates, both
// summaries, completeness, the acceptance evaluation and the decision. A report
// whose recorded values differ from what its attempts support is invalid. Each
// attempt's child evidence is held to the same rules as a single report.
func ValidateCampaign(r CampaignReport) error {
	var problems []error
	bad := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }
	if r.SchemaVersion != CampaignSchemaVersion {
		bad("schemaVersion must be %q", CampaignSchemaVersion)
	}
	if r.Action != "campaign" {
		bad("action must be campaign")
	}
	if !runIDPattern.MatchString(r.RunID) {
		bad("runId must be 128-bit lowercase hex")
	}
	if strings.TrimSpace(r.Campaign) == "" {
		bad("campaign name is required")
	}
	if !digestPattern.MatchString(r.CampaignConfigDigest) {
		bad("campaignConfigDigest must be sha256:<64 hex>")
	}
	if r.EngineVersion != nil && *r.EngineVersion == "" {
		bad("engineVersion must be null or nonempty")
	}
	if r.Environment.OS == "" || r.Environment.Arch == "" {
		bad("environment requires os and arch")
	}
	if r.FinishedAt.Before(r.StartedAt) || r.DurationMs < 0 {
		bad("finishedAt precedes startedAt or duration is negative")
	}
	roots := validateSourceIdentity(r.SourceIdentity, bad)
	if r.Parallelism < 1 || r.Parallelism > 8 {
		bad("parallelism must be 1 to 8")
	}
	snapshotOK := validateCampaignStages(r, bad)
	if snapshotOK && len(roots) == 0 {
		bad("a prepared snapshot requires root digests")
	}
	validateCampaignPolicy(r.AcceptancePolicy, bad)

	ids, attemptIDs := map[string]bool{}, map[string]bool{}
	selected, filtered := 0, 0
	for _, x := range r.Experiments {
		if x.ID == "" || ids[x.ID] {
			bad("experiment ID %q is empty or duplicated", x.ID)
		}
		ids[x.ID] = true
		if x.Kind != domain.KindMutation && x.Kind != domain.KindGuardrail {
			bad("experiment %q has unknown kind %q", x.ID, x.Kind)
		}
		if !workspace.ValidPath(x.Harness) || x.Root != path.Dir(x.Harness) || !digestPattern.MatchString(x.HarnessDigest) || x.ReferenceID == "" {
			bad("experiment %q has an invalid harness, root, digest or reference", x.ID)
		}
		if _, ok := roots[x.Root]; !ok && len(roots) > 0 {
			bad("experiment %q root %q has no recorded digest", x.ID, x.Root)
		}
		if x.Repeat < 1 || x.Repeat > 5 {
			bad("experiment %q has repeat %d outside 1..5", x.ID, x.Repeat)
		}
		switch x.Selection {
		case domain.Selected:
			selected++
			if len(x.Attempts) != x.Repeat {
				bad("experiment %q has %d attempts for repeat %d", x.ID, len(x.Attempts), x.Repeat)
			}
		case domain.FilteredOut:
			filtered++
			if len(x.Attempts) != 0 {
				bad("filtered experiment %q has attempts", x.ID)
			}
		default:
			bad("experiment %q has unknown selection %q", x.ID, x.Selection)
		}
		for i, a := range x.Attempts {
			if a.ID != AttemptID(x.ID, i+1) || a.Index != i+1 || attemptIDs[a.ID] {
				bad("attempt %q of %q is out of order or duplicated", a.ID, x.ID)
			}
			attemptIDs[a.ID] = true
			if !snapshotOK && a.Execution != domain.NotStarted {
				bad("attempt %q ran without a campaign snapshot", a.ID)
			}
			var want *string
			if root, ok := roots[x.Root]; ok {
				want = &root
			}
			validateAttempt(x, a, want, bad)
		}
		verdict, stability, reason := domain.Aggregate(x.Kind, attemptOutcomes(x.Attempts))
		if x.Stability != stability || x.StabilityReason != reason || (x.AggregateVerdict == nil) != (verdict == "") ||
			x.AggregateVerdict != nil && *x.AggregateVerdict != verdict {
			bad("experiment %q aggregate disagrees with its attempts", x.ID)
		}
	}
	validateSelection(r, selected, filtered, bad)

	outcomes := r.Outcomes()
	if got, want := r.MutationSummary, domain.SummarizeMutations(outcomes); !equalMutationSummary(got, want) {
		bad("mutation summary %+v does not match the experiments", got)
	}
	if got, want := r.GuardrailSummary, domain.SummarizeGuardrails(outcomes); !equalGuardrailSummary(got, want) {
		bad("guardrail summary %+v does not match the experiments", got)
	}
	eval, decision := domain.EvaluateAcceptance(r.AcceptancePolicy, outcomes, r.Complete())
	if !reflect.DeepEqual(normalizeEvaluation(r.AcceptanceEvaluation), eval) {
		bad("acceptance evaluation does not match the experiments")
	}
	if r.Decision != decision {
		bad("decision %s does not match the %s supported by the experiments", r.Decision, decision)
	}
	toolIDs := map[string]bool{}
	for _, o := range r.Tools {
		if o.Kind != "tool" || !strings.HasPrefix(o.ID, "tool:") || toolIDs[o.ID] || o.Program == "" {
			bad("tool observation %q is malformed or repeated", o.ID)
		}
		toolIDs[o.ID] = true
	}
	if len(r.Tools) > 0 && !snapshotOK {
		bad("tools were probed without a campaign snapshot")
	}
	for _, d := range r.Diagnostics {
		if d.Code == "" {
			bad("diagnostic without a code")
		}
	}
	for _, a := range r.Artifacts {
		if a.Path == "" || !hashPattern.MatchString(a.SHA256) || a.Bytes < 0 {
			bad("artifact %q is malformed", a.Path)
		}
	}
	return errors.Join(problems...)
}

// validateSourceIdentity returns the recorded root digests by path.
func validateSourceIdentity(s SourceIdentity, bad func(string, ...any)) map[string]string {
	if s.SnapshotSHA256 != nil && !hashPattern.MatchString(*s.SnapshotSHA256) {
		bad("snapshotSha256 must be 64 lowercase hex")
	}
	roots := map[string]string{}
	for i, root := range s.Roots {
		if root.Path != "." && !workspace.ValidPath(root.Path) || !hashPattern.MatchString(root.SHA256) || root.Files < 0 ||
			i > 0 && s.Roots[i-1].Path >= root.Path {
			bad("source root %q is invalid, unsorted or repeated", root.Path)
		}
		roots[root.Path] = root.SHA256
	}
	if whole, ok := roots["."]; ok && (s.SnapshotSHA256 == nil || whole != *s.SnapshotSHA256) {
		bad(`source root "." must equal snapshotSha256`)
	}
	if (s.SnapshotSHA256 == nil) != (s.Files == nil) || s.Files != nil && *s.Files < 0 {
		bad("snapshotSha256 and files must be set together")
	}
	if g := s.GitRevision; g != nil && (len(*g) < 40 || len(*g) > 64 || strings.Trim(*g, "0123456789abcdef") != "") {
		bad("gitRevision must be null or lowercase hex")
	}
	if !slices.Equal(s.Excluded, ExcludedFromSnapshot) {
		bad("excluded must be %v", ExcludedFromSnapshot)
	}
	return roots
}

// validateCampaignStages accepts [snapshot] or [snapshot, cleanup]. A
// successful snapshot requires a cleanup stage and a digest. It returns
// whether the snapshot was prepared successfully.
func validateCampaignStages(r CampaignReport, bad func(string, ...any)) bool {
	st := r.Stages
	if len(st) == 0 || len(st) > 2 || st[0].Name != StageSnapshot || len(st) == 2 && st[1].Name != StageCleanup {
		bad("stages must be snapshot, then cleanup")
		return false
	}
	for _, s := range st {
		if s.Status != domain.StageOK && s.Status != domain.StageFailed || s.DurationMs < 0 {
			bad("stage %q has invalid status or duration", s.Name)
		}
	}
	ok := st[0].Status == domain.StageOK
	if ok && (len(st) != 2 || r.SourceIdentity.SnapshotSHA256 == nil) {
		bad("a prepared snapshot requires a digest and a cleanup stage")
	}
	return ok
}

func validateCampaignPolicy(p domain.AcceptancePolicy, bad func(string, ...any)) {
	if !p.RequireNoSurvivors && !p.RequireAllGuardrailsProven && !p.RequireNoInconclusive && !p.RequireNoMixed && !p.RequireNoInvalid && p.MinMutationScore == nil {
		bad("acceptance policy enables no requirement")
	}
	if s := p.MinMutationScore; s != nil && (*s <= 0 || *s > 1) {
		bad("minMutationScore must be in (0, 1]")
	}
}

func validateSelection(r CampaignReport, selected, filtered int, bad func(string, ...any)) {
	s := r.Selection
	if s.Selected != selected || s.Filtered != filtered || selected == 0 {
		bad("selection counts disagree with the experiments")
	}
	switch s.Mode {
	case "all":
		if len(s.Only) != 0 || filtered != 0 {
			bad("selection mode all with a filter")
		}
	case "filtered":
		seen := map[string]bool{}
		for _, id := range s.Only {
			if seen[id] {
				bad("selection repeats %q", id)
			}
			seen[id] = true
		}
		for _, x := range r.Experiments {
			if (x.Selection == domain.Selected) != seen[x.ID] {
				bad("selection only disagrees with experiment %q", x.ID)
			}
		}
		if len(s.Only) != selected {
			bad("selection only names unknown experiments")
		}
	default:
		bad("unknown selection mode %q", s.Mode)
	}
}

func validateAttempt(x CampaignExperiment, a CampaignAttempt, snapshot *string, bad func(string, ...any)) {
	if a.Reason == "" || domain.ReasonCategory(a.Reason) == "" || a.ReasonCategory != domain.ReasonCategory(a.Reason) {
		bad("attempt %q has unknown reason %q or mismatched category %q", a.ID, a.Reason, a.ReasonCategory)
	}
	switch a.Execution {
	case domain.Executed, domain.Canceled:
	case domain.NotStarted, domain.Refused:
		if a.Mutation != nil || a.Proof != nil || a.Verdict != nil || len(a.Observations) != 0 || a.StartedAt != nil || a.FinishedAt != nil {
			bad("attempt %q did not run but carries evidence", a.ID)
		}
		if domain.ReasonCategory(a.Reason) == domain.CategoryResult {
			bad("attempt %q did not run but has a result reason", a.ID)
		}
		return
	default:
		bad("attempt %q has unknown execution %q", a.ID, a.Execution)
		return
	}
	if (a.Execution == domain.Canceled) != (a.Reason == domain.ReasonCanceled) {
		bad("attempt %q: CANCELED and reason canceled must go together", a.ID)
	}
	if a.StartedAt == nil || a.FinishedAt == nil || a.FinishedAt.Before(*a.StartedAt) || a.DurationMs < 0 {
		bad("attempt %q requires its timing", a.ID)
	}
	for _, o := range a.Observations {
		if o.ID == "" || o.Kind == "" {
			bad("attempt %q has an observation without an ID or kind", a.ID)
		}
	}
	var verdict, reason, childSnapshot string
	conclusive := false
	switch {
	case x.Kind == domain.KindMutation && a.Mutation != nil && a.Proof == nil:
		e := a.Mutation
		validateMutationExperiment(e, a.Observations, true, bad)
		if e.ID != x.ReferenceID {
			bad("attempt %q evaluated experiment %q instead of %q", a.ID, e.ID, x.ReferenceID)
		}
		verdict, reason = string(e.Verdict), e.Reason
		conclusive = e.Verdict != domain.MutationInconclusive
		if e.SourceSnapshot != nil {
			childSnapshot = e.SourceSnapshot.SHA256
		}
	case x.Kind == domain.KindGuardrail && a.Proof != nil && a.Mutation == nil:
		p := *a.Proof
		if p.ID != x.ReferenceID {
			bad("attempt %q evaluated challenge %q instead of %q", a.ID, p.ID, x.ReferenceID)
		}
		switch p.Status {
		case domain.Proven, domain.Unproven, domain.Inconclusive:
		default:
			bad("attempt %q proof has status %q", a.ID, p.Status)
		}
		for _, c := range []*domain.Check{p.Baseline, p.Challenged} {
			if c != nil {
				validateCheck(*c, bad)
			}
		}
		validateReason(p, bad)
		validateProofRecord(p, bad)
		if p.Status == domain.Proven {
			validateProvenEvidence(p, bad)
			if p.EvidenceStrength != domain.StrengthStrong {
				bad("attempt %q is PROVEN with weak evidence", a.ID)
			}
		}
		verdict, reason = string(p.Status), p.Reason
		conclusive = p.Status == domain.Proven || p.Status == domain.Unproven
		if p.SourceSnapshot != nil {
			childSnapshot = p.SourceSnapshot.SHA256
		}
	default:
		bad("attempt %q requires the child evidence of a %s experiment", a.ID, x.Kind)
		return
	}
	if a.Verdict == nil || *a.Verdict != verdict || a.Reason != reason {
		bad("attempt %q verdict or reason disagrees with its evidence", a.ID)
	}
	if childSnapshot != "" && (snapshot == nil || childSnapshot != *snapshot) || conclusive && childSnapshot == "" {
		bad("attempt %q did not evaluate the recorded digest of root %q", a.ID, x.Root)
	}
}

func equalMutationSummary(a, b domain.MutationSummary) bool {
	as, bs := a.Score, b.Score
	a.Score, b.Score = nil, nil
	return a == b && equalRate(as, bs)
}

func equalGuardrailSummary(a, b domain.GuardrailSummary) bool {
	ar, br := a.ProofRate, b.ProofRate
	a.ProofRate, b.ProofRate = nil, nil
	return a == b && equalRate(ar, br)
}

func equalRate(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func normalizeEvaluation(e domain.AcceptanceEvaluation) domain.AcceptanceEvaluation {
	if e.Requirements == nil {
		e.Requirements = []domain.RequirementResult{}
	}
	reqs := make([]domain.RequirementResult, len(e.Requirements))
	for i, r := range e.Requirements {
		if r.Experiments == nil {
			r.Experiments = []string{}
		}
		reqs[i] = r
	}
	e.Requirements = reqs
	return e
}

// MarshalCampaign serializes a validated campaign report. Field order is fixed,
// there are no maps, and absent lists become empty arrays, so equal reports
// produce identical bytes. An invalid report is an error, not output.
func MarshalCampaign(r CampaignReport) ([]byte, error) {
	r = normalizeCampaign(r)
	if err := ValidateCampaign(r); err != nil {
		return nil, fmt.Errorf("invalid campaign report: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeCampaign(r CampaignReport) CampaignReport {
	if r.SourceIdentity.Excluded == nil {
		r.SourceIdentity.Excluded = []string{}
	}
	if r.SourceIdentity.Roots == nil {
		r.SourceIdentity.Roots = []SourceRoot{}
	}
	if r.Selection.Only == nil {
		r.Selection.Only = []string{}
	}
	if r.Stages == nil {
		r.Stages = []domain.Stage{}
	}
	r.Tools = normalizeObservations(r.Tools)
	if r.Diagnostics == nil {
		r.Diagnostics = []domain.Diagnostic{}
	}
	if r.Artifacts == nil {
		r.Artifacts = []Artifact{}
	}
	r.AcceptanceEvaluation = normalizeEvaluation(r.AcceptanceEvaluation)
	experiments := make([]CampaignExperiment, len(r.Experiments))
	for i, x := range r.Experiments {
		attempts := make([]CampaignAttempt, len(x.Attempts))
		for j, a := range x.Attempts {
			a.Observations = normalizeObservations(a.Observations)
			if a.Proof != nil {
				p := normalizeProof(*a.Proof)
				a.Proof = &p
			}
			if a.Mutation != nil {
				m := *a.Mutation
				if m.Stages == nil {
					m.Stages = []domain.Stage{}
				}
				if m.TestIDs == nil {
					m.TestIDs = []string{}
				}
				a.Mutation = &m
			}
			attempts[j] = a
		}
		x.Attempts = attempts
		experiments[i] = x
	}
	r.Experiments = experiments
	return r
}

// RedactCampaign redacts every string in the report in place before it is
// serialized. Excerpts of truncated observations also lose a secret prefix cut
// by the size limit. Only configured literal secrets are found.
func RedactCampaign(r *CampaignReport, secrets []string) {
	if len(usable(secrets)) == 0 {
		return
	}
	truncated := func(obs []Observation) {
		for k := range obs {
			if obs[k].Truncated {
				obs[k].Stdout = string(RedactTruncated([]byte(obs[k].Stdout), secrets))
				obs[k].Stderr = string(RedactTruncated([]byte(obs[k].Stderr), secrets))
			}
		}
	}
	truncated(r.Tools)
	for i := range r.Experiments {
		for j := range r.Experiments[i].Attempts {
			truncated(r.Experiments[i].Attempts[j].Observations)
		}
	}
	redactValue(reflect.ValueOf(r).Elem(), secrets)
}
