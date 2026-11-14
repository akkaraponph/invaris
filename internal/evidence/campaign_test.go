package evidence

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
)

var (
	campaignT0   = time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	snapshotHash = strings.Repeat("d", 64)
)

func obs(id, kind string, exit int) Observation {
	s, f := campaignT0, campaignT0.Add(time.Millisecond)
	return Observation{ID: id, Kind: kind, Program: "sh", Args: []string{"-c", "x"}, Provider: Provider{Name: "invaris-command"},
		Scope: []string{}, StartedAt: &s, FinishedAt: &f, ExitCode: ptr(exit), Diagnostics: []domain.Diagnostic{}, EnvAllowlist: []string{}}
}

func killedAttempt(experiment string, index int) CampaignAttempt {
	ok := func(name string) domain.Stage { return domain.Stage{Name: name, Status: domain.StageOK} }
	e := &MutationExperiment{
		ID: "m1", MutationID: "chg", Operator: "replace", Target: "src", TestCommandID: "test", ValidationCommandID: "valid",
		TestIDs: []string{"case"}, Verdict: domain.MutationKilled, Reason: domain.ReasonAssertionFail, ReasonCategory: domain.CategoryResult,
		SourceSnapshot: &domain.SourceSnapshot{SHA256: snapshotHash, Files: 2},
		Mutation:       &domain.Mutation{Path: "src", BeforeSHA256: strings.Repeat("a", 64), AfterSHA256: strings.Repeat("b", 64)},
		Stages:         []domain.Stage{ok("prepare"), ok("baseline"), ok("mutate"), ok("validation"), ok("tests"), ok("cleanup")},
		Baseline:       &MutationCommandResult{ObservationID: "m1:baseline", Tests: []domain.TestCaseResult{{ID: "case", Status: domain.TestPass}}},
		Validation:     &MutationCommandResult{ObservationID: "m1:validation", Validity: domain.MutantValid},
		Tests:          &MutationCommandResult{ObservationID: "m1:tests", Tests: []domain.TestCaseResult{{ID: "case", Status: domain.TestAssertionFailed}}},
	}
	s, f := campaignT0, campaignT0.Add(time.Second)
	return CampaignAttempt{ID: AttemptID(experiment, index), Index: index, Execution: domain.Executed, Verdict: ptr("KILLED"),
		Reason: e.Reason, ReasonCategory: e.ReasonCategory, StartedAt: &s, FinishedAt: &f, DurationMs: 1000, Mutation: e,
		Observations: []Observation{obs("m1:baseline", "baseline", 0), obs("m1:validation", "validation", 0), obs("m1:tests", "tests", 1)}}
}

func provenAttempt(experiment string, index int) CampaignAttempt {
	ok := func(name string) domain.Stage { return domain.Stage{Name: name, Status: domain.StageOK} }
	p := &domain.Proof{ID: "c1", PolicyID: "p", GuardID: "g", Target: "src", FaultID: "fault-1", EvidenceStrength: domain.StrengthStrong,
		Status: domain.Proven, Reason: domain.ReasonGuardDetected, ReasonCategory: domain.CategoryResult, Detail: "guard reported",
		SourceSnapshot: &domain.SourceSnapshot{SHA256: snapshotHash, Files: 2},
		Mutation:       &domain.Mutation{Path: "src", BeforeSHA256: strings.Repeat("a", 64), AfterSHA256: strings.Repeat("b", 64)},
		Stages:         []domain.Stage{ok("prepare"), ok("baseline"), ok("mutate"), ok("challenged"), ok("cleanup")},
		Baseline:       &domain.Check{ID: "c1:baseline", PolicyID: "p", Status: domain.Pass, Inspected: ptr(1), ExitCode: ptr(0)},
		Challenged:     &domain.Check{ID: "c1:challenged", PolicyID: "p", Status: domain.Fail, Inspected: ptr(1), ExitCode: ptr(1), Findings: []domain.Finding{{PolicyID: "p", RuleID: "p", Path: "src"}}}}
	s, f := campaignT0, campaignT0.Add(time.Second)
	return CampaignAttempt{ID: AttemptID(experiment, index), Index: index, Execution: domain.Executed, Verdict: ptr("PROVEN"),
		Reason: p.Reason, ReasonCategory: p.ReasonCategory, Detail: p.Detail, StartedAt: &s, FinishedAt: &f, DurationMs: 1000, Proof: p,
		Observations: []Observation{obs("c1:baseline", "baseline", 0), obs("c1:challenged", "challenged", 1)}}
}

