//go:build integration

package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

func TestDependencyCruiserProof(t *testing.T) {
	original, err := filepath.Abs("../../examples/dependency-cruiser")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(root string) map[string]workspace.Entry {
		t.Helper()
		entries, err := workspace.Snapshot(root)
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	before := snapshot(original)
	t.Cleanup(func() {
		if !reflect.DeepEqual(before, snapshot(original)) {
			t.Error("original fixture changed")
		}
	})
	spec, err := Load(filepath.Join(original, "invaris.json"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for this integration: ", err)
	}
	spec.Adapters[0].Program = node
	spec.Adapters[0].Args = []string{filepath.Join(original, "../../adapters/dependency-cruiser/provider.mjs")}

	ctx := context.Background()
	const rule = "no-forbidden-import"
	check := func(c *domain.Check, status domain.CheckStatus, exit int) {
		t.Helper()
		if c == nil || c.Status != status || c.PolicyID != rule || c.Inspected == nil || *c.Inspected != 3 || c.ExitCode == nil || *c.ExitCode != exit {
			t.Fatalf("expected %s, 3 inspected modules and exit %d, got %+v", status, exit, c)
		}
		if status == domain.Fail && (len(c.Findings) != 1 || c.Findings[0].RuleID != rule || c.Findings[0].Path != "src/main.js") {
			t.Fatalf("unexpected findings: %+v", c.Findings)
		}
	}
	run := RunEvidence(ctx, spec, original, "check")
	if run.Decision != domain.Accept || len(run.Checks) != 1 || len(run.Observations) != 2 {
		t.Fatalf("clean fixture failed (run npm ci --prefix adapters/dependency-cruiser to set up): %+v", run)
	}
	check(&run.Checks[0], domain.Pass, 0)
	if v := run.Observations[0].Provider.ToolVersion; v == nil || *v != "18.7.2" {
		t.Fatalf("wrong tool version: %v", v)
	}
	t.Log("dependency-cruiser 18.7.2: clean PASS, 3 modules, exit 0")

	copyFixture := func(t *testing.T) string {
		t.Helper()
		ws, err := workspace.Prepare(original)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := ws.Close(); err != nil {
				t.Error(err)
			}
		})
		return ws.Root
	}
	changeRuleTarget := func(t *testing.T, root, replacement string) {
		t.Helper()
		config := filepath.Join(root, ".dependency-cruiser.cjs")
		data, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		changed := strings.Replace(string(data), `^src/forbidden\\.js$`, replacement, 1)
		if changed == string(data) {
			t.Fatal("fixture lacks expected rule target")
		}
		if err := os.WriteFile(config, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("violating project", func(t *testing.T) {
		root := copyFixture(t)
		if _, err := workspace.Replace(root, "src/main.js", "./allowed.js", "./forbidden.js"); err != nil {
			t.Fatal(err)
		}
		report := RunEvidence(ctx, spec, root, "check")
		if report.Decision != domain.Reject || len(report.Checks) != 1 {
			t.Fatalf("violating fixture did not reject: %+v", report)
		}
		check(&report.Checks[0], domain.Fail, 1)
		t.Log("FAIL, no-forbidden-import at src/main.js, 3 modules, exit 1")
	})

	t.Run("invalid native config", func(t *testing.T) {
		root := copyFixture(t)
		// Discovery can name the rule, but the analyzer rejects its regex.
		changeRuleTarget(t, root, "[")
		report := RunEvidence(ctx, spec, root, "check")
		if report.Decision != domain.RefuseDecision || len(report.Checks) != 1 {
			t.Fatalf("invalid config did not refuse: %+v", report)
		}
		c := report.Checks[0]
		if c.Status != domain.Refuse || c.ExitCode == nil || *c.ExitCode != 1 || c.Inspected != nil || len(c.Findings) != 0 {
			t.Fatalf("invalid config lost its native failure: %+v", c)
		}
		t.Log("REFUSE on invalid native config, exit 1, no inspected count")
	})

	for _, tc := range []struct {
		name string
		want domain.ProofStatus
	}{
		{"effective rule", domain.Proven},
		{"ineffective rule", domain.Unproven},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := original
			if tc.want == domain.Unproven {
				root = copyFixture(t)
				changeRuleTarget(t, root, `^src/never-matches\\.js$`)
			}
			proofRun := RunEvidence(ctx, spec, root, "challenge")
			if len(proofRun.Proofs) != 1 || proofRun.Proofs[0].Status != tc.want {
				t.Fatalf("expected %s, got %+v", tc.want, proofRun)
			}
			p := proofRun.Proofs[0]
			check(p.Baseline, domain.Pass, 0)
			if tc.want == domain.Proven {
				check(p.Challenged, domain.Fail, 1)
				if proofRun.Decision != domain.Accept || p.Mutation == nil || p.Mutation.Path != "src/main.js" {
					t.Fatalf("invalid proof: %+v", proofRun)
				}
			} else {
				check(p.Challenged, domain.Pass, 0)
				if proofRun.Decision != domain.Reject || p.Mutation == nil {
					t.Fatalf("invalid negative control: %+v", proofRun)
				}
			}
			t.Logf("%s with matching baseline and challenged evidence", p.Status)
		})
	}
}
