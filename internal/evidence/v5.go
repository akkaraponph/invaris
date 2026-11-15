package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/mutation"
)

// ProfileV5 is the portable evidence profile. It extends the run, check,
// challenge and mutate report and the campaign report with built-in mutation
// metadata, command digests, the engine version and, for campaigns,
// effectiveness rows; and it withholds machine paths, argument lists, output
// excerpts and free-text diagnostics. Old inputs produce it only on request.
const ProfileV5 = "invaris.evidence/v1alpha5"

// Withheld replaces free text and executable paths in ProfileV5 reports.
const (
	Withheld     = "details withheld"
	WithheldPath = "<withheld>"
)

// Application states of a built-in mutation.
const (
	NotApplied = "NOT_APPLIED" // the mutate stage was not reached
	Rejected   = "REJECTED"    // the mutate stage failed; nothing was written
	Applied    = "APPLIED"     // the edit was installed
)

// ReproductionPrefix is the first element of the reproduction identity.
const ReproductionPrefix = "invaris.reproduction/v1"

// ExpectedDetection is the one finding a built-in challenge must produce.
type ExpectedDetection struct {
	GuardID  string `json:"guardId"`
	PolicyID string `json:"policyId"`
	RuleID   string `json:"ruleId"`
	Path     string `json:"path"`
}

// MutationMetadata describes one built-in mutation proof (a standalone proof
// or one campaign attempt).
type MutationMetadata struct {
	EvidenceID      string            `json:"evidenceId"`
	Operator        string            `json:"operator"`
	OperatorVersion string            `json:"operatorVersion"`
	Target          string            `json:"target"`
	PreimageSHA256  string            `json:"preimageSha256"`
	Fixture         string            `json:"fixture,omitempty"`
	Application     string            `json:"application"`
	BeforeSHA256    *string           `json:"beforeSha256"`
	AfterSHA256     *string           `json:"afterSha256"`
	Expected        ExpectedDetection `json:"expected"`
	ReproductionID  *string           `json:"reproductionId"`
}

// CommandDigest identifies the configured command of one observation without
// echoing its arguments.
type CommandDigest struct {
	ObservationID string `json:"observationId"`
	SHA256        string `json:"sha256"`
}

var sha256Prefixed = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func compactJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // strings, string slices and ints always encode
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func prefixedDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReproductionID is the SHA-256 of the compact JSON array (no HTML escaping)
// ["invaris.reproduction/v1", configDigest, sourceSnapshotSHA256, challengeID,
// guardID, operator, operatorVersion, fixture, target, beforeSHA256,
// afterSHA256, policyID, ruleID]. It identifies the declared inputs and the
// edit, not the tools or the environment, and contains no run-specific value.
func ReproductionID(configDigest, snapshot, challengeID, guardID, operator, operatorVersion, fixture, target, before, after, policyID, ruleID string) string {
	return prefixedDigest(compactJSON([]string{ReproductionPrefix, configDigest, snapshot, challengeID, guardID,
		operator, operatorVersion, fixture, target, before, after, policyID, ruleID}))
}

// CommandDigestOf hashes the compact JSON array [program, args, envAllowlist,
// timeoutSeconds], with absent lists as [] and a zero timeout as the 30-second
// default.
func CommandDigestOf(program string, args, env []string, timeoutSeconds int) string {
	if args == nil {
		args = []string{}
	}
	if env == nil {
		env = []string{}
	}
	if timeoutSeconds == 0 {
		timeoutSeconds = 30
	}
	return prefixedDigest(compactJSON([]any{program, args, env, timeoutSeconds}))
}

// ObservationDigest is CommandDigestOf for an observation before projection.
func ObservationDigest(o Observation) CommandDigest {
	t := 0
	if o.TimeoutSeconds != nil {
		t = *o.TimeoutSeconds
	}
	return CommandDigest{ObservationID: o.ID, SHA256: CommandDigestOf(o.Program, o.Args, o.EnvAllowlist, t)}
}

// ApplicationOf reads the application state from a proof's stages.
func ApplicationOf(stages []domain.Stage) string {
	for _, s := range stages {
		if s.Name == domain.StageMutate {
			if s.Status == domain.StageOK {
				return Applied
			}
			return Rejected
		}
	}
	return NotApplied
}

