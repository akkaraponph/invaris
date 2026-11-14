package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckAndProof(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("SAFE_IMPORT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "unit", Guards: []Command{{ID: "guard", Program: "sh", Args: []string{"-c", "if grep -q FORBIDDEN_IMPORT source.txt; then echo FORBIDDEN_IMPORT; exit 1; fi"}}}, Challenges: []Challenge{{ID: "proof", Guard: "guard", File: "source.txt", Before: "SAFE_IMPORT", After: "FORBIDDEN_IMPORT", ExpectedOutput: "FORBIDDEN_IMPORT"}}}
	if got := Run(context.Background(), spec, dir, "check"); got.Status != "PASS" {
		t.Fatalf("check: %+v", got)
	}
	if got := Run(context.Background(), spec, dir, "challenge"); got.Status != "PASS" || len(got.Results) != 1 || got.Results[0].Status != "PROVEN" {
		t.Fatalf("proof: %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "source.txt"))
	if err != nil || string(data) != "SAFE_IMPORT\n" {
		t.Fatalf("original changed: %q %v", data, err)
	}
}
func TestChallengeMustDetectExpectedViolation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "source.txt"), []byte("SAFE_IMPORT"), 0600)
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "unit", Guards: []Command{{ID: "guard", Program: "sh", Args: []string{"-c", "test ! -f nonexistent; exit 0"}}}, Challenges: []Challenge{{ID: "proof", Guard: "guard", File: "source.txt", Before: "SAFE_IMPORT", After: "FORBIDDEN_IMPORT", ExpectedOutput: "FORBIDDEN_IMPORT"}}}
	got := Run(context.Background(), spec, dir, "challenge")
	if got.Status != "FAIL" || got.Results[0].Status != "UNPROVEN" {
		t.Fatalf("false proof: %+v", got)
	}
}
func TestLoadRejectsInvalidContracts(t *testing.T) {
	for _, c := range []string{
		`{"version":"bad","name":"demo"}`,
		`{"version":"invaris.dev/v1alpha1","name":"demo","steps":[{"id":"a","program":"true"},{"id":"a","program":"true"}]}`,
		`{"version":"invaris.dev/v1alpha1","name":"demo","guards":[{"id":"a","program":"true"}],"challenges":[{"id":"x","guard":"a","file":"../secret","before":"x","after":"y","expectedOutput":"z"}]}`,
		`{"version":"invaris.dev/v1alpha1","name":"demo","extra":true}`,
	} {
		file := filepath.Join(t.TempDir(), "contract.json")
		os.WriteFile(file, []byte(c), 0600)
		if _, err := Load(file); err == nil {
			t.Errorf("accepted invalid contract: %s", c)
		}
	}
}
func TestExecuteCannotInventPass(t *testing.T) {
	result := execute(context.Background(), t.TempDir(), Command{ID: "missing", Program: "missing-invaris-test-executable-0101"})
	if result.Status != "REFUSE" {
		t.Fatalf("missing executable incorrectly passed: %+v", result)
	}
	if report := Run(context.Background(), Specification{Version: "invaris.dev/v1alpha1", Name: "unit"}, t.TempDir(), "check"); report.Status != "REFUSE" {
		t.Fatalf("empty check incorrectly passed")
	}
}
func TestPathGuard(t *testing.T) {
	for _, p := range []string{"../secret", "/tmp/secret", ""} {
		if err := validatePath(p); err == nil {
			t.Fatal("accepted " + p)
		}
	}
	if err := validatePath("src/module.go"); err != nil {
		t.Fatal(err)
	}
}
func TestProofRefusesAmbiguousMutation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "source.txt"), []byte("SAFE_IMPORT SAFE_IMPORT"), 0600)
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "unit", Guards: []Command{{ID: "g", Program: "true"}}, Challenges: []Challenge{{ID: "c", Guard: "g", File: "source.txt", Before: "SAFE_IMPORT", After: "BAD", ExpectedOutput: "BAD"}}}
	got := Run(context.Background(), spec, dir, "challenge")
	if got.Status != "INCONCLUSIVE" || !strings.Contains(got.Results[0].Detail, "exactly once") {
		t.Fatalf("expected inconclusive: %+v", got)
	}
}