// derive recomputes every derived field, as the engine does.
func derive(r *CampaignReport) {
	for i := range r.Experiments {
		x := &r.Experiments[i]
		v, s, reason := domain.Aggregate(x.Kind, attemptOutcomes(x.Attempts))
		x.Stability, x.StabilityReason, x.AggregateVerdict = s, reason, nil
		if v != "" {
			x.AggregateVerdict = &v
		}
	}
	out := r.Outcomes()
	r.MutationSummary, r.GuardrailSummary = domain.SummarizeMutations(out), domain.SummarizeGuardrails(out)
	r.AcceptanceEvaluation, r.Decision = domain.EvaluateAcceptance(r.AcceptancePolicy, out, r.Complete())
}

func validCampaign() CampaignReport {
	digest := "sha256:" + strings.Repeat("0", 64)
	r := CampaignReport{
		SchemaVersion: CampaignSchemaVersion, RunID: strings.Repeat("ab", 16), Action: "campaign", Campaign: "c", CampaignConfigDigest: digest,
		SourceIdentity: SourceIdentity{SnapshotSHA256: ptr(snapshotHash), Files: ptr(2), Excluded: []string{".git", ".invaris"},
			Roots: []SourceRoot{{Path: ".", SHA256: snapshotHash, Files: 2}, {Path: "sub", SHA256: strings.Repeat("e", 64), Files: 1}}},
		Environment: Environment{OS: "linux", Arch: "amd64"},
		Selection:   CampaignSelection{Mode: "filtered", Only: []string{"killed", "proven"}, Selected: 2, Filtered: 1},
		Parallelism: 1,
		StartedAt:   campaignT0, FinishedAt: campaignT0.Add(3 * time.Second), DurationMs: 3000,
		Stages: []domain.Stage{{Name: StageSnapshot, Status: domain.StageOK}, {Name: StageCleanup, Status: domain.StageOK}},
		Experiments: []CampaignExperiment{
			{ID: "killed", Kind: domain.KindMutation, Harness: "mut.json", Root: ".", HarnessDigest: digest, ReferenceID: "m1", Repeat: 1, Selection: domain.Selected, Attempts: []CampaignAttempt{killedAttempt("killed", 1)}},
			{ID: "proven", Kind: domain.KindGuardrail, Harness: "guard.json", Root: ".", HarnessDigest: digest, ReferenceID: "c1", Repeat: 1, Selection: domain.Selected, Attempts: []CampaignAttempt{provenAttempt("proven", 1)}},
			{ID: "skipped", Kind: domain.KindMutation, Harness: "sub/mut2.json", Root: "sub", HarnessDigest: digest, ReferenceID: "m2", Repeat: 2, Selection: domain.FilteredOut},
		},
		AcceptancePolicy: domain.AcceptancePolicy{RequireNoSurvivors: true, RequireAllGuardrailsProven: true, RequireNoInconclusive: true, RequireNoMixed: true},
	}
	derive(&r)
	return r
}

func TestValidCampaign(t *testing.T) {
	r := validCampaign()
	if err := ValidateCampaign(r); err != nil {
		t.Fatal(err)
	}
	if r.Decision != domain.Accept || r.MutationSummary.Score == nil || *r.MutationSummary.Score != 1 || r.MutationSummary.Filtered != 1 || r.GuardrailSummary.Proven != 1 {
		t.Fatalf("unexpected derived values: %+v %+v %s", r.MutationSummary, r.GuardrailSummary, r.Decision)
	}
}

