package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

const proofPolicy = "no-bad"

func hashOf(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func ip(n int) *int { return &n }

// proofSource creates a source checkout holding the target and a dirty,
// read-only extra, and returns a snapshot function used to prove it unchanged.
func proofSource(t *testing.T) (root string, snapshot func() map[string]workspace.Entry) {
	t.Helper()
	root = t.TempDir()
	for name, body := range map[string]string{"src/a.txt": "SAFE\n", "src/dirty.txt": "uncommitted edit\n", "untracked.log": "scratch\n"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "untracked.log"), 0o444); err != nil {
		t.Fatal(err)
	}
	return root, func() map[string]workspace.Entry {
		s, err := workspace.Snapshot(root)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

var strongExpect = expectation{Policy: proofPolicy, Rule: proofPolicy}

func challengeFor(file string) Challenge {
	return Challenge{ID: "proof", Guard: "g", File: file, Before: "SAFE", After: "BAD"}
}

// contentEvaluator inspects the target file in whatever root it is given: it
// reports one finding when the target contains BAD, like a real analyzer.
type contentEvaluator struct {
	roots []string
	// override, when set, replaces the check for call n (1-based).
	override func(call int, root string, c domain.Check) domain.Check
}

func (e *contentEvaluator) eval(_ context.Context, root string) domain.Check {
	e.roots = append(e.roots, root)
	c := domain.Check{ID: "guard", PolicyID: proofPolicy, ProviderID: "test", Status: domain.Pass, Inspected: ip(3), ExitCode: ip(0)}
	if b, err := os.ReadFile(filepath.Join(root, "src", "a.txt")); err == nil && strings.Contains(string(b), "BAD") {
		c.Status, c.ExitCode = domain.Fail, ip(1)
		c.Findings = []domain.Finding{{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "src/a.txt"}}
	}
	if e.override != nil {
		c = e.override(len(e.roots), root, c)
	}
	return c
}

type cleanupLog struct {
	calls int
	err   error
	// keep skips the real removal, simulating a cleanup that claims success
	// without removing anything.
	keep bool
	root string
}

func (c *cleanupLog) fn(w *workspace.Workspace) error {
	c.calls++
	c.root = w.Root
	if c.keep {
		return c.err
	}
	rerr := w.Close()
	if c.err != nil {
		return c.err
	}
	return rerr
}

func stageNames(p domain.Proof) string {
	var n []string
	for _, s := range p.Stages {
		if s.Status != domain.StageOK {
			n = append(n, s.Name+"!")
		} else {
			n = append(n, s.Name)
		}
	}
	return strings.Join(n, ",")
}

func TestProofLifecycle(t *testing.T) {
	type tc struct {
		name     string
		override func(call int, root string, c domain.Check) domain.Check
		cleanup  error
		keep     bool
		want     domain.ProofStatus
		stages   string
		mutated  bool
		detail   string
	}
	other := func(f domain.Finding) func(int, string, domain.Check) domain.Check {
		return func(call int, _ string, c domain.Check) domain.Check {
			if call == 2 {
				c.Findings = []domain.Finding{f}
			}
			return c
		}
	}
	at := func(call int, f func(domain.Check) domain.Check) func(int, string, domain.Check) domain.Check {
		return func(n int, _ string, c domain.Check) domain.Check {
			if n == call {
				return f(c)
			}
			return c
		}
	}
	all := "prepare,baseline,mutate,challenged,cleanup"
	cases := []tc{
		{name: "exact rule and path is PROVEN", want: domain.Proven, stages: all, mutated: true, detail: "expected policy and rule"},
		{name: "surviving guard is UNPROVEN", override: at(2, func(c domain.Check) domain.Check {
			c.Status, c.ExitCode, c.Findings = domain.Pass, ip(0), nil
			return c
		}), want: domain.Unproven, stages: all, mutated: true, detail: "passed after mutation"},
		{name: "disabled guard always passing is UNPROVEN", override: func(_ int, _ string, c domain.Check) domain.Check {
			c.Status, c.ExitCode, c.Findings = domain.Pass, ip(0), nil
			return c
		}, want: domain.Unproven, stages: all, mutated: true},
		{name: "failing baseline", override: at(1, func(c domain.Check) domain.Check {
			c.Status, c.ExitCode = domain.Fail, ip(1)
			c.Findings = []domain.Finding{{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "elsewhere.txt"}}
			return c
		}), want: domain.Inconclusive, stages: "prepare,baseline!,cleanup", detail: "baseline guard did not pass"},
		{name: "wrong rule", override: other(domain.Finding{PolicyID: proofPolicy, RuleID: "other-rule", Path: "src/a.txt"}), want: domain.Inconclusive, stages: all, mutated: true, detail: "expected policy, rule"},
		{name: "wrong policy", override: other(domain.Finding{PolicyID: "q", RuleID: proofPolicy, Path: "src/a.txt"}), want: domain.Inconclusive, stages: all, mutated: true},
		{name: "wrong path", override: other(domain.Finding{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "src/dirty.txt"}), want: domain.Inconclusive, stages: all, mutated: true},
		{name: "duplicate findings", override: func(call int, _ string, c domain.Check) domain.Check {
			if call == 2 {
				f := domain.Finding{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "src/a.txt"}
				c.Findings = []domain.Finding{f, f}
			}
			return c
		}, want: domain.Inconclusive, stages: all, mutated: true, detail: "got 2"},
		{name: "failure without findings", override: at(2, func(c domain.Check) domain.Check { c.Findings = nil; return c }), want: domain.Inconclusive, stages: all, mutated: true, detail: "exactly one finding"},
		{name: "zero targets in baseline", override: at(1, func(c domain.Check) domain.Check { c.Inspected = ip(0); return c }), want: domain.Inconclusive, stages: "prepare,baseline!,cleanup", detail: "no targets"},
		{name: "unknown targets in baseline", override: at(1, func(c domain.Check) domain.Check { c.Inspected = nil; return c }), want: domain.Inconclusive, stages: "prepare,baseline!,cleanup"},
		{name: "zero targets in passing challenged check", override: at(2, func(c domain.Check) domain.Check {
			c.Status, c.ExitCode, c.Findings, c.Inspected = domain.Pass, ip(0), nil, ip(0)
			return c
		}), want: domain.Inconclusive, stages: all, mutated: true, detail: "no targets"},
		{name: "tool crash at baseline", override: at(1, func(c domain.Check) domain.Check {
			return domain.Check{ID: c.ID, Status: domain.Refuse, Diagnostics: []domain.Diagnostic{{Code: "CRASH", Message: "boom"}}}
		}), want: domain.Inconclusive, stages: "prepare,baseline!,cleanup"},
		{name: "tool crash after mutation", override: at(2, func(c domain.Check) domain.Check {
			return domain.Check{ID: c.ID, Status: domain.Refuse}
		}), want: domain.Inconclusive, stages: all, mutated: true, detail: "could not execute"},
		{name: "cleanup error prevents PROVEN", cleanup: errors.New("disk on fire"), want: domain.Inconclusive, stages: "prepare,baseline,mutate,challenged,cleanup!", mutated: true, detail: "not certified"},
		{name: "cleanup that removes nothing prevents PROVEN", keep: true, want: domain.Inconclusive, stages: "prepare,baseline,mutate,challenged,cleanup!", mutated: true, detail: "still exists"},
		{name: "cleanup error after UNPROVEN stays not proven", override: at(2, func(c domain.Check) domain.Check {
			c.Status, c.ExitCode, c.Findings = domain.Pass, ip(0), nil
			return c
		}), cleanup: errors.New("busy"), want: domain.Inconclusive, stages: "prepare,baseline,mutate,challenged,cleanup!", mutated: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source, snap := proofSource(t)
			before := snap()
			sourceBytes, _ := os.ReadFile(filepath.Join(source, "src", "a.txt"))
			ev := &contentEvaluator{override: c.override}
			cl := &cleanupLog{err: c.cleanup, keep: c.keep}
			var prepared []string
			prepare := func(s string) (*workspace.Workspace, error) {
				w, err := workspace.Prepare(s)
				if w != nil {
					prepared = append(prepared, w.Root)
				}
				return w, err
			}
			got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, ev.eval, prepare, cl.fn)
			if c.keep && cl.root != "" {
				defer os.RemoveAll(cl.root)
			}

			if got.Status != c.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Detail, c.want)
			}
			if !strings.Contains(got.Detail, c.detail) {
				t.Errorf("detail %q lacks %q", got.Detail, c.detail)
			}
			if s := stageNames(got); s != c.stages {
				t.Errorf("stages = %s, want %s", s, c.stages)
			}
			for _, s := range got.Stages {
				if s.DurationMs < 0 || s.Name == "" {
					t.Errorf("stage without timing: %+v", s)
				}
				if s.Status == domain.StageFailed && s.Detail == "" {
					t.Errorf("failed stage %q has no detail", s.Name)
				}
			}
			if (got.Mutation != nil) != c.mutated {
				t.Errorf("mutation recorded = %v, want %v", got.Mutation != nil, c.mutated)
			}
			if got.Mutation != nil {
				want := domain.Mutation{Path: "src/a.txt", BeforeSHA256: hashOf("SAFE\n"), AfterSHA256: hashOf("BAD\n")}
				if *got.Mutation != want {
					t.Errorf("mutation = %+v, want %+v", *got.Mutation, want)
				}
				if got.FaultID == "" {
					t.Error("fault ID not derived")
				}
			} else if got.FaultID != "" {
				t.Error("fault ID without a mutation")
			}

			// Safety invariants from the plan.
			if got.Status == domain.Proven && cl.err != nil {
				t.Fatal("certified failed cleanup")
			}
			if after, _ := os.ReadFile(filepath.Join(source, "src", "a.txt")); string(sourceBytes) != string(after) {
				t.Fatal("proof changed user checkout")
			}
			if d := workspace.Diff(before, snap()); len(d) != 0 {
				t.Fatalf("source checkout changed: %v", d)
			}
			if len(ev.roots) > 0 {
				baselineRoot, challengedRoot := ev.roots[0], ev.roots[len(ev.roots)-1]
				if baselineRoot == source || (len(ev.roots) > 1 && baselineRoot != challengedRoot) {
					t.Fatal("unsafe proof workspace")
				}
			}
			if len(prepared) != 1 || cl.calls != 1 {
				t.Fatalf("prepared %d workspaces, cleaned %d", len(prepared), cl.calls)
			}
			if !c.keep {
				if _, err := os.Lstat(prepared[0]); err == nil {
					t.Fatal("disposable copy left behind")
				}
			}
			if got.Baseline == nil || got.Baseline.ID != "proof:baseline" {
				t.Errorf("baseline not recorded: %+v", got.Baseline)
			}
		})
	}
}

