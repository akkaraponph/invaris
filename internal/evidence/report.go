// Package evidence defines the versioned invaris.evidence/v1alpha2 report,
// its validation, deterministic serialization, redaction and artifact storage.
package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
)

const SchemaVersion = "invaris.evidence/v1alpha2"

type Environment struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Compatibility records whether the input used a legacy contract whose
// guarantees are weaker than the native one.
type Compatibility struct {
	Status       string   `json:"status"` // "native" or "legacy"
	InputVersion string   `json:"inputVersion"`
	Warnings     []string `json:"warnings"`
}

// Provider identifies the tool that produced an observation; versions are null
// when unknown. Legacy commands report the built-in command runner.
type Provider struct {
	Name        string  `json:"name"`
	Version     *string `json:"version"`
	ToolName    *string `json:"toolName"`
	ToolVersion *string `json:"toolVersion"`
}

// Observation is what was run for one step, guard or proof stage. Excerpts are
// bounded, redacted and kept separate per stream.
type Observation struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"` // step, guard, baseline, challenged
	Program     string              `json:"program"`
	Args        []string            `json:"args"`
	Provider    Provider            `json:"provider"`
	Scope       []string            `json:"scope"`
	StartedAt   *time.Time          `json:"startedAt"`
	FinishedAt  *time.Time          `json:"finishedAt"`
	ExitCode    *int                `json:"exitCode"`
	Stdout      string              `json:"stdout"`
	Stderr      string              `json:"stderr"`
	Truncated   bool                `json:"truncated"`
	Diagnostics []domain.Diagnostic `json:"diagnostics"`
}

// Event is one lifecycle entry in configuration order.
type Event struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // step, guard, challenge
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// Artifact describes a persisted file. SHA256 covers the exact persisted
// (redacted) bytes; the report is never hashed into itself.
type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Report struct {
	SchemaVersion string              `json:"schemaVersion"`
	RunID         string              `json:"runId"`
	Action        string              `json:"action"`
	Target        string              `json:"target"`
	Harness       string              `json:"harness"`
	ConfigDigest  *string             `json:"configDigest"`
	GitRevision   *string             `json:"gitRevision"`
	Environment   Environment         `json:"environment"`
	StartedAt     time.Time           `json:"startedAt"`
	FinishedAt    time.Time           `json:"finishedAt"`
	DurationMs    int64               `json:"durationMs"`
	Compatibility Compatibility       `json:"compatibility"`
	Policies      []domain.Policy     `json:"policies"`
	Gate          domain.Gate         `json:"gate"`
	Lifecycle     []Event             `json:"lifecycle"`
	Steps         []domain.Check      `json:"steps"`
	Checks        []domain.Check      `json:"checks"`
	Proofs        []domain.Proof      `json:"proofs"`
	Observations  []Observation       `json:"observations"`
	TargetCount   *int                `json:"targetCount"`
	Decision      domain.Decision     `json:"decision"`
	Diagnostics   []domain.Diagnostic `json:"diagnostics"`
	Artifacts     []Artifact          `json:"artifacts"`
}

var (
	runIDPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	hashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate checks structure for every report, and the additional completeness
// requirements for an ACCEPT. It also recomputes the gate decision: a report
// whose recorded decision differs from the decision its own results support is
// invalid. A report that cannot be accepted may still validate (it is evidence
// of a failure or refusal); an ACCEPT never validates without full evidence.
func Validate(r Report) error {
	var problems []error
	bad := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }
	if r.SchemaVersion != SchemaVersion {
		bad("schemaVersion must be %q", SchemaVersion)
	}
	if !runIDPattern.MatchString(r.RunID) {
		bad("runId must be 128-bit lowercase hex")
	}
	switch r.Action {
	case "run", "check", "challenge":
	default:
		bad("unknown action %q", r.Action)
	}
	switch r.Decision {
	case domain.Accept, domain.Reject, domain.RefuseDecision:
	default:
		bad("invalid decision %q", r.Decision)
	}
	if r.FinishedAt.Before(r.StartedAt) {
		bad("finishedAt precedes startedAt")
	}
	if r.Gate.Precedence != domain.FailureFirst && r.Gate.Precedence != domain.RefusalFirst {
		bad("gate precedence must be %q or %q", domain.FailureFirst, domain.RefusalFirst)
	}
	if r.ConfigDigest != nil && !digestPattern.MatchString(*r.ConfigDigest) {
		bad("configDigest must be sha256:<64 hex>")
	}
	for _, c := range append(append([]domain.Check{}, r.Steps...), r.Checks...) {
		validateCheck(c, bad)
	}
	for _, p := range r.Proofs {
		switch p.Status {
		case domain.Proven, domain.Unproven, domain.Inconclusive, domain.ProofNotRun:
		default:
			bad("proof %q has invalid status %q", p.ID, p.Status)
		}
		for _, c := range []*domain.Check{p.Baseline, p.Challenged} {
			if c != nil {
				validateCheck(*c, bad)
			}
		}
	}
	for _, a := range r.Artifacts {
		if a.Path == "" || !hashPattern.MatchString(a.SHA256) || a.Bytes < 0 {
			bad("artifact %q is malformed", a.Path)
		}
	}
	if want := domain.Decide(r.Gate, r.Steps, r.Checks, r.Proofs); r.Decision != want {
		bad("decision %s does not match the %s supported by the results", r.Decision, want)
	}
	if r.Decision == domain.Accept {
		validateAccepted(r, bad)
	}
	return errors.Join(problems...)
}