// NewMutationMetadata builds the metadata of a built-in proof from the
// declaration and the recorded proof. configDigest is the digest of the
// harness that declared it ("" when unknown).
func NewMutationMetadata(evidenceID string, spec mutation.Spec, guardID string, p domain.Proof, configDigest string) MutationMetadata {
	m := MutationMetadata{EvidenceID: evidenceID, Operator: spec.Operator, OperatorVersion: spec.Version, Target: p.Target,
		PreimageSHA256: spec.PreimageSHA256, Fixture: spec.Fixture, Application: ApplicationOf(p.Stages),
		Expected: ExpectedDetection{GuardID: guardID, PolicyID: p.PolicyID, RuleID: p.PolicyID, Path: p.Target}}
	if m.Application == Applied && p.Mutation != nil {
		before, after := p.Mutation.BeforeSHA256, p.Mutation.AfterSHA256
		m.BeforeSHA256, m.AfterSHA256 = &before, &after
		if configDigest != "" && p.SourceSnapshot != nil {
			id := ReproductionID(configDigest, p.SourceSnapshot.SHA256, p.ID, guardID, spec.Operator, spec.Version,
				spec.Fixture, p.Target, before, after, p.PolicyID, p.PolicyID)
			m.ReproductionID = &id
		}
	}
	return m
}

// validateMetadata checks one entry against the proof it describes.
func validateMetadata(m MutationMetadata, p domain.Proof, configDigest *string, bad func(string, ...any)) {
	spec := mutation.Spec{Operator: m.Operator, Version: m.OperatorVersion, PreimageSHA256: m.PreimageSHA256, Fixture: m.Fixture}
	if err := mutation.Validate(spec); err != nil {
		bad("mutation metadata %q: %v", m.EvidenceID, err)
	}
	e := m.Expected
	if e.GuardID == "" || e.PolicyID == "" || e.RuleID != e.PolicyID || e.Path != m.Target || m.Target != p.Target ||
		e.GuardID != p.GuardID || e.PolicyID != p.PolicyID || p.EvidenceStrength != domain.StrengthStrong {
		bad("mutation metadata %q does not match its strong proof's guard, policy, rule and target", m.EvidenceID)
	}
	if m.Application != ApplicationOf(p.Stages) {
		bad("mutation metadata %q application %q disagrees with the proof stages", m.EvidenceID, m.Application)
	}
	switch m.Application {
	case Applied:
		mm := p.Mutation
		if m.BeforeSHA256 == nil || m.AfterSHA256 == nil || mm == nil || *m.BeforeSHA256 != mm.BeforeSHA256 ||
			*m.AfterSHA256 != mm.AfterSHA256 || *m.BeforeSHA256 == *m.AfterSHA256 || *m.BeforeSHA256 != m.PreimageSHA256 {
			bad("mutation metadata %q hashes disagree with the applied edit or the declared preimage", m.EvidenceID)
		}
		var want *string
		if configDigest != nil && p.SourceSnapshot != nil && m.BeforeSHA256 != nil && m.AfterSHA256 != nil {
			id := ReproductionID(*configDigest, p.SourceSnapshot.SHA256, p.ID, m.Expected.GuardID, m.Operator, m.OperatorVersion,
				m.Fixture, m.Target, *m.BeforeSHA256, *m.AfterSHA256, m.Expected.PolicyID, m.Expected.RuleID)
			want = &id
		}
		if (want == nil) != (m.ReproductionID == nil) || want != nil && *want != *m.ReproductionID {
			bad("mutation metadata %q has a wrong reproductionId", m.EvidenceID)
		}
	default:
		if m.BeforeSHA256 != nil || m.AfterSHA256 != nil || m.ReproductionID != nil || p.Mutation != nil {
			bad("mutation metadata %q records an edit that was not applied", m.EvidenceID)
		}
		if p.Status == domain.Proven || p.Status == domain.Unproven {
			bad("mutation metadata %q: a conclusive proof requires an applied edit", m.EvidenceID)
		}
	}
}