func TestProofSameEvaluatorAndScope(t *testing.T) {
	source, _ := proofSource(t)
	var calls int
	var roots []string
	eval := func(_ context.Context, root string) domain.Check {
		calls++
		roots = append(roots, root)
		c := domain.Check{PolicyID: proofPolicy, Status: domain.Pass, Inspected: ip(1), ExitCode: ip(0)}
		if b, _ := os.ReadFile(filepath.Join(root, "src", "a.txt")); strings.Contains(string(b), "BAD") {
			c.Status, c.ExitCode = domain.Fail, ip(1)
			c.Findings = []domain.Finding{{PolicyID: proofPolicy, RuleID: proofPolicy, Path: "src/a.txt"}}
		}
		return c
	}
	got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, eval, workspace.Prepare, (*workspace.Workspace).Close)
	if got.Status != domain.Proven || calls != 2 || roots[0] != roots[1] || roots[0] == source {
		t.Fatalf("status %s, calls %d, roots %v", got.Status, calls, roots)
	}
}

func TestProofPrepareFailures(t *testing.T) {
	source, snap := proofSource(t)
	before := snap()
	boom := errors.New("no space")
	for name, prepare := range map[string]func(string) (*workspace.Workspace, error){
		"error":         func(string) (*workspace.Workspace, error) { return nil, boom },
		"nil":           func(string) (*workspace.Workspace, error) { return nil, nil },
		"empty root":    func(string) (*workspace.Workspace, error) { return &workspace.Workspace{}, nil },
		"source itself": func(s string) (*workspace.Workspace, error) { return &workspace.Workspace{Root: s}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			cleaned := 0
			ev := &contentEvaluator{}
			got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, ev.eval, prepare,
				func(*workspace.Workspace) error { cleaned++; return nil })
			if got.Status != domain.Inconclusive || stageNames(got) != "prepare!" || len(ev.roots) != 0 {
				t.Fatalf("status %s stages %s evaluated %d", got.Status, stageNames(got), len(ev.roots))
			}
			if cleaned != 0 {
				t.Fatal("cleanup was invoked for a workspace that was never created (or for the source)")
			}
			if d := workspace.Diff(before, snap()); len(d) != 0 {
				t.Fatalf("source changed: %v", d)
			}
		})
	}
}

