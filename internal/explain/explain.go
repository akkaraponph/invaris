package explain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// ExplanationVersion is the schema of a serialized Explanation.
const ExplanationVersion = "invaris.explanation/v1alpha1"

// MaxReportBytes bounds the report Explain will read.
const MaxReportBytes = 16 << 20

var (
	// ErrInvalidReport means the input is not valid evidence: it does not
	// decode strictly, fails validation or is too large.
	ErrInvalidReport = errors.New("invalid report")
	// ErrUnsupported means the report's schemaVersion is not one Explain reads.
	ErrUnsupported = errors.New("unsupported report version")
)

// Result classes. They separate what a check or experiment recorded from what
// that means: detected and missed are about a fault, inconclusive is neither.
const (
	ClassPassed       = "passed"
	ClassFailed       = "failed"
	ClassDetected     = "detected"
	ClassMissed       = "missed"
	ClassInconclusive = "inconclusive"
	ClassExcluded     = "excluded"
	ClassNotRun       = "not-run"
)

// Limit codes, in the order they are reported.
const (
	LimitWeakEvidence    = "weak-evidence"
	LimitPortableProfile = "portable-profile"
	LimitLegacyInput     = "legacy-input"
	LimitDirtyWorktree   = "dirty-worktree"
	LimitIncomplete      = "incomplete"
	LimitUnauthenticated = "unauthenticated"
)

// ReportInfo identifies the explained report.
type ReportInfo struct {
	SchemaVersion string `json:"schemaVersion"`
	Action        string `json:"action"`
	RunID         string `json:"runId"`
	Decision      string `json:"decision"`
	Subject       string `json:"subject"`
}

// Item is one step, guard, proof, mutation or campaign experiment.
type Item struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Outcome  string  `json:"outcome"`
	Class    string  `json:"class"`
	Reason   *string `json:"reason"`
	Category *string `json:"category"`
	Meaning  string  `json:"meaning"`
	Next     *string `json:"next"`
}

// Limit is something the report cannot show.
type Limit struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Explanation is the offline reading of one report. Items and Limits are never nil.
type Explanation struct {
	Version string     `json:"version"`
	Report  ReportInfo `json:"report"`
	Items   []Item     `json:"items"`
	Limits  []Limit    `json:"limits"`
}