// validateDigests checks that digests align one-to-one with observations,
// and that a challenged guard and a mutant's tests ran the same configured
// command as their baseline, which withheld arguments cannot otherwise show.
func validateDigests(digests []CommandDigest, observations []Observation, prefix string, bad func(string, ...any)) {
	if len(digests) != len(observations) {
		bad("%d command digests for %d observations", len(digests), len(observations))
		return
	}
	for i, o := range observations {
		if digests[i].ObservationID != prefix+o.ID || !sha256Prefixed.MatchString(digests[i].SHA256) {
			bad("command digest %d does not identify observation %q", i, prefix+o.ID)
		}
	}
	baseline := -1
	for i, o := range observations {
		switch o.Kind {
		case "baseline":
			baseline = i
		case "challenged":
			if i == 0 || observations[i-1].Kind != "baseline" || observations[i-1].ID != o.ID || digests[i].SHA256 != digests[i-1].SHA256 {
				bad("challenged observation %q did not run the baseline's command", prefix+o.ID)
			}
		case "tests":
			if baseline < 0 || digests[i].SHA256 != digests[baseline].SHA256 {
				bad("mutant tests %q did not run the baseline's command", prefix+o.ID)
			}
		}
	}
}

// freeText reports whether s is permitted free text in a ProfileV5 report.
func freeText(s string) bool { return s == "" || s == Withheld }