func validateCheck(c domain.Check, bad func(string, ...any)) {
	switch c.Status {
	case domain.Pass, domain.Fail, domain.Refuse, domain.NotRun:
	default:
		bad("check %q has invalid status %q", c.ID, c.Status)
	}
	if c.Status == domain.Pass && len(c.Findings) > 0 {
		bad("check %q passed with findings", c.ID)
	}
	if c.Status == domain.Fail && len(c.Findings) == 0 && c.ExitCode == nil {
		bad("check %q failed without findings or exit code", c.ID)
	}
}

func validateAccepted(r Report, bad func(string, ...any)) {
	if r.ConfigDigest == nil {
		bad("accepted report requires configDigest")
	}
	if r.TargetCount == nil || *r.TargetCount <= 0 {
		bad("accepted report requires a positive targetCount")
	}
	allowEmpty := map[string]bool{}
	for _, p := range r.Policies {
		allowEmpty[p.ID] = p.AllowEmpty
	}
	for _, c := range r.Checks {
		if c.Status == domain.Pass && c.PolicyID != "" && (c.Inspected == nil || (*c.Inspected <= 0 && !allowEmpty[c.PolicyID])) {
			bad("accepted check %q has no inspected targets", c.ID)
		}
	}
	for _, p := range r.Proofs {
		if p.Status != domain.Proven {
			continue
		}
		if p.Baseline == nil || p.Baseline.Status != domain.Pass {
			bad("proven %q requires a passing baseline", p.ID)
		}
		if p.Challenged == nil || p.Challenged.Status != domain.Fail {
			bad("proven %q requires a failing challenged check", p.ID)
		}
		if p.FaultID == "" || p.Target == "" {
			bad("proven %q requires a fault and target", p.ID)
		}
		if p.EvidenceStrength == domain.StrengthStrong && p.Challenged != nil {
			if f := p.Challenged.Findings; len(f) != 1 || f[0].PolicyID != p.PolicyID || f[0].Path != p.Target {
				bad("strong proof %q requires exactly one finding for its policy at its target", p.ID)
			}
		}
	}
	for _, a := range r.Artifacts {
		if a.Bytes <= 0 {
			bad("accepted report artifact %q has no bytes", a.Path)
		}
	}
	if r.Compatibility.Status != "native" && r.Compatibility.Status != "legacy" {
		bad("compatibility status must be native or legacy")
	}
}

// Marshal serializes a validated report. Equal reports marshal to identical
// bytes: field order is fixed, there are no maps, and absent lists are
// normalized to empty arrays. An invalid report is an error, not output.
func Marshal(r Report) ([]byte, error) {
	r = normalize(r)
	if err := Validate(r); err != nil {
		return nil, fmt.Errorf("invalid evidence report: %w", err)
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

func normalize(r Report) Report {
	if r.Policies == nil {
		r.Policies = []domain.Policy{}
	}
	if r.Gate.RequiredGuards == nil {
		r.Gate.RequiredGuards = []string{}
	}
	if r.Gate.RequiredProofs == nil {
		r.Gate.RequiredProofs = []string{}
	}
	if r.Compatibility.Warnings == nil {
		r.Compatibility.Warnings = []string{}
	}
	if r.Lifecycle == nil {
		r.Lifecycle = []Event{}
	}
	if r.Observations == nil {
		r.Observations = []Observation{}
	}
	if r.Diagnostics == nil {
		r.Diagnostics = []domain.Diagnostic{}
	}
	if r.Artifacts == nil {
		r.Artifacts = []Artifact{}
	}
	r.Steps = normalizeChecks(r.Steps)
	r.Checks = normalizeChecks(r.Checks)
	if r.Proofs == nil {
		r.Proofs = []domain.Proof{}
	}
	proofs := make([]domain.Proof, len(r.Proofs))
	for i, p := range r.Proofs {
		if p.Baseline != nil {
			c := normalizeCheck(*p.Baseline)
			p.Baseline = &c
		}
		if p.Challenged != nil {
			c := normalizeCheck(*p.Challenged)
			p.Challenged = &c
		}
		proofs[i] = p
	}
	r.Proofs = proofs
	observations := make([]Observation, len(r.Observations))
	for i, o := range r.Observations {
		if o.Args == nil {
			o.Args = []string{}
		}
		if o.Scope == nil {
			o.Scope = []string{}
		}
		if o.Diagnostics == nil {
			o.Diagnostics = []domain.Diagnostic{}
		}
		observations[i] = o
	}
	r.Observations = observations
	return r
}

func normalizeChecks(in []domain.Check) []domain.Check {
	out := make([]domain.Check, len(in))
	for i, c := range in {
		out[i] = normalizeCheck(c)
	}
	return out
}

func normalizeCheck(c domain.Check) domain.Check {
	if c.Findings == nil {
		c.Findings = []domain.Finding{}
	}
	if c.Diagnostics == nil {
		c.Diagnostics = []domain.Diagnostic{}
	}
	return c
}
