package engine

import (
	"context"
	"testing"
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
