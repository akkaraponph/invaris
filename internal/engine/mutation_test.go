package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/workspace"
)

func mutationTestProject(t *testing.T) (Specification, string) {
	t.Helper()
	spec, err := decodeSpecification([]byte(mutationConfig))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	spec.Experiment.TestIDs = []string{"case"}
	spec.Experiment.Test.Args = []string{"-c", emit(cleanTests)}
	spec.Experiment.Validation.Args = []string{"-c", emit(cleanValidity)}
	return spec, root
}

func emit(json string) string {
	return "printf '%s\\n' '" + json + "'"
}

func TestMutationLifecycleFailures(t *testing.T) {
	failTests := emit(strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED")) + "; exit 1"
	for _, tc := range []struct {
		name, reason                      string
		test, validation                  string
		missing, cancel, cleanup, timeout bool
		ambiguous, stale                  bool
		stage                             string
	}{
		{name: "unhealthy baseline", reason: "unhealthy_baseline", test: failTests, stage: "baseline"},
		{name: "missing executable", reason: "missing_executable", missing: true, stage: "baseline"},
		{name: "timeout", reason: "timeout", test: "sleep 5", timeout: true, stage: "baseline"},
		{name: "cancellation", reason: "canceled", test: "sleep 5", cancel: true, stage: "baseline"},
		{name: "malformed", reason: "malformed_evidence", test: "printf '{'", stage: "baseline"},
		{name: "truncated assertion", reason: "output_limit", test: "head -c 65537 /dev/zero >&2; " + failTests, stage: "baseline"},
		{name: "mismatch", reason: "malformed_evidence", test: emit(cleanTests) + "; exit 1", stage: "baseline"},
		{name: "infrastructure", reason: "execution_failed", test: failTests[:strings.LastIndex(failTests, "1")] + "2", stage: "baseline"},
		{name: "baseline changes manifest", reason: "workspace_changed", test: "printf x > side-effect; " + emit(cleanTests), stage: "baseline"},
		{name: "validation changes manifest", reason: "workspace_changed", validation: "printf x > src; " + emit(cleanValidity), stage: "validation"},
		{name: "mutant tests change manifest", reason: "workspace_changed", test: "if [ \"$(cat src)\" = new ]; then touch side-effect; fi; " + emit(cleanTests), stage: "tests"},
		{name: "ambiguous match", reason: "mutation_rejected", ambiguous: true, stage: "mutate"},
		{name: "stale preimage digest", reason: "mutation_rejected", stale: true, stage: "mutate"},
		{name: "cleanup failure", reason: "cleanup_failed", cleanup: true, stage: "cleanup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, root := mutationTestProject(t)
			original := "old"
			if tc.ambiguous {
				original = "old old"
				if err := os.WriteFile(filepath.Join(root, "src"), []byte(original), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if tc.stale {
				spec.Experiment.Mutation.PreimageSHA256 = strings.Repeat("0", 64)
			}
			if tc.test != "" {
				spec.Experiment.Test.Args[1] = tc.test
			}
			if tc.validation != "" {
				spec.Experiment.Validation.Args[1] = tc.validation
			}
			if tc.missing {
				spec.Experiment.Test.Program = filepath.Join(root, "absent")
			}
			if tc.timeout {
				spec.Experiment.Test.TimeoutSeconds = 1
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var copyRoot string
			e, obs := runMutation(ctx, spec, root, func(path string) (*workspace.Workspace, error) {
				w, err := workspace.Prepare(path)
				if err == nil {
					copyRoot = w.Root
					if tc.cancel {
						// Wait for the child to signal that the baseline started.
						spec.Experiment.Test.Args[1] = "touch started; " + spec.Experiment.Test.Args[1]
						go func(root string) {
							deadline := time.NewTimer(3 * time.Second)
							defer deadline.Stop()
							ticker := time.NewTicker(5 * time.Millisecond)
							defer ticker.Stop()
							for {
								select {
								case <-ctx.Done():
									return
								case <-deadline.C:
									return
								case <-ticker.C:
									if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
										cancel()
										return
									}
								}
							}
						}(w.Root)
					}
				}
				return w, err
			}, func(w *workspace.Workspace) error {
				if err := w.Close(); err != nil {
					return err
				}
				if tc.cleanup {
					return errors.New("simulated cleanup verification failure")
				}
				return nil
			})
			report := evidenceMetadata(ctx, spec, root, "mutate", time.Now(), time.Now())
			report.SchemaVersion = evidence.MutationSchemaVersion
			report.Gate.Precedence = domain.FailureFirst
			report.Decision, report.Experiment, report.Observations = domain.RefuseDecision, &e, obs
			if _, err := evidence.Marshal(report); err != nil {
				t.Fatalf("refusal could not be serialized: %v", err)
			}
			lastStage := len(e.Stages) - 1
			if tc.stage != "cleanup" {
				lastStage--
			}
			if e.ReasonCategory != domain.ReasonCategory(tc.reason) || e.ReasonCategory == "" {
				t.Fatalf("reason %q has category %q", e.Reason, e.ReasonCategory)
			}
			if tc.stage != "prepare" && (e.SourceSnapshot == nil || len(e.SourceSnapshot.SHA256) != 64) {
				t.Fatalf("source snapshot missing: %+v", e.SourceSnapshot)
			}
			if e.Verdict != domain.MutationInconclusive || e.Reason != tc.reason || lastStage < 0 ||
				e.Stages[lastStage].Name != tc.stage || e.Stages[lastStage].Status != domain.StageFailed {
				t.Fatalf("unexpected result: %+v", e)
			}
			if tc.stage == "baseline" && (e.Mutation != nil || len(obs) != 1) {
				t.Fatalf("work after unhealthy baseline: %+v", e)
			}
			if tc.stage == "validation" && e.Tests != nil {
				t.Fatal("tests ran after validation failure")
			}
			if _, err := os.Stat(copyRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("copy remains: %v", err)
			}
			f := filepath.Join(root, "src")
			b, readErr := os.ReadFile(f)
			info, statErr := os.Stat(f)
			if readErr != nil || statErr != nil || string(b) != original || info.Mode().Perm() != 0o640 {
				t.Fatalf("source changed: %q %v %v", b, readErr, statErr)
			}
		})
	}
	for _, path := range []string{"../src", "src"} {
		spec, root := mutationTestProject(t)
		spec.Experiment.Mutation.Path = path
		if path == "src" {
			spec.Experiment.Mutation.After = "old"
		}
		spec.Experiment.Test.Args[1] = "touch dispatched"
		r := RunEvidence(context.Background(), spec, root, "mutate")
		if r.Decision != domain.RefuseDecision || r.Experiment != nil {
			t.Fatalf("invalid input dispatched: %+v", r)
		}
		if _, err := os.Stat(filepath.Join(root, "dispatched")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid input reached a command: %v", err)
		}
	}
}

func TestMutationRelocatedWorkspaceCannotVerifyCleanup(t *testing.T) {
	spec, root := mutationTestProject(t)
	spec.Experiment.Test.Args[1] = `mv "$PWD" "$PWD-moved"; ` + emit(cleanTests)
	var moved string
	e, _ := runMutation(context.Background(), spec, root, func(path string) (*workspace.Workspace, error) {
		w, err := workspace.Prepare(path)
		if err == nil {
			moved = w.Root + "-moved"
			t.Cleanup(func() { os.RemoveAll(moved) })
		}
		return w, err
	}, (*workspace.Workspace).Close)
	if e.Verdict != domain.MutationInconclusive || e.Reason != "cleanup_failed" {
		t.Fatalf("relocation did not invalidate cleanup: %+v", e)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("fixture failed to relocate its workspace: %v", err)
	}
}

func TestMutationIsolation(t *testing.T) {
	spec, root := mutationTestProject(t)
	var roots []string
	for _, replacement := range []string{"mutant-one", "mutant-two"} {
		spec.Experiment.Mutation.After = replacement
		spec.Experiment.Test.Args[1] = `case "$(cat src)" in old)` + emit(cleanTests) + `;; ` + replacement + `)` +
			emit(strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED")) + `; exit 1;; *) exit 2;; esac`
		e, _ := runMutation(context.Background(), spec, root, func(path string) (*workspace.Workspace, error) {
			w, err := workspace.Prepare(path)
			if err == nil {
				roots = append(roots, w.Root)
			}
			return w, err
		}, (*workspace.Workspace).Close)
		if e.Verdict != domain.MutationKilled || e.Baseline.Tests[0].Status != domain.TestPass {
			t.Fatalf("unexpected result: %+v", e)
		}
	}
	if roots[0] == roots[1] {
		t.Fatalf("workspace reused: %v", roots)
	}
	for _, root := range roots {
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("copy remains: %v", err)
		}
	}
}

func TestMutationEvidenceValidationAndRedaction(t *testing.T) {
	const secret = "mutation-test-secret"
	t.Setenv("TEST_SECRET", secret)
	spec, root := mutationTestProject(t)
	spec.RedactEnv = []string{"TEST_SECRET"}
	spec.Experiment.Test.EnvAllowlist = []string{"TEST_SECRET"}
	spec.Experiment.Test.Args[1] = `printf '%s' "$TEST_SECRET" >&2; if [ "$(cat src)" = new ]; then ` +
		emit(strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED")) + `; exit 1; fi; ` + emit(cleanTests)
	run := func() evidence.Report { return RunEvidence(context.Background(), spec, root, "mutate") }
	r := run()
	b, err := evidence.Marshal(r)
	if err != nil || r.Experiment.Verdict != domain.MutationKilled || bytes.Contains(b, []byte(secret)) ||
		!bytes.Contains(b, []byte(evidence.Marker)) {
		t.Fatalf("invalid or unredacted report: %s %v", b, err)
	}
	for _, edit := range []func(*evidence.Report){
		func(r *evidence.Report) { r.Experiment.Stages = r.Experiment.Stages[:5] },
		func(r *evidence.Report) { r.Experiment.Baseline.Tests[0].Status = domain.TestAssertionFailed },
		func(r *evidence.Report) { r.Experiment.Mutation.Path = "elsewhere" },
		func(r *evidence.Report) { r.Experiment.Mutation.AfterSHA256 = r.Experiment.Mutation.BeforeSHA256 },
		func(r *evidence.Report) { r.Observations = r.Observations[:2] },
		func(r *evidence.Report) { r.Observations[2].Program = "another-runner" },
		func(r *evidence.Report) { r.Observations[2].Args = nil },
		func(r *evidence.Report) { r.Experiment.Tests.Tests[0].ID = "another" },
		func(r *evidence.Report) { r.Decision = domain.Reject },
		func(r *evidence.Report) {
			r.Decision = domain.RefuseDecision
			r.Experiment.Verdict = domain.MutationInconclusive
			r.Experiment.Reason = "unhealthy_baseline"
		},
	} {
		r := run()
		edit(&r)
		if _, err := evidence.Marshal(r); err == nil {
			t.Fatal("accepted forged report")
		}
	}
}
