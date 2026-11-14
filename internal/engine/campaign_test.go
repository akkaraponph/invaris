package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// sourceDigest hashes every path and file body under dir.
func sourceDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		h.Write([]byte(p + "\x00"))
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			h.Write(b)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// project writes src and the given harnesses, then a campaign over entries. It
// fails the test if the run writes back to the directory.
func project(t *testing.T, harnesses map[string]string, entries []entry, acceptance map[string]any) (Campaign, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "src", "old")
	for name, body := range harnesses {
		writeFile(t, dir, name, body)
	}
	writeFile(t, dir, "campaign.json", campaignJSON(t, entries, acceptance))
	c, err := LoadCampaign(filepath.Join(dir, "campaign.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := sourceDigest(t, dir)
	t.Cleanup(func() {
		if sourceDigest(t, dir) != before {
			t.Error("the campaign wrote back to the source directory")
		}
	})
	return c, dir
}

func all(c Campaign) []bool {
	s, _ := c.Select(nil)
	return s
}

func run(t *testing.T, ctx context.Context, c Campaign) evidence.CampaignReport {
	t.Helper()
	r := RunCampaign(ctx, c, all(c), nil)
	if err := evidence.ValidateCampaign(r); err != nil {
		t.Fatalf("invalid campaign report: %v", err)
	}
	return r
}

func verdicts(x evidence.CampaignExperiment) []string {
	var out []string
	for _, a := range x.Attempts {
		v := string(a.Execution)
		if a.Verdict != nil {
			v = *a.Verdict
		}
		out = append(out, v)
	}
	return out
}

func TestCampaignOrderIsolationAndIdentity(t *testing.T) {
	// A second killed mutant runs after the first: its baseline fails unless
	// it sees the original src, so a KILLED verdict shows no leakage.
	c, _ := project(t, map[string]string{
		"mut.json":   mutationHarness(t, "m1", shTests, shValid),
		"guard.json": guardHarness(t, "c1", shGuard, "p"),
		"mut2.json":  mutationHarness(t, "m2", shTestsPass, shValid),
		"mut3.json":  mutationHarness(t, "m3", shTests, shValid),
	}, []entry{
		{"killed", "mutation", "mut.json", "m1", 2},
		{"proven", "guardrail", "guard.json", "c1", 2},
		{"survived", "mutation", "mut2.json", "m2", 2},
		{"killed-again", "mutation", "mut3.json", "m3", 2},
	}, allRequirements())
	r := run(t, context.Background(), c)

	var ids []string
	for _, x := range r.Experiments {
		ids = append(ids, x.ID)
		for i, a := range x.Attempts {
			if a.ID != x.ID+"#"+string(rune('1'+i)) || a.Index != i+1 {
				t.Fatalf("attempt %d of %s: %s %d", i, x.ID, a.ID, a.Index)
			}
		}
	}
	if !slices.Equal(ids, []string{"killed", "proven", "survived", "killed-again"}) {
		t.Fatalf("order %v", ids)
	}
	for _, x := range r.Experiments {
		want := map[string]string{"killed": "KILLED", "proven": "PROVEN", "survived": "SURVIVED", "killed-again": "KILLED"}[x.ID]
		if got := verdicts(x); !slices.Equal(got, []string{want, want}) || x.Stability != domain.Consistent || *x.AggregateVerdict != want {
			t.Fatalf("%s: %v %s", x.ID, got, x.Stability)
		}
	}
	// Every attempt evaluated the recorded source identity.
	digest := *r.SourceIdentity.SnapshotSHA256
	for _, x := range r.Experiments {
		for _, a := range x.Attempts {
			var got string
			if a.Mutation != nil {
				got = a.Mutation.SourceSnapshot.SHA256
			} else {
				got = a.Proof.SourceSnapshot.SHA256
			}
			if got != digest {
				t.Fatalf("%s evaluated %s, campaign %s", a.ID, got, digest)
			}
		}
	}
	s := r.MutationSummary
	if s.Killed != 2 || s.Survived != 1 || s.Eligible != 3 || s.Score == nil || *s.Score != 2.0/3 || r.GuardrailSummary.Proven != 1 {
		t.Fatalf("summaries %+v %+v", s, r.GuardrailSummary)
	}
	if r.Decision != domain.Reject || !r.AcceptanceEvaluation.Complete || r.Stages[1].Status != domain.StageOK {
		t.Fatalf("decision %s %+v", r.Decision, r.AcceptanceEvaluation)
	}
}

func TestCampaignAttemptsUseFreshWorkspaces(t *testing.T) {
	state := t.TempDir()
	t.Setenv("INVARIS_STATE", state)
	record := `pwd >> "$INVARIS_STATE/pwd"; ` + shTests
	c, _ := project(t, map[string]string{"mut.json": mutationHarness(t, "m1", record, shValid, "INVARIS_STATE")},
		[]entry{{"killed", "mutation", "mut.json", "m1", 3}}, map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true})
	r := run(t, context.Background(), c)
	if r.Decision != domain.Accept {
		t.Fatalf("decision %s: %+v", r.Decision, r.AcceptanceEvaluation)
	}
	raw, err := os.ReadFile(filepath.Join(state, "pwd"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(raw))
	if len(lines) != 6 {
		t.Fatalf("pwd lines %v", lines)
	}
	distinct := map[string]bool{}
	for i := 0; i < 6; i += 2 {
		if lines[i] != lines[i+1] {
			t.Fatalf("baseline and mutant tests of one attempt ran in different workspaces: %v", lines)
		}
		distinct[lines[i]] = true
	}
	if len(distinct) != 3 {
		t.Fatalf("attempts shared a workspace: %v", lines)
	}
	for w := range distinct {
		if _, err := os.Lstat(w); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("workspace %s still exists", w)
		}
	}
}