func TestProofContextEndedBeforeBaseline(t *testing.T) {
	source, _ := proofSource(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ev := &contentEvaluator{}
	cl := &cleanupLog{}
	got := prove(ctx, source, challengeFor("src/a.txt"), strongExpect, ev.eval, workspace.Prepare, cl.fn)
	if got.Status != domain.Inconclusive || len(ev.roots) != 0 || cl.calls != 1 || !strings.Contains(got.Detail, "context ended") {
		t.Fatalf("%+v evaluated %d cleaned %d", got, len(ev.roots), cl.calls)
	}
}

func TestProofContextEndsDuringBaseline(t *testing.T) {
	source, snap := proofSource(t)
	before := snap()
	ctx, cancel := context.WithCancel(context.Background())
	ev := &contentEvaluator{override: func(call int, _ string, c domain.Check) domain.Check { cancel(); return c }}
	cl := &cleanupLog{}
	got := prove(ctx, source, challengeFor("src/a.txt"), strongExpect, ev.eval, workspace.Prepare, cl.fn)
	if got.Status != domain.Inconclusive || len(ev.roots) != 1 || cl.calls != 1 || !strings.Contains(got.Detail, "before the challenged check") {
		t.Fatalf("status %s evaluated %d cleaned %d detail %q", got.Status, len(ev.roots), cl.calls, got.Detail)
	}
	if d := workspace.Diff(before, snap()); len(d) != 0 {
		t.Fatalf("source changed: %v", d)
	}
}

func TestProofMutationRefusals(t *testing.T) {
	source, snap := proofSource(t)
	if err := os.WriteFile(filepath.Join(source, "dup.txt"), []byte("SAFE SAFE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ro.txt"), []byte("SAFE"), 0o444); err != nil {
		t.Fatal(err)
	}
	before := snap()
	for name, file := range map[string]string{
		"ambiguous marker": "dup.txt", "absent marker": "src/dirty.txt", "unwritable target": "ro.txt", "missing target": "nope.txt",
	} {
		t.Run(name, func(t *testing.T) {
			ev := &contentEvaluator{}
			cl := &cleanupLog{}
			got := prove(context.Background(), source, challengeFor(file), strongExpect, ev.eval, workspace.Prepare, cl.fn)
			if got.Status != domain.Inconclusive || stageNames(got) != "prepare,baseline,mutate!,cleanup" || got.Mutation != nil {
				t.Fatalf("status %s stages %s mutation %v", got.Status, stageNames(got), got.Mutation)
			}
			if len(ev.roots) != 1 {
				t.Fatalf("the guard ran %d times; no challenged run is allowed after a refused mutation", len(ev.roots))
			}
			if d := workspace.Diff(before, snap()); len(d) != 0 {
				t.Fatalf("source changed: %v", d)
			}
		})
	}
}

// A baseline that swaps the target for a symlink to a file outside the
// workspace must not let the mutation write through it.
func TestProofSymlinkSwapAfterBaseline(t *testing.T) {
	source, snap := proofSource(t)
	before := snap()
	sentinel := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(sentinel, []byte("SAFE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, swap := range map[string]func(target string) error{
		"file symlink": func(target string) error {
			if err := os.Remove(target); err != nil {
				return err
			}
			return os.Symlink(sentinel, target)
		},
		"directory symlink": func(target string) error {
			dir := filepath.Dir(target)
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			ext := filepath.Dir(sentinel)
			if err := os.WriteFile(filepath.Join(ext, "a.txt"), []byte("SAFE\n"), 0o644); err != nil {
				return err
			}
			return os.Symlink(ext, dir)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ev := &contentEvaluator{override: func(call int, root string, c domain.Check) domain.Check {
				if call == 1 {
					if err := swap(filepath.Join(root, "src", "a.txt")); err != nil {
						t.Error(err)
					}
				}
				return c
			}}
			got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, ev.eval, workspace.Prepare, (*workspace.Workspace).Close)
			if got.Status != domain.Inconclusive || got.Mutation != nil || !strings.Contains(stageNames(got), "mutate!") {
				t.Fatalf("status %s stages %s detail %s", got.Status, stageNames(got), got.Detail)
			}
			if !strings.Contains(got.Detail, "redirected") {
				t.Errorf("detail %q should name the redirection", got.Detail)
			}
			for _, p := range []string{sentinel, filepath.Join(filepath.Dir(sentinel), "a.txt")} {
				if b, err := os.ReadFile(p); err == nil && string(b) != "SAFE\n" {
					t.Fatalf("external sentinel %s was modified: %q", p, b)
				}
			}
			if d := workspace.Diff(before, snap()); len(d) != 0 {
				t.Fatalf("source changed: %v", d)
			}
		})
	}
}