// JSON serializes the explanation with two-space indentation and a trailing newline.
func (e Explanation) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Explain interprets a saved evidence report. It validates the report with the
// same rules that wrote it, runs nothing and reads nothing else.
func Explain(data []byte) (Explanation, error) {
	if len(data) > MaxReportBytes {
		return Explanation{}, fmt.Errorf("%w: report exceeds 16 MiB", ErrInvalidReport)
	}
	var peek struct {
		SchemaVersion string `json:"schemaVersion"`
		Action        string `json:"action"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
	}
	switch peek.SchemaVersion {
	case evidence.SchemaVersion, evidence.MutationSchemaVersion:
		var r evidence.Report
		if err := decodeStrict(data, &r); err != nil {
			return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
		}
		if err := evidence.Validate(r); err != nil {
			return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
		}
		return explainReport(r)
	case evidence.CampaignSchemaVersion:
		var r evidence.CampaignReport
		if err := decodeStrict(data, &r); err != nil {
			return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
		}
		if err := evidence.ValidateCampaign(r); err != nil {
			return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
		}
		return explainCampaign(r)
	case evidence.ProfileV5:
		if peek.Action == "campaign" {
			r, err := evidence.DecodeCampaignV5(data)
			if err != nil {
				return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
			}
			return explainCampaign(r)
		}
		r, err := evidence.DecodeReportV5(data)
		if err != nil {
			return Explanation{}, fmt.Errorf("%w: %v", ErrInvalidReport, err)
		}
		return explainReport(r)
	}
	return Explanation{}, fmt.Errorf("%w: unsupported report version: %s", ErrUnsupported, peek.SchemaVersion)
}

// decodeStrict rejects unknown fields and anything after the one JSON value.
func decodeStrict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("unexpected data after the report")
	}
	return nil
}

var classMeaning = map[string]string{
	ClassPassed:       "The check passed.",
	ClassFailed:       "The check failed.",
	ClassDetected:     "The edit was caught.",
	ClassMissed:       "Nothing caught the edit.",
	ClassInconclusive: "No conclusion could be reached.",
	ClassExcluded:     "The result is excluded from the score and proof rate.",
	ClassNotRun:       "This did not run.",
}

// item builds an Item from a recorded outcome and reason. The reason is only
// recorded text: guidance is looked up for it, never inferred without one.
func item(id, kind, outcome, class, reason string) Item {
	it := Item{ID: id, Kind: kind, Outcome: outcome, Class: class, Meaning: classMeaning[class]}
	if reason == "" {
		return it
	}
	it.Reason = &reason
	if c := domain.ReasonCategory(reason); c != "" {
		it.Category = &c
	}
	if g, ok := GuidanceFor(reason); ok {
		it.Meaning = g.Meaning
		next := g.Next
		it.Next = &next
	}
	return it
}

func checkClass(s domain.CheckStatus) (string, bool) {
	switch s {
	case domain.Pass:
		return ClassPassed, true
	case domain.Fail:
		return ClassFailed, true
	case domain.Refuse:
		return ClassInconclusive, true
	case domain.NotRun:
		return ClassNotRun, true
	}
	return "", false
}

func proofClass(s domain.ProofStatus) (string, bool) {
	switch s {
	case domain.Proven:
		return ClassDetected, true
	case domain.Unproven:
		return ClassMissed, true
	case domain.Inconclusive:
		return ClassInconclusive, true
	case domain.ProofNotRun:
		return ClassNotRun, true
	}
	return "", false
}

// verdictClass classifies a recorded guardrail or mutation verdict.
func verdictClass(v string) (string, bool) {
	switch v {
	case string(domain.Proven), string(domain.MutationKilled):
		return ClassDetected, true
	case string(domain.Unproven), string(domain.MutationSurvived):
		return ClassMissed, true
	case string(domain.MutationInvalid):
		return ClassExcluded, true
	case string(domain.MutationInconclusive):
		return ClassInconclusive, true
	}
	return "", false
}

func unrecognized(kind, id, status string) error {
	return fmt.Errorf("%w: %s %s has unrecognized status %q", ErrInvalidReport, kind, id, status)
}

func explainReport(r evidence.Report) (Explanation, error) {
	e := Explanation{
		Version: ExplanationVersion,
		Report:  ReportInfo{r.SchemaVersion, r.Action, r.RunID, string(r.Decision), r.Harness},
		Items:   []Item{},
	}
	if r.Action == "mutate" && r.Experiment != nil {
		x := r.Experiment
		class, ok := verdictClass(string(x.Verdict))
		if !ok {
			return e, unrecognized("mutation", x.ID, string(x.Verdict))
		}
		e.Items = append(e.Items, item(x.ID, "mutation", string(x.Verdict), class, x.Reason))
	} else {
		for _, group := range []struct {
			kind   string
			checks []domain.Check
		}{{"step", r.Steps}, {"guard", r.Checks}} {
			for _, c := range group.checks {
				class, ok := checkClass(c.Status)
				if !ok {
					return e, unrecognized(group.kind, c.ID, string(c.Status))
				}
				e.Items = append(e.Items, item(c.ID, group.kind, string(c.Status), class, ""))
			}
		}
		for _, p := range r.Proofs {
			class, ok := proofClass(p.Status)
			if !ok {
				return e, unrecognized("proof", p.ID, string(p.Status))
			}
			e.Items = append(e.Items, item(p.ID, "proof", string(p.Status), class, p.Reason))
		}
	}
	var weak bool
	for _, p := range r.Proofs {
		weak = weak || weakProof(p)
	}
	e.Limits = limits(weak, r.SchemaVersion, r.Compatibility.Status == "legacy", r.GitDirty, r.Decision)
	return e, nil
}

func explainCampaign(r evidence.CampaignReport) (Explanation, error) {
	e := Explanation{
		Version: ExplanationVersion,
		Report:  ReportInfo{r.SchemaVersion, r.Action, r.RunID, string(r.Decision), r.Campaign},
		Items:   []Item{},
	}
	outcomes := r.Outcomes()
	var weak bool
	for i, x := range r.Experiments {
		o := outcomes[i]
		outcome := string(o.Stability)
		if x.AggregateVerdict != nil {
			outcome = *x.AggregateVerdict
		}
		if x.Selection == domain.FilteredOut {
			outcome = string(domain.FilteredOut)
		}
		var class string
		switch {
		case x.Selection != domain.Selected:
			class = ClassNotRun
		case o.Verdict != "":
			var ok bool
			if class, ok = verdictClass(o.Verdict); !ok {
				return e, unrecognized("experiment", x.ID, o.Verdict)
			}
		case o.Stability == domain.Mixed || o.Ran > 0:
			class = ClassInconclusive
		default:
			class = ClassNotRun
		}
		// A reason belongs to the experiment only when every attempt recorded
		// the same one.
		reason := ""
		for j, a := range x.Attempts {
			if j == 0 {
				reason = a.Reason
			} else if a.Reason != reason {
				reason = ""
				break
			}
		}
		for _, a := range x.Attempts {
			if a.Proof != nil && weakProof(*a.Proof) {
				weak = true
			}
		}
		e.Items = append(e.Items, item(x.ID, "experiment", outcome, class, reason))
	}
	e.Limits = limits(weak, r.SchemaVersion, false, r.SourceIdentity.GitDirty, r.Decision)
	return e, nil
}

func limits(weak bool, version string, legacy bool, dirty *bool, decision domain.Decision) []Limit {
	out := []Limit{}
	add := func(on bool, code, message string) {
		if on {
			out = append(out, Limit{code, message})
		}
	}
	add(weak, LimitWeakEvidence, "At least one proof rests on a text marker, which is weak evidence and not structured rule detection.")
	add(version == evidence.ProfileV5, LimitPortableProfile, "This portable report withholds machine paths, command arguments, output and free-text details.")
	add(legacy, LimitLegacyInput, "The input used a legacy contract whose guarantees are weaker than the native one.")
	add(dirty != nil && *dirty, LimitDirtyWorktree, "The working tree had uncommitted or untracked changes, so a Git revision alone does not identify the tested files.")
	add(decision == domain.RefuseDecision, LimitIncomplete, "The evidence is incomplete or unsupported, so the decision is a refusal, not a rejection.")
	add(true, LimitUnauthenticated, "The report is not authenticated: matching hashes show consistency, not who ran the check or how.")
	return out
}

// weakProof reports whether a completed assessment rests on anything but
// structured evidence. The decision logic treats every strength other than
// "strong" as weak, so a missing or unknown value must not read as structured.
func weakProof(p domain.Proof) bool {
	completed := p.Status == domain.Proven || p.Status == domain.Unproven
	return completed && p.EvidenceStrength != domain.StrengthStrong
}
