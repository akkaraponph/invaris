package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// expectation says what a challenged check must show for PROVEN. A non-empty
// Policy makes the proof strong: exactly one finding with this policy and
// native rule at the mutated path. Otherwise the proof is weak and Marker
// decides whether the failing output is the expected one.
type expectation struct {
	Policy, Rule string
	Marker       func(domain.Check) bool
}

// prove challenges a guard inside one disposable copy of root. The same
// evaluator, run in the same workspace over the same scope, produces the
// baseline and the challenged check; the one declared file is replaced between
// them; and the copy is removed and verified absent before the proof is final.
// Production passes workspace.Prepare and (*Workspace).Close; tests inject
// failures. A proof is PROVEN only when every stage succeeded, including
// cleanup. The source checkout is never written.
func prove(ctx context.Context, root string, c Challenge, expect expectation,
	evaluate func(context.Context, string) domain.Check,
	prepare func(string) (*workspace.Workspace, error),
	cleanup func(*workspace.Workspace) error) domain.Proof {

	p := domain.Proof{ID: c.ID, PolicyID: expect.Policy, GuardID: c.Guard, Target: c.File,
		EvidenceStrength: domain.StrengthWeak, Status: domain.Inconclusive, Stages: []domain.Stage{}}
	if expect.Policy != "" {
		p.EvidenceStrength = domain.StrengthStrong
	}
	stage := func(name string, start time.Time, err error) bool {
		s := domain.Stage{Name: name, Status: domain.StageOK, DurationMs: time.Since(start).Milliseconds()}
		if err != nil {
			s.Status, s.Detail = domain.StageFailed, err.Error()
		}
		p.Stages = append(p.Stages, s)
		return err == nil
	}
	fail := func(format string, args ...any) {
		p.Status, p.Detail = domain.Inconclusive, fmt.Sprintf(format, args...)
	}

	start := time.Now()
	ws, err := prepare(root)
	if err == nil && (ws == nil || ws.Root == "" || ws.Root == root) {
		// A workspace that is missing or is the user's checkout is never used
		// and never cleaned up by the proof.
		err, ws = errors.New("unsafe proof workspace"), nil
	}
	if !stage(domain.StagePrepare, start, err) {
		fail("prepare workspace: %v", err)
		return p
	}

	p.Status, p.Detail = proofStages(ctx, ws, c, expect, evaluate, &p, stage)

	start = time.Now()
	cerr := closeWorkspace(ws, cleanup)
	if !stage(domain.StageCleanup, start, cerr) {
		// A proof whose disposable copy could not be removed is not certified,
		// whatever the checks showed.
		note := "cleanup failed: " + cerr.Error()
		if p.Status == domain.Proven {
			note = "proof not certified; " + note
		}
		if p.Detail != "" {
			note = p.Detail + "; " + note
		}
		p.Status, p.Detail = domain.Inconclusive, note
	}
	return p
}

// closeWorkspace also checks cleanup implementations injected by tests.
func closeWorkspace(ws *workspace.Workspace, cleanup func(*workspace.Workspace) error) error {
	if err := cleanup(ws); err != nil {
		return err
	}
	if _, err := os.Lstat(ws.Root); err == nil {
		return errors.New("workspace still exists after cleanup")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("verify workspace removal: %w", err)
	}
	return nil
}

// proofStages runs baseline, mutation and challenged check in ws and returns
// the provisional status. It never removes the workspace.
func proofStages(ctx context.Context, ws *workspace.Workspace, c Challenge, expect expectation,
	evaluate func(context.Context, string) domain.Check, p *domain.Proof,
	stage func(string, time.Time, error) bool) (domain.ProofStatus, string) {

	if err := ctx.Err(); err != nil {
		return domain.Inconclusive, "context ended before the baseline: " + err.Error()
	}
	start := time.Now()
	base := evaluate(ctx, ws.Root)
	base.ID = p.ID + ":baseline"
	p.Baseline = &base
	var err error
	switch {
	case base.Status != domain.Pass:
		err = fmt.Errorf("baseline guard did not pass: %s", base.Status)
	case expect.Policy != "" && (base.Inspected == nil || *base.Inspected <= 0):
		err = errors.New("baseline inspected no targets")
	}
	if !stage(domain.StageBaseline, start, err) {
		return domain.Inconclusive, err.Error()
	}

	// The target is revalidated inside Replace, after the baseline ran.
	start = time.Now()
	m, err := workspace.Replace(ws.Root, c.File, c.Before, c.After)
	if !stage(domain.StageMutate, start, err) {
		return domain.Inconclusive, err.Error()
	}
	p.Mutation = &domain.Mutation{Path: m.Path, BeforeSHA256: m.BeforeSHA256, AfterSHA256: m.AfterSHA256}
	p.FaultID = faultID(expect.Policy, m)

	if err := ctx.Err(); err != nil {
		return domain.Inconclusive, "context ended before the challenged check: " + err.Error()
	}
	start = time.Now()
	got := evaluate(ctx, ws.Root)
	got.ID = p.ID + ":challenged"
	p.Challenged = &got
	status, detail := classify(expect, c.File, got)
	stage(domain.StageChallenged, start, nil)
	return status, detail
}

// classify decides the proof from the challenged check alone; the baseline has
// already passed.
func classify(expect expectation, target string, got domain.Check) (domain.ProofStatus, string) {
	if got.Status != domain.Pass && got.Status != domain.Fail {
		return domain.Inconclusive, "guard could not execute after injection: " + string(got.Status)
	}
	if expect.Policy == "" {
		switch {
		case got.Status == domain.Fail && expect.Marker != nil && expect.Marker(got):
			return domain.Proven, "weak evidence: baseline passed; injected violation triggered expected output marker"
		default:
			return domain.Unproven, "injected violation did not produce the expected failing guard evidence"
		}
	}
	if got.Status == domain.Pass {
		if got.Inspected == nil || *got.Inspected <= 0 {
			return domain.Inconclusive, "challenged guard inspected no targets"
		}
		return domain.Unproven, "guard passed after mutation"
	}
	if len(got.Findings) != 1 {
		return domain.Inconclusive, fmt.Sprintf("expected exactly one finding at the mutated path, got %d", len(got.Findings))
	}
	if f := got.Findings[0]; f.PolicyID != expect.Policy || f.RuleID != expect.Rule || f.Path != target {
		return domain.Inconclusive, "finding does not match the expected policy, rule and mutated path"
	}
	return domain.Proven, "guard reported the expected policy and rule at the mutated path"
}

// faultID is derived from the policy, the target and the recorded digests, so
// analyzers need not echo an engine-only identifier.
func faultID(policy string, m workspace.Mutation) string {
	sum := sha256.Sum256([]byte(policy + "\x00" + m.Path + "\x00" + m.BeforeSHA256 + "\x00" + m.AfterSHA256))
	return "fault-" + hex.EncodeToString(sum[:8])
}