func TestProofCopyLimits(t *testing.T) {
	for name, build := range map[string]func(root string) error{
		"symlink in source": func(root string) error { return os.Symlink("src/a.txt", filepath.Join(root, "link")) },
		"oversized file": func(root string) error {
			return os.WriteFile(filepath.Join(root, "big"), []byte(strings.Repeat("x", workspace.MaxFileBytes+1)), 0o644)
		},
		"too many entries": func(root string) error {
			for i := 0; i <= workspace.MaxEntries; i++ {
				if err := os.WriteFile(filepath.Join(root, "f"+hex.EncodeToString([]byte{byte(i >> 8), byte(i)})), nil, 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, _ := proofSource(t)
			if err := build(source); err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ev := &contentEvaluator{}
			cl := &cleanupLog{}
			got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, ev.eval, workspace.Prepare, cl.fn)
			if got.Status != domain.Inconclusive || stageNames(got) != "prepare!" || len(ev.roots) != 0 {
				t.Fatalf("status %s stages %s", got.Status, stageNames(got))
			}
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Fatalf("failed copy left %d temp entries", len(left))
			}
		})
	}
}

func TestProofStageTiming(t *testing.T) {
	source, _ := proofSource(t)
	ev := &contentEvaluator{override: func(call int, _ string, c domain.Check) domain.Check {
		time.Sleep(25 * time.Millisecond)
		return c
	}}
	got := prove(context.Background(), source, challengeFor("src/a.txt"), strongExpect, ev.eval, workspace.Prepare, (*workspace.Workspace).Close)
	for _, s := range got.Stages {
		if (s.Name == domain.StageBaseline || s.Name == domain.StageChallenged) && s.DurationMs < 20 {
			t.Errorf("stage %s recorded %dms for a 25ms evaluation", s.Name, s.DurationMs)
		}
	}
}