func validatePortableObservations(obs []Observation, bad func(string, ...any)) {
	for _, o := range obs {
		if len(o.Args) != 0 || o.Stdout != "" || o.Stderr != "" || o.Executable != nil && o.Executable.Path != WithheldPath ||
			strings.ContainsAny(o.Program, `/\`) {
			bad("observation %q exposes arguments, output or an executable path", o.ID)
		}
		validatePortableDiagnostics(o.Diagnostics, bad)
	}
}

func validatePortableDiagnostics(ds []domain.Diagnostic, bad func(string, ...any)) {
	for _, d := range ds {
		if !freeText(d.Message) {
			bad("diagnostic %q exposes its message", d.Code)
		}
	}
}

func validatePortableCheck(c domain.Check, bad func(string, ...any)) {
	validatePortableDiagnostics(c.Diagnostics, bad)
	for _, f := range c.Findings {
		if !freeText(f.Message) {
			bad("finding in %q exposes its message", c.ID)
		}
	}
}

func validatePortableProof(p domain.Proof, bad func(string, ...any)) {
	if !freeText(p.Detail) {
		bad("proof %q exposes its detail", p.ID)
	}
	for _, s := range p.Stages {
		if !freeText(s.Detail) {
			bad("proof %q stage %q exposes its detail", p.ID, s.Name)
		}
	}
	for _, c := range []*domain.Check{p.Baseline, p.Challenged} {
		if c != nil {
			validatePortableCheck(*c, bad)
		}
	}
}

// validateV5 checks what ProfileV5 adds to a run, check, challenge or mutate
// report: the engine version, metadata, digests and the portable projection.
func validateV5(r Report, bad func(string, ...any)) {
	if r.EngineVersion != nil && *r.EngineVersion == "" {
		bad("engineVersion must be null or nonempty")
	}
	if r.Target != "." {
		bad(`target must be "." in %s`, ProfileV5)
	}
	proofs := map[string]domain.Proof{}
	for _, p := range r.Proofs {
		proofs[p.ID] = p
	}
	seen := map[string]bool{}
	for _, m := range r.MutationMetadata {
		p, ok := proofs[m.EvidenceID]
		if !ok || seen[m.EvidenceID] {
			bad("mutation metadata %q names no proof or is repeated", m.EvidenceID)
			continue
		}
		seen[m.EvidenceID] = true
		validateMetadata(m, p, r.ConfigDigest, bad)
	}
	validateDigests(r.CommandDigests, r.Observations, "", bad)
	validatePortableObservations(r.Observations, bad)
	validatePortableDiagnostics(r.Diagnostics, bad)
	for _, c := range append(append([]domain.Check{}, r.Steps...), r.Checks...) {
		validatePortableCheck(c, bad)
	}
	for _, p := range r.Proofs {
		validatePortableProof(p, bad)
	}
	if e := r.Experiment; e != nil {
		for _, s := range e.Stages {
			if !freeText(s.Detail) {
				bad("mutation stage %q exposes its detail", s.Name)
			}
		}
	}
}

// ProjectPortable applies the ProfileV5 privacy projection in place, after
// classification and secret redaction: the target root becomes ".",
// executable paths "<withheld>", argument lists [], output excerpts "" and
// nonempty free text "details withheld". IDs, statuses, exits, hashes,
// counts, durations and configured targets are kept. It never changes a
// verdict or a decision.
func ProjectPortable(r *Report) {
	r.Target = "."
	projectObservations(r.Observations)
	projectDiagnostics(r.Diagnostics)
	for i := range r.Steps {
		projectCheck(&r.Steps[i])
	}
	for i := range r.Checks {
		projectCheck(&r.Checks[i])
	}
	for i := range r.Proofs {
		projectProof(&r.Proofs[i])
	}
	if e := r.Experiment; e != nil {
		projectStages(e.Stages)
	}
}

func withhold(s *string) {
	if *s != "" {
		*s = Withheld
	}
}

func projectObservations(obs []Observation) {
	for i := range obs {
		o := &obs[i]
		o.Args, o.Stdout, o.Stderr = []string{}, "", ""
		if strings.ContainsAny(o.Program, `/\`) {
			o.Program = WithheldPath // a configured path can name the user's machine
		}
		if o.Executable != nil {
			e := *o.Executable
			e.Path = WithheldPath
			o.Executable = &e
		}
		projectDiagnostics(o.Diagnostics)
	}
}

func projectDiagnostics(ds []domain.Diagnostic) {
	for i := range ds {
		withhold(&ds[i].Message)
	}
}

func projectStages(st []domain.Stage) {
	for i := range st {
		withhold(&st[i].Detail)
	}
}

func projectCheck(c *domain.Check) {
	if c == nil {
		return
	}
	c.Diagnostics = append([]domain.Diagnostic(nil), c.Diagnostics...)
	projectDiagnostics(c.Diagnostics)
	c.Findings = append([]domain.Finding(nil), c.Findings...)
	for i := range c.Findings {
		withhold(&c.Findings[i].Message)
	}
}

func projectProof(p *domain.Proof) {
	withhold(&p.Detail)
	p.Stages = append([]domain.Stage(nil), p.Stages...)
	projectStages(p.Stages)
	for _, c := range []**domain.Check{&p.Baseline, &p.Challenged} {
		if *c != nil {
			copy := **c
			projectCheck(&copy)
			*c = &copy
		}
	}
}

// reportV5Wire is the ProfileV5 serialization of Report: the shared fields,
// then the fields the profile adds, which are required (null or []).
type reportV5Wire struct {
	Report
	EngineVersion    *string            `json:"engineVersion"`
	MutationMetadata []MutationMetadata `json:"mutationMetadata"`
	CommandDigests   []CommandDigest    `json:"commandDigests"`
}

func normalizeV5(r Report) reportV5Wire {
	w := reportV5Wire{Report: r, EngineVersion: r.EngineVersion, MutationMetadata: r.MutationMetadata, CommandDigests: r.CommandDigests}
	if w.MutationMetadata == nil {
		w.MutationMetadata = []MutationMetadata{}
	}
	if w.CommandDigests == nil {
		w.CommandDigests = []CommandDigest{}
	}
	return w
}

// DecodeReportV5 parses a ProfileV5 run, check, challenge or mutate report,
// including the fields it adds, and validates it.
func DecodeReportV5(data []byte) (Report, error) {
	var w reportV5Wire
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return Report{}, err
	}
	r := w.Report
	r.EngineVersion, r.MutationMetadata, r.CommandDigests = w.EngineVersion, w.MutationMetadata, w.CommandDigests
	if r.SchemaVersion != ProfileV5 {
		return r, fmt.Errorf("schemaVersion %q is not %s", r.SchemaVersion, ProfileV5)
	}
	return r, Validate(r)
}

// campaignDigestPrefix scopes an attempt's observation IDs, which repeat
// across attempts, in the campaign's flat digest list.
func campaignDigestPrefix(attemptID string) string { return attemptID + "/" }

// CampaignCommandDigests lists the digests of the campaign's observations in
// report order: tool probes, then each attempt's observations, whose IDs are
// prefixed with "<attemptId>/". Call it before ProjectPortableCampaign.
func CampaignCommandDigests(r CampaignReport) []CommandDigest {
	out := []CommandDigest{}
	for _, o := range r.Tools {
		out = append(out, ObservationDigest(o))
	}
	for _, x := range r.Experiments {
		for _, a := range x.Attempts {
			for _, o := range a.Observations {
				d := ObservationDigest(o)
				d.ObservationID = campaignDigestPrefix(a.ID) + d.ObservationID
				out = append(out, d)
			}
		}
	}
	return out
}

// ProjectPortableCampaign applies the ProfileV5 projection to a campaign
// report and every child record in place.
func ProjectPortableCampaign(r *CampaignReport) {
	projectObservations(r.Tools)
	projectDiagnostics(r.Diagnostics)
	projectStages(r.Stages)
	for i := range r.Experiments {
		for j := range r.Experiments[i].Attempts {
			a := &r.Experiments[i].Attempts[j]
			withhold(&a.Detail)
			projectObservations(a.Observations)
			if a.Proof != nil {
				p := *a.Proof
				projectProof(&p)
				a.Proof = &p
			}
			if a.Mutation != nil {
				m := *a.Mutation
				m.Stages = append([]domain.Stage(nil), m.Stages...)
				projectStages(m.Stages)
				a.Mutation = &m
			}
		}
	}
}

func validateCampaignV5(r CampaignReport, bad func(string, ...any)) {
	attempts := map[string]struct {
		x CampaignExperiment
		a CampaignAttempt
	}{}
	var tools []Observation
	tools = append(tools, r.Tools...)
	digests := r.CommandDigests
	n := len(tools)
	if len(digests) < n {
		bad("%d command digests for at least %d observations", len(digests), n)
		return
	}
	validateDigests(digests[:n], tools, "", bad)
	validatePortableObservations(r.Tools, bad)
	validatePortableDiagnostics(r.Diagnostics, bad)
	for _, s := range r.Stages {
		if !freeText(s.Detail) {
			bad("campaign stage %q exposes its detail", s.Name)
		}
	}
	for _, x := range r.Experiments {
		for _, a := range x.Attempts {
			attempts[a.ID] = struct {
				x CampaignExperiment
				a CampaignAttempt
			}{x, a}
			k := len(a.Observations)
			if n+k > len(digests) {
				bad("attempt %q observations have no command digests", a.ID)
				return
			}
			validateDigests(digests[n:n+k], a.Observations, campaignDigestPrefix(a.ID), bad)
			n += k
			validatePortableObservations(a.Observations, bad)
			if !freeText(a.Detail) {
				bad("attempt %q exposes its detail", a.ID)
			}
			if a.Proof != nil {
				validatePortableProof(*a.Proof, bad)
			}
			if a.Mutation != nil {
				for _, s := range a.Mutation.Stages {
					if !freeText(s.Detail) {
						bad("attempt %q mutation stage %q exposes its detail", a.ID, s.Name)
					}
				}
			}
		}
	}
	if n != len(digests) {
		bad("%d command digests for %d observations", len(digests), n)
	}
	seen := map[string]bool{}
	for _, m := range r.MutationMetadata {
		xa, ok := attempts[m.EvidenceID]
		if !ok || seen[m.EvidenceID] || xa.x.Kind != domain.KindGuardrail || xa.a.Proof == nil {
			bad("mutation metadata %q names no started guardrail attempt or is repeated", m.EvidenceID)
			continue
		}
		seen[m.EvidenceID] = true
		digest := xa.x.HarnessDigest
		validateMetadata(m, *xa.a.Proof, &digest, bad)
	}
	validateEffectiveness(r, bad)
}

// campaignV5Wire is the ProfileV5 serialization of CampaignReport.
type campaignV5Wire struct {
	CampaignReport
	MutationMetadata []MutationMetadata `json:"mutationMetadata"`
	CommandDigests   []CommandDigest    `json:"commandDigests"`
	Effectiveness    Effectiveness      `json:"effectiveness"`
}

func normalizeCampaignV5(r CampaignReport) campaignV5Wire {
	w := campaignV5Wire{CampaignReport: r, MutationMetadata: r.MutationMetadata, CommandDigests: r.CommandDigests}
	if w.MutationMetadata == nil {
		w.MutationMetadata = []MutationMetadata{}
	}
	if w.CommandDigests == nil {
		w.CommandDigests = []CommandDigest{}
	}
	if r.Effectiveness != nil {
		w.Effectiveness = *r.Effectiveness
	}
	w.Effectiveness = normalizeEffectiveness(w.Effectiveness)
	return w
}

// DecodeCampaignV5 parses a ProfileV5 campaign report and validates it.
func DecodeCampaignV5(data []byte) (CampaignReport, error) {
	var w campaignV5Wire
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return CampaignReport{}, err
	}
	r := w.CampaignReport
	e := w.Effectiveness
	r.MutationMetadata, r.CommandDigests, r.Effectiveness = w.MutationMetadata, w.CommandDigests, &e
	if r.SchemaVersion != ProfileV5 {
		return r, fmt.Errorf("schemaVersion %q is not %s", r.SchemaVersion, ProfileV5)
	}
	return r, ValidateCampaign(r)
}
