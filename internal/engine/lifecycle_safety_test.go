package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

const safetyGuard = `if grep -q BAD src/a.txt; then printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-bad","inspected":1,"findings":[{"policyId":"no-bad","path":"src/a.txt"}]}\n'; exit 1; fi; printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-bad","inspected":1,"findings":[]}\n'`

func isolatedTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

func noWorkspacesLeft(t *testing.T, tmp string) {
	t.Helper()
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "invaris-") {
			t.Fatalf("workspace left in TMPDIR: %s", e.Name())
		}
	}
}

func TestCanceledPrepareIsInconclusiveAndClean(t *testing.T) {
	tmp := isolatedTemp(t)
	root, snapshot := proofSource(t)
	before := snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	p := prove(ctx, root, challengeFor("src/a.txt"), strongExpect, func(context.Context, string) domain.Check {
		calls++
		return domain.Check{Status: domain.Pass}
	}, prepareFunc(ctx, workspace.Options{}), (*workspace.Workspace).Close)
	if p.Status != domain.Inconclusive || p.Reason != domain.ReasonCanceled || p.ReasonCategory != domain.CategoryInfrastructure || calls != 0 {
		t.Fatalf("proof %+v, evaluator calls %d", p, calls)
	}
	if len(p.Stages) != 1 || p.Stages[0].Name != domain.StagePrepare || p.Stages[0].Status != domain.StageFailed {
		t.Fatalf("stages %+v", p.Stages)
	}
	if d := workspace.Diff(before, snapshot()); len(d) != 0 {
		t.Fatalf("source changed: %v", d)
	}
	noWorkspacesLeft(t, tmp)
}

// Every outcome leaves the source byte-for-byte unchanged and removes the
// owned workspace. A trusted guard writing outside the workspace is not
// contained: the engine detects only changes inside its copy.
func TestChallengeLifecycleNeverChangesSource(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside-sentinel")
	guard := func(script string, timeout int) Command {
		return Command{ID: "g", Program: "sh", Args: []string{"-c", script}, PolicyID: proofPolicy, TimeoutSeconds: timeout}
	}
	for _, tc := range []struct {
		name    string
		guard   Command
		before  string
		ctx     func() (context.Context, context.CancelFunc)
		status  domain.ProofStatus
		reason  string
		outside bool
	}{
		{name: "proven", guard: guard(safetyGuard, 10), status: domain.Proven, reason: domain.ReasonGuardDetected},
		{name: "guard passes after change", guard: guard(strings.ReplaceAll(safetyGuard, "BAD", "NEVER"), 10), status: domain.Unproven, reason: domain.ReasonGuardPassed},
		{name: "timeout", guard: guard("sleep 5", 1), status: domain.Inconclusive, reason: domain.ReasonTimeout},
		{name: "output overflow", guard: guard("head -c 200000 /dev/zero | tr '\\0' x", 10), status: domain.Inconclusive, reason: domain.ReasonOutputLimit},
		{name: "canceled guard", guard: guard("sleep 5", 30), status: domain.Inconclusive, reason: domain.ReasonCanceled,
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(300*time.Millisecond, cancel)
				return ctx, cancel
			}},
		{name: "missing before marker", guard: guard(safetyGuard, 10), before: "ABSENT", status: domain.Inconclusive, reason: domain.ReasonMutationRejected},
		{name: "guard writes into workspace", guard: guard("echo x > src/new.txt; "+safetyGuard, 10), status: domain.Inconclusive, reason: domain.ReasonWorkspaceChanged},
		{name: "guard writes outside (not contained)", guard: guard("touch '"+outside+"'; "+safetyGuard, 10), status: domain.Proven, reason: domain.ReasonGuardDetected, outside: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(outside)
			tmp := isolatedTemp(t)
			root, snapshot := proofSource(t)
			before := snapshot()
			c := challengeFor("src/a.txt")
			if tc.before != "" {
				c.Before = tc.before
			}
			spec := Specification{Version: versionV2, Name: "safety", TrustMode: "trusted-local", NetworkPolicy: "unrestricted",
				WritablePaths: []string{"src/a.txt"}, Guards: []Command{tc.guard}, Challenges: []Challenge{c}}
			ctx, cancel := context.Background(), context.CancelFunc(func() {})
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			r := RunEvidence(ctx, spec, root, "challenge")
			if len(r.Proofs) != 1 {
				t.Fatalf("proofs %+v", r.Proofs)
			}
			p := r.Proofs[0]
			if p.Status != tc.status || p.Reason != tc.reason {
				t.Fatalf("got %s/%s (%s), want %s/%s", p.Status, p.Reason, p.Detail, tc.status, tc.reason)
			}
			cleaned := false
			for _, s := range p.Stages {
				cleaned = cleaned || s.Name == domain.StageCleanup && s.Status == domain.StageOK
			}
			if !cleaned {
				t.Fatalf("cleanup not verified: %+v", p.Stages)
			}
			if d := workspace.Diff(before, snapshot()); len(d) != 0 {
				t.Fatalf("source changed: %v", d)
			}
			noWorkspacesLeft(t, tmp)
			if _, err := os.Stat(outside); (err == nil) != tc.outside {
				t.Fatalf("outside sentinel present=%v, want %v", err == nil, tc.outside)
			}
		})
	}
}