func TestProofWeakMarker(t *testing.T) {
	source, _ := proofSource(t)
	marker := func(want bool) expectation { return expectation{Marker: func(domain.Check) bool { return want }} }
	for name, tc := range map[string]struct {
		expect expectation
		want   domain.ProofStatus
	}{
		"marker seen":    {marker(true), domain.Proven},
		"marker missing": {marker(false), domain.Unproven},
		"no marker func": {expectation{}, domain.Unproven},
	} {
		t.Run(name, func(t *testing.T) {
			eval := func(_ context.Context, root string) domain.Check {
				c := domain.Check{Status: domain.Pass, ExitCode: ip(0)}
				if b, _ := os.ReadFile(filepath.Join(root, "src", "a.txt")); strings.Contains(string(b), "BAD") {
					c.Status, c.ExitCode = domain.Fail, ip(1)
				}
				return c
			}
			got := prove(context.Background(), source, challengeFor("src/a.txt"), tc.expect, eval, workspace.Prepare, (*workspace.Workspace).Close)
			if got.Status != tc.want || got.EvidenceStrength != domain.StrengthWeak {
				t.Fatalf("status %s strength %s", got.Status, got.EvidenceStrength)
			}
		})
	}
}

// End to end through Run and RunEvidence with real guard processes.