func TestValidateCampaignRejects(t *testing.T) {
	for name, edit := range map[string]func(*CampaignReport){
		"schema version":         func(r *CampaignReport) { r.SchemaVersion = MutationSchemaVersion },
		"action":                 func(r *CampaignReport) { r.Action = "mutate" },
		"run id":                 func(r *CampaignReport) { r.RunID = "x" },
		"config digest":          func(r *CampaignReport) { r.CampaignConfigDigest = "md5:1" },
		"harness digest":         func(r *CampaignReport) { r.Experiments[0].HarnessDigest = "sha256:1" },
		"snapshot digest":        func(r *CampaignReport) { r.SourceIdentity.SnapshotSHA256 = ptr("xyz") },
		"snapshot without files": func(r *CampaignReport) { r.SourceIdentity.Files = nil },
		"excluded list":          func(r *CampaignReport) { r.SourceIdentity.Excluded = []string{".git"} },
		"root mismatch":          func(r *CampaignReport) { r.Experiments[0].Root = "sub" },
		"root without digest":    func(r *CampaignReport) { r.SourceIdentity.Roots = r.SourceIdentity.Roots[:1] },
		"whole root differs":     func(r *CampaignReport) { r.SourceIdentity.Roots[0].SHA256 = strings.Repeat("f", 64) },
		"roots unsorted": func(r *CampaignReport) {
			r.SourceIdentity.Roots[0], r.SourceIdentity.Roots[1] = r.SourceIdentity.Roots[1], r.SourceIdentity.Roots[0]
		},
		"no roots": func(r *CampaignReport) { r.SourceIdentity.Roots = nil },
		"unsafe harness path": func(r *CampaignReport) {
			r.Experiments[2].Harness, r.Experiments[2].Root = "../mut2.json", ".."
		},
		"time order":            func(r *CampaignReport) { r.FinishedAt = r.StartedAt.Add(-time.Second) },
		"unknown kind":          func(r *CampaignReport) { r.Experiments[0].Kind = "fuzz" },
		"unknown execution":     func(r *CampaignReport) { r.Experiments[0].Attempts[0].Execution = "SKIPPED" },
		"unknown selection":     func(r *CampaignReport) { r.Experiments[0].Selection = "MAYBE" },
		"unknown stability":     func(r *CampaignReport) { r.Experiments[0].Stability = "STABLE" },
		"unknown decision":      func(r *CampaignReport) { r.Decision = "PASS" },
		"missing cleanup stage": func(r *CampaignReport) { r.Stages = r.Stages[:1] },
		"cleanup first": func(r *CampaignReport) {
			r.Stages[0], r.Stages[1] = r.Stages[1], r.Stages[0]
		},
		"cleanup failed but accepted": func(r *CampaignReport) { r.Stages[1].Status = domain.StageFailed },
		"duplicate experiment":        func(r *CampaignReport) { r.Experiments[1].ID = "killed" },
		"duplicate attempt": func(r *CampaignReport) {
			r.Experiments[0].Repeat = 2
			r.Experiments[0].Attempts = append(r.Experiments[0].Attempts, killedAttempt("killed", 1))
		},
		"missing attempt":        func(r *CampaignReport) { r.Experiments[0].Repeat = 2 },
		"attempt out of order":   func(r *CampaignReport) { r.Experiments[0].Attempts[0].Index = 2 },
		"filtered with attempts": func(r *CampaignReport) { r.Experiments[2].Attempts = []CampaignAttempt{killedAttempt("skipped", 1)} },
		"selection counts":       func(r *CampaignReport) { r.Selection.Filtered = 0 },
		"selection mode":         func(r *CampaignReport) { r.Selection.Mode = "all" },
		"selection only":         func(r *CampaignReport) { r.Selection.Only = []string{"killed"} },
		"executed without evidence": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Mutation = nil
		},
		"evidence of the other kind": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Proof = provenAttempt("killed", 1).Proof
		},
		"not started with evidence": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Execution = domain.NotStarted
		},
		"verdict differs from child": func(r *CampaignReport) { r.Experiments[0].Attempts[0].Verdict = ptr("SURVIVED") },
		"canceled without reason":    func(r *CampaignReport) { r.Experiments[0].Attempts[0].Execution = domain.Canceled },
		"unknown reason": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Reason, r.Experiments[0].Attempts[0].ReasonCategory = "lucky", "result"
		},
		"killed without assertion evidence": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Mutation.Tests.Tests[0].Status = domain.TestPass
		},
		"killed without cleanup": func(r *CampaignReport) {
			m := r.Experiments[0].Attempts[0].Mutation
			m.Stages = m.Stages[:len(m.Stages)-1]
		},
		"killed observation missing": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Observations = r.Experiments[0].Attempts[0].Observations[:2]
		},
		"wrong mutation experiment": func(r *CampaignReport) { r.Experiments[0].ReferenceID = "m9" },
		"proven without findings":   func(r *CampaignReport) { r.Experiments[1].Attempts[0].Proof.Challenged.Findings = nil },
		"proven wrong policy": func(r *CampaignReport) {
			r.Experiments[1].Attempts[0].Proof.Challenged.Findings[0].PolicyID = "q"
		},
		"proven wrong target": func(r *CampaignReport) {
			r.Experiments[1].Attempts[0].Proof.Challenged.Findings[0].Path = "other"
		},
		"proven weak": func(r *CampaignReport) {
			r.Experiments[1].Attempts[0].Proof.EvidenceStrength = domain.StrengthWeak
		},
		"proven failed cleanup": func(r *CampaignReport) {
			p := r.Experiments[1].Attempts[0].Proof
			p.Stages[len(p.Stages)-1].Status = domain.StageFailed
		},
		"proven without mutation": func(r *CampaignReport) { r.Experiments[1].Attempts[0].Proof.Mutation = nil },
		"other source snapshot": func(r *CampaignReport) {
			r.Experiments[1].Attempts[0].Proof.SourceSnapshot = &domain.SourceSnapshot{SHA256: strings.Repeat("e", 64), Files: 2}
		},
		"conclusive without snapshot": func(r *CampaignReport) {
			r.Experiments[0].Attempts[0].Mutation.SourceSnapshot = nil
		},
		"aggregate verdict":       func(r *CampaignReport) { r.Experiments[0].AggregateVerdict = ptr("SURVIVED") },
		"aggregate stability":     func(r *CampaignReport) { r.Experiments[0].Stability = domain.Mixed },
		"mutation summary":        func(r *CampaignReport) { r.MutationSummary.Killed = 2 },
		"score denominator":       func(r *CampaignReport) { r.MutationSummary.Eligible = 2 },
		"score value":             func(r *CampaignReport) { r.MutationSummary.Score = ptr(0.5) },
		"score null":              func(r *CampaignReport) { r.MutationSummary.Score = nil },
		"guardrail summary":       func(r *CampaignReport) { r.GuardrailSummary.Unproven = 1 },
		"proof rate value":        func(r *CampaignReport) { r.GuardrailSummary.ProofRate = ptr(0.5) },
		"proof rate null":         func(r *CampaignReport) { r.GuardrailSummary.ProofRate = nil },
		"proof rate denominator":  func(r *CampaignReport) { r.GuardrailSummary.Eligible = 2 },
		"decision against policy": func(r *CampaignReport) { r.Decision = domain.Reject },
		"evaluation tampered": func(r *CampaignReport) {
			r.AcceptanceEvaluation.Requirements[0].Status = domain.RequirementViolated
		},
		"policy without requirement": func(r *CampaignReport) { r.AcceptancePolicy = domain.AcceptancePolicy{} },
		"ran without snapshot": func(r *CampaignReport) {
			r.Stages = []domain.Stage{{Name: StageSnapshot, Status: domain.StageFailed}}
		},
		"artifact":         func(r *CampaignReport) { r.Artifacts = []Artifact{{Path: "a", SHA256: "x"}} },
		"parallelism zero": func(r *CampaignReport) { r.Parallelism = 0 },
		"parallelism nine": func(r *CampaignReport) { r.Parallelism = 9 },
		"tool kind":        func(r *CampaignReport) { r.Tools = []Observation{obs("tool:go", "baseline", 0)} },
		"tool id":          func(r *CampaignReport) { r.Tools = []Observation{obs("go", "tool", 0)} },
		"tool repeated": func(r *CampaignReport) {
			r.Tools = []Observation{obs("tool:go", "tool", 0), obs("tool:go", "tool", 0)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validCampaign()
			edit(&r)
			if err := ValidateCampaign(r); err == nil {
				t.Fatal("accepted")
			}
			if _, err := MarshalCampaign(r); err == nil {
				t.Fatal("marshaled")
			}
		})
	}
}

