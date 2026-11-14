package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// treeDigest hashes every path and file body so any write-back is visible.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		h.Write([]byte(p))
		h.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func decodeReport(t *testing.T, raw []byte) evidence.Report {
	t.Helper()
	var r evidence.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode report: %v: %s", err, raw)
	}
	return r
}

// TestGoProjectExample runs the Go example through the CLI. It needs the go
// tool and builds and runs real Go tests, so it is skipped with -short.
func TestGoProjectExample(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the Go toolchain")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	dir := filepath.Join("..", "..", "examples", "go-project")
	before := treeDigest(t, dir)
	t.Cleanup(func() {
		if treeDigest(t, dir) != before {
			t.Error("the example tree changed: a run wrote back to the source")
		}
	})

	t.Run("guardrail", func(t *testing.T) {
		code, out, stderr := invoke(t, dir, "check", "guardrail.json")
		if r := decodeReport(t, out); code != 0 || r.Decision != domain.Accept {
			t.Fatalf("clean source: exit %d, %v: %s", code, r.Decision, stderr)
		}
		code, out, stderr = invoke(t, dir, "challenge", "guardrail.json")
		r := decodeReport(t, out)
		if code != 0 || len(r.Proofs) != 1 {
			t.Fatalf("challenge: exit %d: %s %s", code, stderr, out)
		}
		p := r.Proofs[0]
		if p.Status != domain.Proven || p.PolicyID != "no-network-imports" || p.Target != "internal/discount/format.go" ||
			p.EvidenceStrength != domain.StrengthStrong || p.Mutation == nil || p.Challenged == nil ||
			len(p.Challenged.Findings) != 1 || p.Challenged.Findings[0].Path != p.Target {
			t.Fatalf("unexpected proof: %+v", p)
		}
	})

	source, err := os.ReadFile(filepath.Join(dir, "internal", "discount", "discount.go"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	for _, tc := range []struct {
		name    string
		code    int
		verdict domain.MutationVerdict
		reason  string
		tests   string
	}{
		{"mutation-killed", 0, domain.MutationKilled, "assertion_failed", domain.TestAssertionFailed},
		{"mutation-survived", 1, domain.MutationSurvived, "all_tests_passed", domain.TestPass},
		{"mutation-invalid", 2, domain.MutationInvalid, "mutant_invalid", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := invoke(t, dir, "mutate", tc.name+".json")
			e := decodeReport(t, out).Experiment
			if code != tc.code || e == nil || e.Verdict != tc.verdict || e.Reason != tc.reason {
				t.Fatalf("exit %d: %+v %s", code, e, stderr)
			}
			if e.Baseline == nil || len(e.Baseline.Tests) != 2 || e.Mutation == nil ||
				e.Mutation.BeforeSHA256 != hex.EncodeToString(sum[:]) || e.Mutation.AfterSHA256 == e.Mutation.BeforeSHA256 {
				t.Fatalf("baseline or mutation evidence wrong: %+v", e)
			}
			if tc.tests == "" {
				if e.Tests != nil {
					t.Fatal("tests ran against an invalid mutant")
				}
				return
			}
			// The same selected IDs run on the baseline and the mutant; only the
			// killed run has a failing case.
			failed := 0
			for _, c := range e.Tests.Tests {
				if c.Status == domain.TestAssertionFailed {
					failed++
				}
			}
			if (tc.tests == domain.TestAssertionFailed) != (failed > 0) || len(e.Tests.Tests) != 2 {
				t.Fatalf("unexpected mutant results: %+v", e.Tests)
			}
		})
	}

	t.Run("campaign", func(t *testing.T) {
		t.Setenv("INVARIS_FIXTURE_STATE", t.TempDir())
		type want struct{ verdict, reason string }
		for _, tc := range []struct {
			name     string
			args     []string
			code     int
			decision domain.Decision
			results  map[string]want
		}{
			{"six scenarios", []string{"campaign", "campaign.json"}, 2, domain.RefuseDecision, map[string]want{
				"mutation-killed": {"KILLED", "assertion_failed"}, "mutation-survived": {"SURVIVED", "all_tests_passed"},
				"mutation-invalid": {"INVALID", "mutant_invalid"}, "guardrail-proven": {"PROVEN", "guard_detected"},
				"guardrail-unproven": {"UNPROVEN", "guard_passed"}, "guardrail-inconclusive": {"INCONCLUSIVE", "execution_failed"}}},
			{"accept", []string{"campaign", "campaign-accept.json"}, 0, domain.Accept, map[string]want{
				"mutation-killed": {"KILLED", "assertion_failed"}, "guardrail-proven": {"PROVEN", "guard_detected"}}},
			{"filtered survivor", []string{"campaign", "--only", "mutation-survived", "campaign.json"}, 1, domain.Reject, map[string]want{
				"mutation-survived": {"SURVIVED", "all_tests_passed"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				code, out, stderr := invoke(t, dir, tc.args...)
				r := decodeCampaign(t, out)
				if code != tc.code || r.Decision != tc.decision {
					t.Fatalf("exit %d decision %s: %s", code, r.Decision, stderr)
				}
				for _, x := range r.Experiments {
					w, selected := tc.results[x.ID]
					if !selected {
						if x.Selection != domain.FilteredOut {
							t.Fatalf("%s should be filtered", x.ID)
						}
						continue
					}
					if a := x.Attempts[0]; a.Verdict == nil || *a.Verdict != w.verdict || a.Reason != w.reason {
						t.Fatalf("%s: %v %s, want %+v", x.ID, a.Verdict, a.Reason, w)
					}
				}
			})
		}
		t.Run("score in text", func(t *testing.T) {
			code, out, _ := invoke(t, dir, "campaign", "--format", "text", "campaign.json")
			if code != 2 || !strings.Contains(string(out), "Score:         50.0%") || !strings.Contains(string(out), "Acceptance: REFUSE") {
				t.Fatalf("exit %d:\n%s", code, out)
			}
		})
		t.Run("mixed", func(t *testing.T) {
			code, out, stderr := invoke(t, dir, "campaign", "campaign-mixed.json")
			r := decodeCampaign(t, out)
			x := r.Experiments[0]
			var got []string
			for _, a := range x.Attempts {
				got = append(got, *a.Verdict)
			}
			if code != 2 || strings.Join(got, ",") != "KILLED,SURVIVED,KILLED" || x.Stability != domain.Mixed || x.AggregateVerdict != nil || r.MutationSummary.Score != nil {
				t.Fatalf("exit %d %v %s: %s", code, got, x.Stability, stderr)
			}
		})
	})

	t.Run("preimage pin matches the source", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(dir, "mutation-killed.json"))
		if err != nil {
			t.Fatal(err)
		}
		var h struct {
			Experiment struct {
				Mutation struct {
					Preimage string `json:"preimageSha256"`
				} `json:"mutation"`
			} `json:"experiment"`
		}
		if err := json.Unmarshal(raw, &h); err != nil || h.Experiment.Mutation.Preimage != hex.EncodeToString(sum[:]) {
			t.Fatalf("preimageSha256 is stale: %v", err)
		}
	})
}