func realProofSpec(t *testing.T, root, script string) Specification {
	t.Helper()
	return Specification{Version: "invaris.dev/v1alpha1", Name: "p",
		Guards:     []Command{{ID: "g", Program: "sh", Args: []string{"-c", script}}},
		Challenges: []Challenge{{ID: "c", Guard: "g", File: "source.txt", Before: "SAFE", After: "BAD", ExpectedOutput: "BAD"}}}
}

func TestChallengeSymlinkSwapEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("SAFE"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(sentinel, []byte("SAFE"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The baseline command, run in the copy, redirects the target outside it.
	spec := realProofSpec(t, dir, "rm source.txt && ln -s '"+sentinel+"' source.txt; if grep -q BAD source.txt; then echo BAD; exit 1; fi")
	got := Run(context.Background(), spec, dir, "challenge")
	if got.Status != "INCONCLUSIVE" || got.Results[0].Status != "INCONCLUSIVE" || !strings.Contains(got.Results[0].Detail, "redirected") {
		t.Fatalf("%+v", got)
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "SAFE" {
		t.Fatalf("external sentinel modified: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "source.txt")); string(b) != "SAFE" {
		t.Fatalf("source modified: %q", b)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("workspace left in TMPDIR: %v", left)
	}
}

func TestChallengeEvidenceRecordsMutationAndStages(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	spec, err := Load("../../examples/structured/invaris.json")
	if err != nil {
		t.Fatal(err)
	}
	spec.Gate = &domain.Gate{RequiredProofs: []string{"detect-marker"}, Precedence: domain.FailureFirst}
	root := "../../examples/structured"
	data, err := os.ReadFile(filepath.Join(root, "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	r := RunEvidence(context.Background(), spec, root, "challenge")
	if r.Decision != domain.Accept || len(r.Proofs) != 1 {
		t.Fatalf("%s %+v", r.Decision, r.Diagnostics)
	}
	p := r.Proofs[0]
	ch := spec.Challenges[0]
	mutated := strings.Replace(string(data), ch.Before, ch.After, 1)
	wantMutation := domain.Mutation{Path: ch.File, BeforeSHA256: hashOf(string(data)), AfterSHA256: hashOf(mutated)}
	if p.Mutation == nil || *p.Mutation != wantMutation {
		t.Fatalf("mutation = %+v, want %+v", p.Mutation, wantMutation)
	}
	if p.FaultID != faultID(p.PolicyID, workspace.Mutation{Path: ch.File, BeforeSHA256: wantMutation.BeforeSHA256, AfterSHA256: wantMutation.AfterSHA256}) {
		t.Errorf("fault ID %q is not derived from policy, target and digests", p.FaultID)
	}
	if got := stageNames(p); got != "prepare,baseline,mutate,challenged,cleanup" {
		t.Errorf("stages = %s", got)
	}
	if p.Challenged == nil || len(p.Challenged.Findings) != 1 || p.Challenged.Findings[0].Path != ch.File {
		t.Errorf("finding not linked to the mutated target: %+v", p.Challenged)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("workspace left in TMPDIR: %v", left)
	}
}

// A report claiming PROVEN for a proof whose cleanup failed must not be ACCEPT,
// even if the engine-level status were wrong: the evidence layer re-checks it.
func TestFailedCleanupCannotBeAcceptedInEvidence(t *testing.T) {
	spec, err := Load("../../examples/structured/invaris.json")
	if err != nil {
		t.Fatal(err)
	}
	spec.Gate = &domain.Gate{RequiredProofs: []string{"detect-marker"}, Precedence: domain.FailureFirst}
	root := "../../examples/structured"
	legacy := Run(context.Background(), spec, root, "challenge")
	if legacy.Results[0].Status != "PROVEN" {
		t.Fatalf("setup: %+v", legacy.Results[0])
	}
	proof := *legacy.Results[0].Proof
	stages := append([]domain.Stage{}, proof.Stages...)
	stages[len(stages)-1] = domain.Stage{Name: domain.StageCleanup, Status: domain.StageFailed, Detail: "injected"}
	proof.Stages = stages
	legacy.Results[0].Proof = &proof // status string still says PROVEN
	r := buildEvidence(context.Background(), spec, root, "challenge", legacy)
	if r.Decision == domain.Accept {
		t.Fatal("PROVEN with a failed cleanup stage was accepted")
	}
}
