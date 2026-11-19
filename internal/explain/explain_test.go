package explain

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func codes(e Explanation) []string {
	out := []string{}
	for _, l := range e.Limits {
		out = append(out, l.Code)
	}
	return out
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestExplainFixtures(t *testing.T) {
	type want struct {
		file, version, action, decision string
		outcomes, classes               []string // per item, in order; nil skips
		reason, category                string   // of the first item not passed
		limits                          []string
	}
	tests := []want{
		// examples/structured uses the legacy invaris.dev/v1alpha1 harness, so
		// legacy-input applies; the fixture was generated from a clean tree.
		{file: "structured-challenge.json", version: "invaris.evidence/v1alpha2", action: "challenge", decision: "ACCEPT",
			classes: []string{"detected"}, limits: []string{"legacy-input", "unauthenticated"}},
		{file: "structured-check.json", version: "invaris.evidence/v1alpha2", action: "check", decision: "ACCEPT",
			outcomes: []string{"PASS"}, classes: []string{"passed"}, limits: []string{"legacy-input", "unauthenticated"}},
		{file: "weak-challenge.json", version: "invaris.evidence/v1alpha2", action: "challenge", decision: "ACCEPT",
			reason: "marker_matched", limits: []string{"weak-evidence", "legacy-input", "unauthenticated"}},
		{file: "guardrail-unproven.json", version: "invaris.evidence/v1alpha2", action: "challenge", decision: "REJECT",
			classes: []string{"missed"}, reason: "guard_passed", category: "result", limits: []string{"unauthenticated"}},
		{file: "mutation-killed.json", version: "invaris.evidence/v1alpha3", action: "mutate", decision: "ACCEPT",
			outcomes: []string{"KILLED"}, classes: []string{"detected"}, reason: "assertion_failed", limits: []string{"unauthenticated"}},
		{file: "mutation-survived.json", version: "invaris.evidence/v1alpha3", action: "mutate", decision: "REJECT",
			outcomes: []string{"SURVIVED"}, classes: []string{"missed"}, reason: "all_tests_passed", limits: []string{"unauthenticated"}},
		{file: "mutation-invalid.json", version: "invaris.evidence/v1alpha3", action: "mutate", decision: "REFUSE",
			outcomes: []string{"INVALID"}, classes: []string{"excluded"}, reason: "mutant_invalid", category: "result",
			limits: []string{"incomplete", "unauthenticated"}},
		{file: "campaign.json", version: "invaris.evidence/v1alpha4", action: "campaign", decision: "REFUSE",
			outcomes: []string{"KILLED", "SURVIVED", "INVALID", "PROVEN", "UNPROVEN", "INCOMPLETE"},
			classes:  []string{"detected", "missed", "excluded", "detected", "missed", "inconclusive"},
			limits:   []string{"incomplete", "unauthenticated"}},
		{file: "operators-challenge-v5.json", version: "invaris.evidence/v1alpha5", action: "challenge", decision: "ACCEPT",
			limits: []string{"portable-profile", "unauthenticated"}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			e, err := Explain(fixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if e.Version != "invaris.explanation/v1alpha1" || e.Report.SchemaVersion != tc.version ||
				e.Report.Action != tc.action || e.Report.Decision != tc.decision {
				t.Fatalf("report info = %+v", e)
			}
			if got := codes(e); !slices.Equal(got, tc.limits) {
				t.Fatalf("limits = %v, want %v", got, tc.limits)
			}
			var classes, outcomes []string
			for _, it := range e.Items {
				classes, outcomes = append(classes, it.Class), append(outcomes, it.Outcome)
			}
			if tc.classes != nil && !slices.Equal(classes, tc.classes) {
				t.Fatalf("classes = %v, want %v", classes, tc.classes)
			}
			if tc.outcomes != nil && !slices.Equal(outcomes, tc.outcomes) {
				t.Fatalf("outcomes = %v, want %v", outcomes, tc.outcomes)
			}
			if tc.reason != "" {
				var found bool
				for _, it := range e.Items {
					if str(it.Reason) == tc.reason {
						found = true
						if tc.category != "" && str(it.Category) != tc.category {
							t.Fatalf("category = %s, want %s", str(it.Category), tc.category)
						}
						if it.Next == nil || it.Meaning == "" {
							t.Fatalf("known reason without guidance: %+v", it)
						}
					}
				}
				if !found {
					t.Fatalf("no item with reason %s: %+v", tc.reason, e.Items)
				}
			}
		})
	}
}

func TestExplainCampaignInconclusiveReason(t *testing.T) {
	e, err := Explain(fixture(t, "campaign.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Items) != 6 {
		t.Fatalf("items = %d, want 6", len(e.Items))
	}
	last := e.Items[5]
	if last.Kind != "experiment" || last.Class != "inconclusive" || str(last.Reason) != "malformed_evidence" || str(last.Category) != "evidence" {
		t.Fatalf("inconclusive item = %+v", last)
	}
	if str(e.Items[0].Reason) != "assertion_failed" || e.Report.Subject != "go-project-verification" {
		t.Fatalf("first item %+v subject %q", e.Items[0], e.Report.Subject)
	}
}

func TestExplainNoReasonUsesClassSentence(t *testing.T) {
	e, err := Explain(fixture(t, "structured-check.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range e.Items {
		if it.Reason != nil || it.Category != nil || it.Next != nil || it.Meaning != "The check passed." {
			t.Fatalf("passed item = %+v", it)
		}
	}
	if e.Items[0].Kind != "guard" {
		t.Fatalf("kind = %s", e.Items[0].Kind)
	}
}

func TestExplainJSONShape(t *testing.T) {
	e, err := Explain(fixture(t, "structured-check.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := e.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("}\n")) || !bytes.Contains(data, []byte("\n  \"items\": [")) {
		t.Fatalf("not indented JSON with trailing newline:\n%s", data)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	first := items[0].(map[string]any)
	if v, ok := first["reason"]; !ok || v != nil {
		t.Fatalf("absent reason must serialize as null: %v", first)
	}
	if first["next"] != nil || first["category"] != nil {
		t.Fatalf("absent next/category must be null: %v", first)
	}
}

func TestExplainEmptyListsAreNotNull(t *testing.T) {
	data, err := Explanation{Version: ExplanationVersion, Items: []Item{}, Limits: []Limit{}}.JSON()
	if err != nil || !bytes.Contains(data, []byte(`"items": []`)) || !bytes.Contains(data, []byte(`"limits": []`)) {
		t.Fatalf("%v %s", err, data)
	}
}

func TestExplainRefusesTamperedDecision(t *testing.T) {
	data := fixture(t, "guardrail-unproven.json")
	tampered := bytes.Replace(data, []byte(`"decision": "REJECT"`), []byte(`"decision": "ACCEPT"`), 1)
	if bytes.Equal(data, tampered) {
		t.Fatal("fixture has no REJECT decision to flip")
	}
	if _, err := Explain(tampered); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("err = %v, want ErrInvalidReport", err)
	}
	camp := fixture(t, "campaign.json")
	tampered = bytes.Replace(camp, []byte(`"decision": "REFUSE"`), []byte(`"decision": "ACCEPT"`), 1)
	if _, err := Explain(tampered); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("campaign err = %v, want ErrInvalidReport", err)
	}
}

func TestExplainUnsupported(t *testing.T) {
	_, err := Explain([]byte(`{"schemaVersion":"invaris.evidence/v9"}`))
	if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalidReport) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "unsupported report version: invaris.evidence/v9") {
		t.Fatalf("message = %q", err)
	}
}

