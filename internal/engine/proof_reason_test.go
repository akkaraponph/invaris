package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// Every proof outcome carries a machine-readable reason and the category that
// separates a bad experiment from an infrastructure or evidence failure.
func TestProofReasons(t *testing.T) {
	refuse := func(reason string) func(int, string, domain.Check) domain.Check {
		return func(call int, _ string, c domain.Check) domain.Check {
			return domain.Check{ID: "guard", PolicyID: proofPolicy, Status: domain.Refuse, Reason: reason, Inspected: c.Inspected}
		}
	}
	atCall := func(n int, f func(int, string, domain.Check) domain.Check) func(int, string, domain.Check) domain.Check {
		return func(call int, root string, c domain.Check) domain.Check {
			if call == n {
				return f(call, root, c)
			}
			return c
		}
	}
	for _, tc := range []struct {
		name     string
		override func(int, string, domain.Check) domain.Check
		file     string
		cleanup  error
		want     domain.ProofStatus
		reason   string
		category string
	}{
		{name: "detected", want: domain.Proven, reason: "guard_detected", category: "result"},
		{name: "guard passed", override: atCall(2, func(_ int, _ string, c domain.Check) domain.Check {
			c.Status, c.Findings, c.ExitCode = domain.Pass, nil, ip(0)
			return c
		}), want: domain.Unproven, reason: "guard_passed", category: "result"},
		{name: "wrong path", override: atCall(2, func(_ int, _ string, c domain.Check) domain.Check {
			c.Findings = []domain.Finding{{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "src/dirty.txt"}}
			return c
		}), want: domain.Inconclusive, reason: "finding_mismatch", category: "setup"},
		{name: "baseline finds a violation", override: atCall(1, func(_ int, _ string, c domain.Check) domain.Check {
			c.Status, c.ExitCode = domain.Fail, ip(1)
			return c
		}), want: domain.Inconclusive, reason: "unhealthy_baseline", category: "setup"},
		{name: "baseline inspects nothing", override: atCall(1, func(_ int, _ string, c domain.Check) domain.Check {
			c.Inspected = ip(0)
			return c
		}), want: domain.Inconclusive, reason: "no_targets_inspected", category: "setup"},
		{name: "baseline timeout", override: atCall(1, refuse("timeout")), want: domain.Inconclusive, reason: "timeout", category: "infrastructure"},
		{name: "challenged malformed", override: atCall(2, refuse("malformed_evidence")), want: domain.Inconclusive, reason: "malformed_evidence", category: "evidence"},
		{name: "marker absent", file: "src/missing.txt", want: domain.Inconclusive, reason: "mutation_rejected", category: "setup"},
		{name: "guard rewrites a file", override: atCall(2, func(_ int, root string, c domain.Check) domain.Check {
			_ = os.WriteFile(filepath.Join(root, "src", "dirty.txt"), []byte("changed"), 0o644)
			return c
		}), want: domain.Inconclusive, reason: "workspace_changed", category: "evidence"},
		{name: "cleanup fails", cleanup: errors.New("busy"), want: domain.Inconclusive, reason: "cleanup_failed", category: "infrastructure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _ := proofSource(t)
			ev := &contentEvaluator{override: tc.override}
			file := tc.file
			if file == "" {
				file = "src/a.txt"
			}
			log := &cleanupLog{err: tc.cleanup}
			got := prove(context.Background(), source, challengeFor(file), strongExpect, ev.eval, workspace.Prepare, log.fn)
			if got.Status != tc.want || got.Reason != tc.reason || got.ReasonCategory != tc.category {
				t.Fatalf("got %s/%s/%s (%s), want %s/%s/%s", got.Status, got.Reason, got.ReasonCategory, got.Detail, tc.want, tc.reason, tc.category)
			}
			if got.SourceSnapshot == nil || got.SourceSnapshot.Files != 4 && tc.name != "marker absent" {
				t.Fatalf("source snapshot not recorded: %+v", got.SourceSnapshot)
			}
			if tc.cleanup != nil {
				_ = os.RemoveAll(log.root)
			}
		})
	}
}

func TestProofPrepareFailureReason(t *testing.T) {
	source, _ := proofSource(t)
	got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, (&contentEvaluator{}).eval,
		func(string) (*workspace.Workspace, error) { return nil, errors.New("disk full") }, (*workspace.Workspace).Close)
	if got.Reason != "workspace_prepare_failed" || got.ReasonCategory != "infrastructure" || got.SourceSnapshot != nil {
		t.Fatalf("%+v", got)
	}
}

// The snapshot digest identifies copied inputs, including dirty files that a
// Git revision cannot: changing an untracked file changes it, and an identical
// tree gives the same digest.
func TestSourceSnapshotIdentifiesDirtyInputs(t *testing.T) {
	digest := func(source string) string {
		got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, (&contentEvaluator{}).eval, workspace.Prepare, (*workspace.Workspace).Close)
		if got.SourceSnapshot == nil {
			t.Fatal("no snapshot")
		}
		return got.SourceSnapshot.SHA256
	}
	a, _ := proofSource(t)
	b, _ := proofSource(t)
	if digest(a) != digest(b) {
		t.Fatal("identical trees must have identical digests")
	}
	untracked := filepath.Join(b, "untracked.log")
	if err := os.Chmod(untracked, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("different"), 0o444); err != nil {
		t.Fatal(err)
	}
	if digest(a) == digest(b) {
		t.Fatal("an edited untracked file did not change the digest")
	}
}

// A challenge reports configuration failures with a reason too, and the report
// still validates.
func TestChallengeConfigurationReasons(t *testing.T) {
	dir := t.TempDir()
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "cfg", Guards: []Command{{ID: "g", Program: "true"}},
		Challenges: []Challenge{{ID: "c", Guard: "g", File: "source", Before: "a", After: "b", ExpectedOutput: "b"}}}
	got := Run(context.Background(), spec, dir, "challenge")
	p := toProof(got.Results[0], spec.Challenges[0], spec.Guards[0])
	if p.Reason != "mutation_rejected" || p.ReasonCategory != "setup" {
		t.Fatalf("missing target: %+v", p)
	}
	spec.Challenges[0].Guard = "absent"
	res := challenge(context.Background(), dir, spec, providerState{}, spec.Challenges[0])
	if res.reason != domain.ReasonGuardNotFound {
		t.Fatalf("reason = %q", res.reason)
	}
}

func TestReasonVocabulary(t *testing.T) {
	// Every reason the mutation lifecycle can emit has a category.
	for _, r := range []string{"workspace_prepare_failed", "unhealthy_baseline", "mutation_rejected", "workspace_changed",
		"malformed_evidence", "missing_executable", "output_limit", "execution_failed", "timeout", "canceled",
		"cleanup_failed", "assertion_failed", "all_tests_passed", "mutant_invalid"} {
		if domain.ReasonCategory(r) == "" {
			t.Errorf("reason %q has no category", r)
		}
	}
}
