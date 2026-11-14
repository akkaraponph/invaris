package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
)

func TestInspectionRejectsInvalidEvidence(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"empty scope", `echo '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":0,"findings":[]}'`},
		{"wrong rule", `echo '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"q","path":"file"}]}'; exit 1`},
		{"failed tool with clean report", `echo '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}'; exit 1`},
		{"malformed response", `echo '{'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkGuard(context.Background(), t.TempDir(), Command{ID: "guard", Program: "sh", Args: []string{"-c", tc.script}, PolicyID: "p"})
			if got.Status != "REFUSE" {
				t.Fatalf("accepted invalid evidence: %+v", got)
			}
		})
	}
}

func TestMinInspectedIsIndependentOfReportedCount(t *testing.T) {
	script := `echo '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}'`
	run := func(min int) Result {
		return checkGuard(context.Background(), t.TempDir(), Command{ID: "g", Program: "sh", Args: []string{"-c", script}, PolicyID: "p", MinInspected: min})
	}
	if got := run(1); got.Status != "PASS" {
		t.Fatalf("floor met: %+v", got)
	}
	if got := run(2); got.Status != "REFUSE" {
		t.Fatalf("narrowed scope accepted: %+v", got)
	}
	s := Specification{Version: versionV1, Name: "n", Steps: []Command{{ID: "s", Program: "sh", MinInspected: 1}}}
	if s.Validate() == nil {
		t.Fatal("minInspected accepted without a policyId")
	}
}

func TestStructuredFixture(t *testing.T) {
	root := "../../examples/structured"
	spec, err := Load(root + "/invaris.json")
	if err != nil {
		t.Fatal(err)
	}
	got := Run(context.Background(), spec, root, "challenge")
	if got.Status != "PASS" || len(got.Results) != 1 || got.Results[0].Status != "PROVEN" || got.Results[0].Challenged.Inspection.Findings[0].Path != "source.txt" {
		t.Fatalf("expected structured proof: %+v", got)
	}
}

// Rejected structured output from a guard that exited 0 or 1 (a completed
// assessment) is malformed evidence. Other exits are the tool's own failure.
func TestRejectedGuardEvidenceReason(t *testing.T) {
	clean := `{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}`
	onNew := func(then string) string {
		return `if [ "$(cat src)" = new ]; then ` + then + `; fi; printf '%s\n' '` + clean + `'`
	}
	for _, tc := range []struct{ name, guard, reason string }{
		{"wrong policy, exit 1", onNew(`printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"q","path":"src"}]}'; exit 1`), domain.ReasonMalformedEvidence},
		{"no findings, exit 1", onNew(`printf '%s\n' '` + clean + `'; exit 1`), domain.ReasonMalformedEvidence},
		{"unparsable, exit 1", onNew(`printf '{'; exit 1`), domain.ReasonMalformedEvidence},
		{"finding, exit 0", onNew(`printf '%s\n' '{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"p","path":"src"}]}'; exit 0`), domain.ReasonMalformedEvidence},
		{"tool failure, exit 2", onNew(`echo broken >&2; exit 2`), domain.ReasonExecutionFailed},
		{"output limit", onNew(`head -c 70000 /dev/zero; exit 1`), domain.ReasonOutputLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "src", "old")
			writeFile(t, dir, "h.json", guardHarness(t, "c1", tc.guard, "p"))
			spec, err := Load(filepath.Join(dir, "h.json"))
			if err != nil {
				t.Fatal(err)
			}
			r := RunEvidence(context.Background(), spec, dir, "challenge")
			if p := r.Proofs[0]; p.Status != domain.Inconclusive || p.Reason != tc.reason {
				t.Fatalf("got %s %s, want INCONCLUSIVE %s", p.Status, p.Reason, tc.reason)
			}
		})
	}
}