func TestCampaignUnhealthyBaselineIsNotASurvivor(t *testing.T) {
	failing := `printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"ASSERTION_FAILED"}]}'; exit 1`
	c, _ := project(t, map[string]string{"mut.json": mutationHarness(t, "m1", failing, shValid)},
		[]entry{{"broken", "mutation", "mut.json", "m1", 1}}, map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true})
	r := run(t, context.Background(), c)
	a := r.Experiments[0].Attempts[0]
	if *a.Verdict != "INCONCLUSIVE" || a.Reason != domain.ReasonUnhealthyBaseline || a.Execution != domain.Executed {
		t.Fatalf("attempt %+v", a)
	}
	if r.Experiments[0].Stability != domain.Incomplete || r.MutationSummary.Survived != 0 || r.MutationSummary.Inconclusive != 1 || r.Decision != domain.RefuseDecision {
		t.Fatalf("%s %+v %s", r.Experiments[0].Stability, r.MutationSummary, r.Decision)
	}
}

// Each guard behaviour gives the same verdict and reason in a campaign as the
// single challenge action does.
func TestCampaignGuardrailEvidence(t *testing.T) {
	finding := func(policy, path string) string {
		return `{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[{"policyId":"` + policy + `","path":"` + path + `"}]}`
	}
	clean := `{"version":"invaris.inspection/v1alpha1","policyId":"p","inspected":1,"findings":[]}`
	onNew := func(then string) string {
		return `if [ "$(cat src)" = new ]; then ` + then + `; fi; printf '%s\n' '` + clean + `'`
	}
	for _, tc := range []struct {
		name, guard, status string
		missing             bool
	}{
		{"wrong policy", onNew(`printf '%s\n' '` + finding("q", "src") + `'; exit 1`), "INCONCLUSIVE", false},
		{"wrong path", onNew(`printf '%s\n' '` + finding("p", "other") + `'; exit 1`), "INCONCLUSIVE", false},
		{"no findings with exit 1", onNew(`printf '%s\n' '` + clean + `'; exit 1`), "INCONCLUSIVE", false},
		{"finding with exit 0", onNew(`printf '%s\n' '` + finding("p", "src") + `'; exit 0`), "INCONCLUSIVE", false},
		{"truncated output", onNew(`head -c 70000 /dev/zero; exit 1`), "INCONCLUSIVE", false},
		{"malformed", onNew(`printf '{'; exit 1`), "INCONCLUSIVE", false},
		{"missing executable", shGuard, "INCONCLUSIVE", true},
		{"passes after injection", `printf '%s\n' '` + clean + `'`, "UNPROVEN", false},
		{"detected", shGuard, "PROVEN", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := guardHarness(t, "c1", tc.guard, "p")
			if tc.missing {
				h = strings.Replace(h, `"program":"sh"`, `"program":"invaris-missing-executable-4242"`, 1)
			}
			policy := map[string]any{"requireNoSurvivors": false, "requireAllGuardrailsProven": true, "requireNoInconclusive": true, "requireNoMixed": true}
			c, dir := project(t, map[string]string{"guard.json": h}, []entry{{"g", "guardrail", "guard.json", "c1", 1}}, policy)
			r := run(t, context.Background(), c)
			a := r.Experiments[0].Attempts[0]
			single := RunEvidence(context.Background(), c.Experiments[0].Spec, dir, "challenge")
			want := single.Proofs[0]
			if a.Verdict == nil || *a.Verdict != tc.status || string(want.Status) != tc.status || a.Reason != want.Reason {
				t.Fatalf("campaign %v/%s, single %s/%s", a.Verdict, a.Reason, want.Status, want.Reason)
			}
			t.Logf("verdict %s reason %s", *a.Verdict, a.Reason)
			wantDecision := map[string]domain.Decision{"PROVEN": domain.Accept, "UNPROVEN": domain.Reject}[tc.status]
			if wantDecision == "" {
				wantDecision = domain.RefuseDecision
			}
			if r.Decision != wantDecision {
				t.Fatalf("decision %s, want %s", r.Decision, wantDecision)
			}
		})
	}
}