func TestExplainInvalidInputs(t *testing.T) {
	good := fixture(t, "mutation-killed.json")
	tests := map[string][]byte{
		"not json":         []byte("hello"),
		"empty":            nil,
		"trailing garbage": append(bytes.Clone(good), []byte("garbage")...),
		"second document":  append(bytes.Clone(good), good...),
		"unknown field":    bytes.Replace(good, []byte(`"runId"`), []byte(`"extra": 1,`+"\n  "+`"runId"`), 1),
		"v5 unknown field": bytes.Replace(fixture(t, "operators-challenge-v5.json"), []byte(`"runId"`), []byte(`"extra": 1,`+"\n  "+`"runId"`), 1),
		"campaign unknown": bytes.Replace(fixture(t, "campaign.json"), []byte(`"runId"`), []byte(`"extra": 1,`+"\n  "+`"runId"`), 1),
		"wrong type":       []byte(`{"schemaVersion": 3}`),
		"too large":        bytes.Repeat([]byte(" "), MaxReportBytes+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Explain(data)
			if !errors.Is(err, ErrInvalidReport) {
				t.Fatalf("err = %v, want ErrInvalidReport", err)
			}
		})
	}
	if _, err := Explain(bytes.Repeat([]byte(" "), MaxReportBytes+1)); err == nil || !strings.Contains(err.Error(), "report exceeds 16 MiB") {
		t.Fatalf("size error = %v", err)
	}
	if _, err := Explain(good); err != nil {
		t.Fatalf("control: unmodified report must explain: %v", err)
	}
}