// A refused campaign with attempts that never started is valid evidence.
func TestValidateCampaignNotStarted(t *testing.T) {
	r := validCampaign()
	r.Stages = []domain.Stage{{Name: StageSnapshot, Status: domain.StageFailed, Detail: "copy failed"}}
	r.SourceIdentity.SnapshotSHA256, r.SourceIdentity.Files, r.SourceIdentity.Roots = nil, nil, nil
	for i := range r.Experiments[:2] {
		r.Experiments[i].Attempts[0] = CampaignAttempt{ID: r.Experiments[i].Attempts[0].ID, Index: 1, Execution: domain.NotStarted,
			Reason: domain.ReasonPrepareFailed, ReasonCategory: domain.CategoryInfrastructure}
	}
	derive(&r)
	if err := ValidateCampaign(r); err != nil {
		t.Fatal(err)
	}
	if r.Decision != domain.RefuseDecision || r.MutationSummary.NotExecuted != 1 || r.GuardrailSummary.NotExecuted != 1 || r.MutationSummary.Score != nil {
		t.Fatalf("%s %+v %+v", r.Decision, r.MutationSummary, r.GuardrailSummary)
	}
}

func TestMarshalCampaignDeterministic(t *testing.T) {
	r := validCampaign()
	a, err := MarshalCampaign(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := MarshalCampaign(validCampaign())
	if !bytes.Equal(a, b) {
		t.Fatal("equal reports marshal differently")
	}
	var back CampaignReport
	if err := json.Unmarshal(a, &back); err != nil || ValidateCampaign(back) != nil {
		t.Fatalf("round trip: %v %v", err, ValidateCampaign(back))
	}
	s := string(a)
	for _, want := range []string{`"artifacts": []`, `"diagnostics": []`, `"gitDirty": null`, `"aggregateVerdict": null`, `"attempts": []`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Index(s, `"id": "killed"`) > strings.Index(s, `"id": "proven"`) || strings.Index(s, `"id": "proven"`) > strings.Index(s, `"id": "skipped"`) {
		t.Error("declaration order not preserved")
	}
	if strings.Index(s, `"schemaVersion"`) > strings.Index(s, `"runId"`) || strings.Index(s, `"decision"`) > strings.LastIndex(s, `"diagnostics": []`) {
		t.Error("field order changed")
	}
	// A null score serializes as null, not 0.
	r.Experiments[0].Attempts = []CampaignAttempt{{ID: "killed#1", Index: 1, Execution: domain.Refused, Reason: domain.ReasonWorkspaceChanged, ReasonCategory: domain.CategoryEvidence}}
	derive(&r)
	out, err := MarshalCampaign(r)
	if err != nil || !strings.Contains(string(out), `"score": null`) || !strings.Contains(string(out), `"verdict": null`) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestRedactCampaign(t *testing.T) {
	const secret = "hunter2-token"
	r := validCampaign()
	a := &r.Experiments[0].Attempts[0]
	a.Observations[0].Stdout = "value " + secret
	a.Observations[1].Stdout, a.Observations[1].Truncated = "cut hunter2-to", true
	a.Detail = "failed with " + secret
	r.Diagnostics = []domain.Diagnostic{{Code: "X", Message: secret}}
	RedactCampaign(&r, []string{secret})
	data, err := MarshalCampaign(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("hunter2")) || bytes.Count(data, []byte(Marker)) != 4 {
		t.Fatalf("secret survived:\n%s", data)
	}
}