func TestCampaignMixedAttempts(t *testing.T) {
	state := t.TempDir()
	t.Setenv("INVARIS_STATE", state)
	// The mutant is caught on odd counts only: KILLED, SURVIVED, KILLED.
	flaky := `if [ "$(cat src)" = new ]; then n=$(( $(cat "$INVARIS_STATE/n" 2>/dev/null || echo 0) + 1 )); echo $n > "$INVARIS_STATE/n"; ` +
		`if [ $((n % 2)) -eq 1 ]; then printf '%s\n' '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"case","status":"ASSERTION_FAILED"}]}'; exit 1; fi; fi; ` + shTestsPass
	c, _ := project(t, map[string]string{"mut.json": mutationHarness(t, "m1", flaky, shValid, "INVARIS_STATE")},
		[]entry{{"flaky", "mutation", "mut.json", "m1", 3}}, map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true})
	r := run(t, context.Background(), c)
	x := r.Experiments[0]
	if got := verdicts(x); !slices.Equal(got, []string{"KILLED", "SURVIVED", "KILLED"}) {
		t.Fatalf("attempts %v", got)
	}
	if x.Stability != domain.Mixed || x.AggregateVerdict != nil || r.MutationSummary.Mixed != 1 || r.MutationSummary.Score != nil || r.Decision != domain.RefuseDecision {
		t.Fatalf("%s %v %+v %s", x.Stability, x.AggregateVerdict, r.MutationSummary, r.Decision)
	}
}