func TestRunAlwaysChecksGuardsAndPreservesFailure(t *testing.T) {
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "order", Steps: []Command{{ID: "step", Program: "sh", Args: []string{"-c", "exit 1"}}, {ID: "skipped", Program: "true"}}, Guards: []Command{{ID: "missing", Program: "missing-invaris-test-executable-0101"}, {ID: "guard", Program: "true"}}}
	got := Run(context.Background(), spec, t.TempDir(), "run")
	if got.Status != "FAIL" || len(got.Results) != 4 || got.Results[0].Status != "FAIL" || got.Results[1].Status != "NOT_RUN" || got.Results[2].Status != "REFUSE" || got.Results[3].ID != "guard" {
		t.Fatalf("lost failure or guard: %+v", got)
	}
}

func TestChallengeBaselineRunsInCopy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "copy", Guards: []Command{{ID: "g", Program: "sh", Args: []string{"-c", "echo side-effect > baseline-side-effect; if grep -q bad source; then echo marker; exit 1; fi"}}}, Challenges: []Challenge{{ID: "c", Guard: "g", File: "source", Before: "safe", After: "bad", ExpectedOutput: "marker"}}}
	got := Run(context.Background(), spec, dir, "challenge")
	if got.Status != "PASS" || !strings.Contains(got.Results[0].Detail, "weak evidence") {
		t.Fatalf("unexpected report: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "baseline-side-effect")); !os.IsNotExist(err) {
		t.Fatalf("baseline wrote to source: %v", err)
	}
}

func TestChallengeFailurePrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "precedence", Guards: []Command{{ID: "g", Program: "true"}}, Challenges: []Challenge{{ID: "survives", Guard: "g", File: "source", Before: "safe", After: "bad", ExpectedOutput: "marker"}, {ID: "invalid", Guard: "g", File: "missing", Before: "safe", After: "bad", ExpectedOutput: "marker"}}}
	got := Run(context.Background(), spec, dir, "challenge")
	if got.Status != "FAIL" || got.Results[0].Status != "UNPROVEN" || got.Results[1].Status != "INCONCLUSIVE" {
		t.Fatalf("lost outcome: %+v", got)
	}
}

func TestChallengeRejectsEscapeWithoutLoad(t *testing.T) {
	got := Run(context.Background(), Specification{Version: "invaris.dev/v1alpha1", Name: "escape", Guards: []Command{{ID: "g", Program: "true"}}, Challenges: []Challenge{{ID: "c", Guard: "g", File: "../outside", Before: "a", After: "b", ExpectedOutput: "b"}}}, t.TempDir(), "challenge")
	if got.Status != "REFUSE" || !strings.Contains(got.Results[0].Detail, "unsafe") {
		t.Fatalf("accepted escape: %+v", got)
	}
}

func TestCommandEvidenceLimits(t *testing.T) {
	t.Setenv("INVARIS_TEST_PRIVATE", "private")
	root := t.TempDir()
	for _, tc := range []struct {
		name, script, status, detail string
		allow                        []string
	}{
		{"default environment", `test -z "$INVARIS_TEST_PRIVATE"`, "PASS", "", nil},
		{"allowed environment", `test "$INVARIS_TEST_PRIVATE" = private`, "PASS", "", []string{"INVARIS_TEST_PRIVATE"}},
		{"excess output", `yes sample | head -c 70000`, "REFUSE", "output limit", nil},
		{"timeout", `sleep 30 & wait`, "REFUSE", "deadline exceeded", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			got := execute(context.Background(), root, Command{ID: "test", Program: "sh", Args: []string{"-c", tc.script}, EnvAllowlist: tc.allow, TimeoutSeconds: 1})
			if got.Status != tc.status || !strings.Contains(got.Detail, tc.detail) || len(got.Output) > 65536 || time.Since(started) > 5*time.Second {
				t.Fatalf("unexpected command result: status=%s detail=%q bytes=%d", got.Status, got.Detail, len(got.Output))
			}
		})
	}
}
