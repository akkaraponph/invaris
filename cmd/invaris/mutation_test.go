package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

func TestMutationFixturesCLI(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "mutation")
	source := filepath.Join(dir, "behavior.sh")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		code    int
		verdict domain.MutationVerdict
		outcome string
	}{
		{"killed", 0, domain.MutationKilled, domain.TestAssertionFailed},
		{"survived", 1, domain.MutationSurvived, domain.TestPass},
		{"invalid", 2, domain.MutationInvalid, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "report.json")
			code, stdout, stderr := invoke(t, ".", "--report", out, "mutate", filepath.Join(dir, tc.name+".json"))
			var report evidence.Report
			err := json.Unmarshal(stdout, &report)
			if code != tc.code || err != nil || report.Experiment == nil {
				t.Fatalf("CLI exit %d, decode %v: %s %s", code, err, stderr, stdout)
			}
			saved, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(stdout, saved) {
				t.Fatalf("report publication differs: %v", err)
			}
			e := report.Experiment
			if e.Verdict != tc.verdict || e.Mutation == nil || e.Mutation.BeforeSHA256 == e.Mutation.AfterSHA256 ||
				e.Baseline == nil || len(e.Baseline.Tests) != 1 || e.Baseline.Tests[0].Status != domain.TestPass ||
				e.Validation == nil || len(e.TestIDs) != 1 || e.TestIDs[0] != "covered" {
				t.Fatalf("unexpected record: %+v", e)
			}
			if tc.outcome == "" {
				if e.Tests != nil || e.Validation.Validity != domain.MutantInvalid {
					t.Fatalf("invalid mutant had unexpected test/validity results: %+v", e)
				}
			} else if e.Tests == nil || len(e.Tests.Tests) != 1 || e.Tests.Tests[0].ID != "covered" ||
				e.Tests.Tests[0].Status != tc.outcome || e.Validation.Validity != domain.MutantValid {
				t.Fatalf("unexpected test/validity results: %+v", e)
			}
			if after, err := os.ReadFile(source); err != nil || !bytes.Equal(after, before) {
				t.Fatalf("source changed: %v", err)
			}
		})
	}
	for _, args := range [][]string{
		{"challenge", filepath.Join(dir, "killed.json")},
		{"mutate", filepath.Join("..", "..", "examples", "invaris.json")},
	} {
		if code, stdout, _ := invoke(t, ".", args...); code != 2 || len(stdout) != 0 {
			t.Fatalf("version mismatch %v: exit %d, stdout %s", args, code, stdout)
		}
	}
}