func TestCampaignCancellation(t *testing.T) {
	state := t.TempDir()
	t.Setenv("INVARIS_STATE", state)
	slow := `if [ "$(cat src)" = new ]; then touch "$INVARIS_STATE/started"; sleep 5; fi; ` + shTests
	var snapshotRoot string
	prepare := func(src string) (*workspace.Workspace, error) {
		ws, err := workspace.Prepare(src)
		if err == nil {
			snapshotRoot = ws.Root
		}
		return ws, err
	}
	c, _ := project(t, map[string]string{
		"mut.json":  mutationHarness(t, "m1", shTests, shValid),
		"slow.json": mutationHarness(t, "m2", slow, shValid, "INVARIS_STATE"),
		"mut3.json": mutationHarness(t, "m3", shTests, shValid),
	}, []entry{{"first", "mutation", "mut.json", "m1", 1}, {"slow", "mutation", "slow.json", "m2", 2}, {"last", "mutation", "mut3.json", "m3", 1}},
		map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(state, "started")); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	started := time.Now()
	r := runCampaign(ctx, c, all(c), nil, prepare, (*workspace.Workspace).Close)
	if err := evidence.ValidateCampaign(r); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("cancellation did not stop the active attempt")
	}
	first, slowX, last := r.Experiments[0], r.Experiments[1], r.Experiments[2]
	if got := verdicts(first); !slices.Equal(got, []string{"KILLED"}) {
		t.Fatalf("completed result lost: %v", got)
	}
	if a := slowX.Attempts[0]; a.Execution != domain.Canceled || a.Reason != domain.ReasonCanceled || a.Mutation == nil {
		t.Fatalf("active attempt %+v", a)
	}
	for _, a := range []evidence.CampaignAttempt{slowX.Attempts[1], last.Attempts[0]} {
		if a.Execution != domain.NotStarted || a.Reason != domain.ReasonCanceled {
			t.Fatalf("unstarted attempt %+v", a)
		}
	}
	if r.Decision != domain.RefuseDecision || r.Stages[1].Name != "cleanup" || r.Stages[1].Status != domain.StageOK {
		t.Fatalf("decision %s stages %+v", r.Decision, r.Stages)
	}
	if _, err := os.Lstat(snapshotRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("snapshot %s not removed", snapshotRoot)
	}
}

func TestCampaignSnapshotFailures(t *testing.T) {
	harnesses := map[string]string{"mut.json": mutationHarness(t, "m1", shTests, shValid)}
	entries := []entry{{"killed", "mutation", "mut.json", "m1", 2}}
	policy := map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true}
	check := func(t *testing.T, r evidence.CampaignReport, stages []string, reason string) {
		t.Helper()
		if err := evidence.ValidateCampaign(r); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range r.Stages {
			got = append(got, s.Name+":"+s.Status)
		}
		if !slices.Equal(got, stages) || r.Decision != domain.RefuseDecision {
			t.Fatalf("stages %v decision %s", got, r.Decision)
		}
		if reason == "" {
			return
		}
		for _, a := range r.Experiments[0].Attempts {
			if a.Execution != domain.NotStarted || a.Reason != reason {
				t.Fatalf("attempt %+v", a)
			}
		}
	}
	t.Run("prepare fails", func(t *testing.T) {
		c, _ := project(t, harnesses, entries, policy)
		r := runCampaign(context.Background(), c, all(c), nil, func(string) (*workspace.Workspace, error) { return nil, errors.New("disk full") }, (*workspace.Workspace).Close)
		check(t, r, []string{"snapshot:FAILED"}, domain.ReasonPrepareFailed)
	})
	t.Run("cleanup fails", func(t *testing.T) {
		c, _ := project(t, harnesses, entries, policy)
		var root string
		prepare := func(src string) (*workspace.Workspace, error) {
			ws, err := workspace.Prepare(src)
			if ws != nil {
				root = ws.Root
			}
			return ws, err
		}
		r := runCampaign(context.Background(), c, all(c), nil, prepare, func(*workspace.Workspace) error { return errors.New("busy") })
		os.RemoveAll(root)
		check(t, r, []string{"snapshot:OK", "cleanup:FAILED"}, "")
		if got := verdicts(r.Experiments[0]); !slices.Equal(got, []string{"KILLED", "KILLED"}) || r.AcceptanceEvaluation.Complete {
			t.Fatalf("%v %+v", got, r.AcceptanceEvaluation)
		}
	})
	t.Run("snapshot manifest unreadable", func(t *testing.T) {
		c, _ := project(t, harnesses, entries, policy)
		prepare := func(src string) (*workspace.Workspace, error) {
			ws, err := workspace.Prepare(src)
			if err == nil {
				// An oversized file makes the manifest unreadable; Close still removes it.
				err = os.WriteFile(filepath.Join(ws.Root, "big"), make([]byte, workspace.MaxFileBytes+1), 0o600)
			}
			return ws, err
		}
		r := runCampaign(context.Background(), c, all(c), nil, prepare, (*workspace.Workspace).Close)
		check(t, r, []string{"snapshot:FAILED", "cleanup:OK"}, domain.ReasonWorkspaceChanged)
		if r.SourceIdentity.SnapshotSHA256 != nil {
			t.Fatal("a snapshot digest was recorded for an unreadable manifest")
		}
	})
	t.Run("harness edited after load", func(t *testing.T) {
		c, dir := project(t, harnesses, entries, policy)
		body, _ := os.ReadFile(filepath.Join(dir, "mut.json"))
		writeFile(t, dir, "mut.json", string(body)+" ")
		t.Cleanup(func() { writeFile(t, dir, "mut.json", string(body)) })
		r := RunCampaign(context.Background(), c, all(c), nil)
		check(t, r, []string{"snapshot:FAILED", "cleanup:OK"}, domain.ReasonWorkspaceChanged)
	})
}

