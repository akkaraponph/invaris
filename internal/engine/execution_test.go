package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// logged returns a command that appends its ID to the attempted log before
// exiting with code, so tests observe which tasks were actually spawned.
func logged(id, code string) Command {
	return Command{ID: id, Program: "sh", Args: []string{"-c", "echo " + id + " >> attempted; exit " + code}}
}

func attempted(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "attempted"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func TestGuardScheduling(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fast, guards bool
		attempted    []string
		statuses     []string
		reasons      []string
	}{
		{"default behavior", true, true, []string{"bad-step", "guard"}, []string{"FAIL", "NOT_RUN", "PASS"}, []string{"", "failFast", ""}},
		{"continue", false, true, []string{"bad-step", "next-step", "guard"}, []string{"FAIL", "PASS", "PASS"}, []string{"", "", ""}},
		{"skip guards", true, false, []string{"bad-step"}, []string{"FAIL", "NOT_RUN", "NOT_RUN"}, []string{"", "failFast", "alwaysRunGuards is false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s := Specification{Version: "invaris.dev/v1alpha1", Name: "schedule", FailFast: &tc.fast, AlwaysRunGuards: &tc.guards,
				Steps: []Command{logged("bad-step", "1"), logged("next-step", "0")}, Guards: []Command{logged("guard", "0")}}
			r := Run(context.Background(), s, root, "run")
			if r.Status != "FAIL" || len(r.Results) != len(tc.statuses) {
				t.Fatalf("unexpected report: %+v", r)
			}
			if got := attempted(t, root); strings.Join(got, ",") != strings.Join(tc.attempted, ",") {
				t.Fatalf("attempted %v, want %v", got, tc.attempted)
			}
			for i, id := range []string{"bad-step", "next-step", "guard"} {
				got := r.Results[i]
				if got.ID != id || got.Status != tc.statuses[i] {
					t.Errorf("result %d = %+v, want %s %s", i, got, id, tc.statuses[i])
				}
				if got.Status == "NOT_RUN" && !strings.Contains(got.Detail, tc.reasons[i]) {
					t.Errorf("result %d reason %q, want %q", i, got.Detail, tc.reasons[i])
				}
			}
		})
	}
}

// An unrun required guard cannot produce PASS even when every attempted task passed.
func TestUnrunGuardNeverPasses(t *testing.T) {
	s := Specification{Version: "invaris.dev/v1alpha1", Name: "x", Steps: []Command{logged("step", "0")}, Guards: []Command{logged("guard", "0")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := Run(ctx, s, t.TempDir(), "check"); r.Status == "PASS" || r.Results[0].Status != "NOT_RUN" {
		t.Fatalf("unrun guard accepted: %+v", r)
	}
}

func TestFailAndRefusePreservedInEitherGuardOrder(t *testing.T) {
	fail := logged("fails", "1")
	refuse := Command{ID: "refuses", Program: "invaris-missing-program"}
	for _, guards := range [][]Command{{fail, refuse}, {refuse, fail}} {
		r := Run(context.Background(), Specification{Version: "invaris.dev/v1alpha1", Name: "x", Guards: guards}, t.TempDir(), "check")
		if r.Status != "FAIL" || len(r.Results) != 2 {
			t.Fatalf("aggregate lost failure: %+v", r)
		}
		statuses := map[string]string{}
		for _, result := range r.Results {
			statuses[result.ID] = result.Status
		}
		if statuses["fails"] != "FAIL" || statuses["refuses"] != "REFUSE" || r.Results[0].ID != guards[0].ID {
			t.Fatalf("individual outcomes not preserved in order: %+v", r.Results)
		}
	}
}

func TestCanceledContextRecordsUnstartedWork(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := Specification{Version: "invaris.dev/v1alpha1", Name: "canceled", Steps: []Command{logged("step", "0")}, Guards: []Command{logged("guard", "0")}}
	r := Run(ctx, s, root, "run")
	if r.Status != "REFUSE" || len(r.Results) != 2 {
		t.Fatalf("unexpected canceled outcome: %+v", r)
	}
	for _, result := range r.Results {
		if result.Status != "NOT_RUN" || !strings.Contains(result.Detail, "context ended before start") {
			t.Fatalf("unstarted work not recorded: %+v", result)
		}
	}
	if got := attempted(t, root); len(got) != 0 {
		t.Fatalf("spawned work after cancellation: %v", got)
	}
}

// Cancellation during a running step refuses that step and records the
// remaining work as NOT_RUN without spawning it.
func TestCancellationDuringRunStopsLaterWork(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := Specification{Version: "invaris.dev/v1alpha1", Name: "midrun", FailFast: new(bool),
		Steps:  []Command{{ID: "slow", Program: "sh", Args: []string{"-c", "echo slow >> attempted; sleep 60"}}, logged("after", "0")},
		Guards: []Command{logged("guard", "0")}}
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if len(attempted(t, root)) > 0 {
				break
			}
		}
		cancel()
	}()
	started := time.Now()
	r := Run(ctx, s, root, "run")
	if time.Since(started) > 5*time.Second {
		t.Fatal("cancellation was not bounded")
	}
	want := []string{"REFUSE", "NOT_RUN", "NOT_RUN"}
	if r.Status != "REFUSE" || len(r.Results) != 3 {
		t.Fatalf("unexpected outcome: %+v", r)
	}
	for i, status := range want {
		if r.Results[i].Status != status {
			t.Fatalf("result %d = %+v, want %s", i, r.Results[i], status)
		}
	}
	if got := attempted(t, root); strings.Join(got, ",") != "slow" {
		t.Fatalf("spawned work after cancellation: %v", got)
	}
}

func TestSchedulingFlagsDecode(t *testing.T) {
	s, err := decodeSpecification([]byte(`{"version":"invaris.dev/v1alpha1","name":"x","failFast":false,"alwaysRunGuards":false}`))
	if err != nil || s.FailFast == nil || *s.FailFast || s.AlwaysRunGuards == nil || *s.AlwaysRunGuards {
		t.Fatalf("flags not preserved: %+v, %v", s, err)
	}
}

// Legacy output keeps both streams; structured guards parse stdout only.
func TestExecuteStreamsAndPathResolution(t *testing.T) {
	r := execute(context.Background(), t.TempDir(), Command{ID: "x", Program: "sh", Args: []string{"-c", "printf out; printf err >&2"}})
	if r.Status != "PASS" || r.Output != "outerr" {
		t.Fatalf("legacy output: %+v", r)
	}
	r = execute(context.Background(), t.TempDir(), Command{ID: "x", Program: "sh", Args: []string{"-c", "printf out; printf err >&2"}, PolicyID: "p"})
	if r.Output != "out" {
		t.Fatalf("structured output included stderr: %+v", r)
	}
	if r = execute(context.Background(), t.TempDir(), Command{ID: "x", Program: "invaris-missing-program"}); r.Status != "REFUSE" || r.ExitCode != nil {
		t.Fatalf("missing program: %+v", r)
	}
}