func TestExplainLimitsOrder(t *testing.T) {
	got := limits(true, "invaris.evidence/v1alpha5", true, boolPtr(true), "REFUSE")
	want := []string{"weak-evidence", "portable-profile", "legacy-input", "dirty-worktree", "incomplete", "unauthenticated"}
	var codes []string
	for _, l := range got {
		codes = append(codes, l.Code)
		if l.Message == "" || strings.Contains(l.Message, "\n") {
			t.Fatalf("limit %s needs a one-line message", l.Code)
		}
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("limits = %v, want %v", codes, want)
	}
	if got := limits(false, "invaris.evidence/v1alpha2", false, boolPtr(false), "ACCEPT"); len(got) != 1 || got[0].Code != "unauthenticated" {
		t.Fatalf("minimal limits = %v", got)
	}
}

func TestExplainDirtyWorktree(t *testing.T) {
	for _, file := range []string{"mutation-survived.json", "campaign.json"} {
		data := fixture(t, file)
		dirty := bytes.Replace(data, []byte(`"gitDirty": false`), []byte(`"gitDirty": true`), 1)
		if bytes.Equal(data, dirty) {
			t.Fatalf("%s: no gitDirty to flip", file)
		}
		e, err := Explain(dirty)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !slices.Contains(codes(e), "dirty-worktree") {
			t.Fatalf("%s limits = %v", file, codes(e))
		}
		clean, _ := Explain(data)
		if slices.Contains(codes(clean), "dirty-worktree") {
			t.Fatalf("%s: clean report must not carry dirty-worktree", file)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// A weak marker proof must raise weak-evidence even when its recorded strength
// is blank or unrecognized, as the gate treats anything but "strong" as weak.
func TestWeakEvidenceIsAnythingButStrong(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "weak-challenge.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"weak", "", "bogus"} {
		data := bytes.Replace(raw, []byte(`"evidenceStrength": "weak"`), []byte(`"evidenceStrength": "`+value+`"`), 1)
		if value != "weak" && bytes.Equal(data, raw) {
			t.Fatal("fixture has no evidenceStrength to change")
		}
		e, err := Explain(data)
		if err != nil {
			t.Fatalf("strength %q: %v", value, err)
		}
		found := false
		for _, l := range e.Limits {
			found = found || l.Code == "weak-evidence"
		}
		if !found {
			t.Errorf("strength %q: weak-evidence limit missing: %v", value, e.Limits)
		}
	}
}

// An inconclusive mutation quotes the recorded stage detail and the baseline
// tests that did not pass, so the cause is visible without reading raw JSON.
func TestExplainMutationDetails(t *testing.T) {
	x := &evidence.MutationExperiment{
		Verdict: domain.MutationInconclusive,
		Reason:  "unhealthy_baseline",
		Stages: []domain.Stage{
			{Name: "prepare", Status: domain.StageOK},
			{Name: "baseline", Status: "FAILED", Detail: "unhealthy_baseline"},
		},
		Baseline: &evidence.MutationCommandResult{Tests: []domain.TestCaseResult{
			{ID: "ok", Status: domain.TestPass},
			{ID: "suite::needs state", Status: domain.TestAssertionFailed},
		}},
	}
	got := mutationDetails(x, "attempt 1: ")
	want := []string{"attempt 1: baseline test suite::needs state: ASSERTION_FAILED"}
	if !slices.Equal(got, want) {
		t.Fatalf("details = %q, want %q", got, want)
	}
	x.Stages[1].Detail = "malformed_evidence: test evidence has 41 tests but the harness selects 1 testIds"
	x.Reason, x.Baseline = "malformed_evidence", nil
	if got := mutationDetails(x, ""); len(got) != 1 || !strings.HasPrefix(got[0], "stage baseline: malformed_evidence: test evidence has 41") {
		t.Fatalf("stage detail = %q", got)
	}
	x.Verdict = domain.MutationKilled
	if got := mutationDetails(x, ""); got != nil {
		t.Fatalf("killed mutation has details %q", got)
	}
	many := make([]string, maxDetails+3)
	for i := range many {
		many[i] = "d"
	}
	if c := capDetails(many); len(c) != maxDetails+1 || c[maxDetails] != "... 3 more" {
		t.Fatalf("cap = %d %q", len(c), c[len(c)-1])
	}
}