// A trusted command that writes into the frozen snapshot through its absolute
// path is detected before the next attempt; every later attempt is refused.
func TestCampaignSnapshotTampering(t *testing.T) {
	t.Setenv("INVARIS_SNAPSHOT", "")
	prepare := func(src string) (*workspace.Workspace, error) {
		ws, err := workspace.Prepare(src)
		if err == nil {
			os.Setenv("INVARIS_SNAPSHOT", ws.Root)
		}
		return ws, err
	}
	tamper := `[ -e "$INVARIS_SNAPSHOT/extra" ] || touch "$INVARIS_SNAPSHOT/extra"; ` + shTests
	c, _ := project(t, map[string]string{
		"mut.json":  mutationHarness(t, "m1", tamper, shValid, "INVARIS_SNAPSHOT"),
		"mut2.json": mutationHarness(t, "m2", shTests, shValid),
	}, []entry{{"tamper", "mutation", "mut.json", "m1", 2}, {"after", "mutation", "mut2.json", "m2", 1}},
		map[string]any{"requireNoSurvivors": true, "requireAllGuardrailsProven": false, "requireNoInconclusive": true, "requireNoMixed": true})
	r := runCampaign(context.Background(), c, all(c), nil, prepare, (*workspace.Workspace).Close)
	if err := evidence.ValidateCampaign(r); err != nil {
		t.Fatal(err)
	}
	if got := verdicts(r.Experiments[0]); !slices.Equal(got, []string{"KILLED", "REFUSED"}) {
		t.Fatalf("tamper attempts %v", got)
	}
	if a := r.Experiments[1].Attempts[0]; a.Execution != domain.Refused || a.Reason != domain.ReasonWorkspaceChanged {
		t.Fatalf("later attempt %+v", a)
	}
	if r.Decision != domain.RefuseDecision || r.Stages[1].Status != domain.StageFailed {
		t.Fatalf("decision %s stages %+v", r.Decision, r.Stages)
	}
}

func TestCampaignFilteredSelection(t *testing.T) {
	c, _ := project(t, map[string]string{
		"mut.json":   mutationHarness(t, "m1", shTests, shValid),
		"guard.json": guardHarness(t, "c1", `exit 1`, "p"),
	}, []entry{{"killed", "mutation", "mut.json", "m1", 1}, {"broken-guard", "guardrail", "guard.json", "c1", 1}}, allRequirements())
	selected, err := c.Select([]string{"killed"})
	if err != nil {
		t.Fatal(err)
	}
	r := RunCampaign(context.Background(), c, selected, []string{"killed"})
	if err := evidence.ValidateCampaign(r); err != nil {
		t.Fatal(err)
	}
	g := r.Experiments[1]
	if g.Selection != domain.FilteredOut || len(g.Attempts) != 0 || r.GuardrailSummary.Filtered != 1 || r.Selection.Mode != "filtered" {
		t.Fatalf("%+v %+v", g, r.Selection)
	}
	if r.Decision != domain.Accept || r.AcceptanceEvaluation.Requirements[1].Status != domain.RequirementNotApplicable {
		t.Fatalf("%s %+v", r.Decision, r.AcceptanceEvaluation)
	}
}
