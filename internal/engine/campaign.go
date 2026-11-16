package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// RunCampaign runs the selected experiments of a loaded campaign in
// declaration order and returns the redacted invaris.evidence/v1alpha4 report.
// The CLI validates the report when it marshals it.
//
// The source is copied once into a frozen snapshot. Every attempt runs the
// existing mutation or challenge lifecycle with the snapshot as its source, so
// each attempt gets its own fresh copy and no attempt sees another attempt's
// files. The snapshot's manifest is checked before every attempt and before it
// is removed; a change refuses every attempt that starts later.
//
// parallel (1 to MaxParallel) runs up to that many attempts at once. Each still
// has its own workspace; commands that share caches, ports or other external
// state can affect each other, which a sequential run (1) avoids.
func RunCampaign(ctx context.Context, c Campaign, selected []bool, only []string, parallel int) evidence.CampaignReport {
	return runCampaign(ctx, c, selected, only, parallel, prepareFunc(ctx, c.Workspace), (*workspace.Workspace).Close)
}

// MaxParallel bounds --parallel.
const MaxParallel = 8

func runCampaign(ctx context.Context, c Campaign, selected []bool, only []string, parallel int,
	prepare func(string) (*workspace.Workspace, error), cleanup func(*workspace.Workspace) error) evidence.CampaignReport {
	parallel = max(1, min(parallel, MaxParallel))

	r := evidence.CampaignReport{
		SchemaVersion: evidence.CampaignSchemaVersion, RunID: newRunID(), Action: "campaign",
		Campaign: c.Name, CampaignConfigDigest: c.ConfigDigest, EngineVersion: engineVersion(),
		Environment: evidence.Environment{OS: runtime.GOOS, Arch: runtime.GOARCH},
		StartedAt:   time.Now().UTC(), AcceptancePolicy: c.Acceptance,
		SourceIdentity: evidence.SourceIdentity{GitRevision: gitRevision(ctx, c.Root), GitDirty: gitDirty(ctx, c.Root),
			Excluded: slices.Clone(evidence.ExcludedFromSnapshot)},
		Selection:   evidence.CampaignSelection{Mode: "all", Only: []string{}},
		Parallelism: parallel,
	}
	if len(only) > 0 {
		r.Selection.Mode, r.Selection.Only = "filtered", slices.Clone(only)
	}
	for i, x := range c.Experiments {
		e := evidence.CampaignExperiment{ID: x.ID, Kind: x.Kind, Harness: x.Harness, Root: x.Dir, HarnessDigest: x.HarnessDigest,
			ReferenceID: x.ReferenceID, Repeat: x.Repeat, Selection: domain.FilteredOut, Attempts: []evidence.CampaignAttempt{}}
		if selected[i] {
			e.Selection = domain.Selected
			r.Selection.Selected++
		} else {
			r.Selection.Filtered++
		}
		r.Experiments = append(r.Experiments, e)
	}
	diagnose := func(code, message string) {
		r.Diagnostics = append(r.Diagnostics, domain.Diagnostic{Code: code, Message: message})
	}
	stage := func(name string, start time.Time, err error) {
		s := domain.Stage{Name: name, Status: domain.StageOK, DurationMs: time.Since(start).Milliseconds()}
		if err != nil {
			s.Status, s.Detail = domain.StageFailed, err.Error()
		}
		r.Stages = append(r.Stages, s)
	}
	// notStarted fills every selected attempt that has no record yet.
	notStarted := func(reason, detail string) {
		for i := range r.Experiments {
			e := &r.Experiments[i]
			for n := len(e.Attempts) + 1; e.Selection == domain.Selected && n <= e.Repeat; n++ {
				e.Attempts = append(e.Attempts, evidence.CampaignAttempt{ID: evidence.AttemptID(e.ID, n), Index: n,
					Execution: domain.NotStarted, Reason: reason, ReasonCategory: domain.ReasonCategory(reason), Detail: detail,
					Observations: []evidence.Observation{}})
			}
		}
	}

	start := time.Now()
	snap, err := prepare(c.Root)
	if err == nil && (snap == nil || snap.Root == "" || snap.Root == c.Root) {
		// Never use, and never remove, a snapshot that is missing or is the source.
		err, snap = errors.New("unsafe campaign snapshot"), nil
	}
	if err != nil {
		stage(evidence.StageSnapshot, start, err)
		diagnose("SNAPSHOT_FAILED", err.Error())
		notStarted(prepareFailure(err), "the campaign snapshot could not be prepared")
	} else {
		digest, roots, err := freezeSnapshot(c, snap.Root, c.Workspace, &r.SourceIdentity)
		stage(evidence.StageSnapshot, start, err)
		if err != nil {
			diagnose("SNAPSHOT_FAILED", err.Error())
			notStarted(domain.ReasonWorkspaceChanged, "the campaign snapshot does not match the loaded configuration")
		} else {
			r.Tools = probeTools(ctx, c.Tools, snap.Root)
			if why := preflight(ctx, c, selected, snap.Root); why != "" {
				diagnose("PREFLIGHT_PROVIDER", why)
				notStarted(domain.ReasonProviderUnavailable, why)
			} else {
				runAttempts(ctx, c, selected, parallel, snap.Root, digest, roots, &r, diagnose)
			}
		}
		start = time.Now()
		err = nil
		if digest != "" { // no digest means the snapshot manifest was never read
			if err = verifySnapshot(snap.Root, c.Workspace, digest); err != nil {
				err = fmt.Errorf("snapshot changed before cleanup: %w", err)
			}
		}
		err = errors.Join(err, closeWorkspace(snap, cleanup))
		stage(evidence.StageCleanup, start, err)
		if err != nil {
			diagnose("CLEANUP_FAILED", err.Error())
		}
	}

	finishCampaign(&r)
	var secrets []string
	for _, x := range c.Experiments {
		secrets = append(secrets, secretsFor(x.Spec)...)
	}
	evidence.RedactCampaign(&r, secrets)
	r.FinishedAt = time.Now().UTC()
	r.DurationMs = r.FinishedAt.Sub(r.StartedAt).Milliseconds()
	return r
}

// freezeSnapshot records the snapshot identity, including the digest of every
// directory an experiment evaluates, and checks that the campaign file and
// every referenced harness are the bytes that were loaded and validated.
// Workspace configurations are checked in the source because execution copies
// exclude .invaris. It returns the whole-snapshot digest and the root digests.
func freezeSnapshot(c Campaign, root string, opts workspace.Options, id *evidence.SourceIdentity) (string, map[string]string, error) {
	manifest, err := workspace.SnapshotWith(root, opts)
	if err != nil {
		return "", nil, err
	}
	digest, files := workspace.ManifestDigest(manifest), len(manifest)
	id.SnapshotSHA256, id.Files = &digest, &files
	roots := map[string]string{}
	var dirs []string
	for _, x := range c.Experiments {
		if _, seen := roots[x.Dir]; !seen {
			roots[x.Dir] = ""
			dirs = append(dirs, x.Dir)
		}
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		sub := workspace.Subtree(manifest, dir)
		roots[dir] = workspace.ManifestDigest(sub)
		id.Roots = append(id.Roots, evidence.SourceRoot{Path: dir, SHA256: roots[dir], Files: len(sub)})
	}
	same := func(name, want string) error {
		// Workspace configs are intentionally omitted from execution copies.
		// Pin their source bytes separately before executing the loaded specs.
		if path.Dir(name) == ".invaris" {
			source := filepath.Join(c.Root, filepath.FromSlash(name))
			info, err := os.Lstat(source)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%s changed after it was loaded", name)
			}
			parent, err := os.Lstat(filepath.Dir(source))
			if err != nil || !parent.IsDir() {
				return fmt.Errorf("%s changed after it was loaded", name)
			}
			f, err := os.Open(source)
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(f, maxCampaignBytes+1))
			f.Close()
			if err != nil || len(raw) > maxCampaignBytes || fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != want {
				return fmt.Errorf("%s changed after it was loaded", name)
			}
			return nil
		}
		if e, ok := manifest[name]; !ok || e.Kind != "file" || "sha256:"+e.SHA256 != want {
			return fmt.Errorf("%s changed after it was loaded", name)
		}
		return nil
	}
	errs := []error{same(c.File, c.ConfigDigest)}
	for _, x := range c.Experiments {
		errs = append(errs, same(x.Harness, x.HarnessDigest))
	}
	return digest, roots, errors.Join(errs...)
}

func verifySnapshot(root string, opts workspace.Options, digest string) error {
	manifest, err := workspace.SnapshotWith(root, opts)
	if err != nil {
		return err
	}
	if workspace.ManifestDigest(manifest) != digest {
		return errors.New("snapshot manifest differs from the recorded source identity")
	}
	return nil
}

// probeTools runs each declared version command once in the snapshot and
// records it as a "tool" observation. A failing probe is recorded, not
// refused: the output is provenance, not evidence about any experiment. A
// probe that writes into the snapshot is caught by the next snapshot check.
func probeTools(ctx context.Context, tools []Command, root string) []evidence.Observation {
	out := []evidence.Observation{}
	for _, t := range tools {
		res := execute(ctx, root, t)
		res.ID = "tool:" + t.ID
		o := observation(res, "tool", t, excerptLimit)
		if res.Status != "PASS" && len(o.Diagnostics) == 0 {
			o.Diagnostics = []domain.Diagnostic{{Code: "TOOL_PROBE_FAILED", Message: res.Detail}}
		}
		out = append(out, o)
	}
	return out
}

// preflight discovers, once per harness, the providers that selected
// guardrail experiments need, and returns why one of them cannot run. Only
// capabilities that discovery can establish are checked here.
func preflight(ctx context.Context, c Campaign, selected []bool, root string) string {
	done := map[string]bool{}
	for i, x := range c.Experiments {
		if !selected[i] || x.Kind != domain.KindGuardrail || done[x.Harness] {
			continue
		}
		done[x.Harness] = true
		spec := x.Spec
		var used []ProviderGuard
		for _, g := range spec.ProviderGuards {
			for j, y := range c.Experiments {
				if selected[j] && y.Harness == x.Harness && guardOf(spec, y.ReferenceID) == g.ID {
					used = append(used, g)
					break
				}
			}
		}
		if len(used)+len(spec.RequiredAdapters) == 0 {
			continue
		}
		spec.ProviderGuards = used
		st := discover(ctx, spec, filepath.Join(root, filepath.FromSlash(x.Dir)))
		for _, id := range spec.RequiredAdapters {
			if why := st.unavailable(id); why != "" {
				return x.Harness + ": " + why
			}
		}
		for _, g := range used {
			if why := st.unsupported(g); why != "" {
				return x.Harness + ": " + why
			}
		}
	}
	return ""
}

func guardOf(spec Specification, challengeID string) string {
	for _, c := range spec.Challenges {
		if c.ID == challengeID {
			return c.Guard
		}
	}
	return ""
}

func runAttempts(ctx context.Context, c Campaign, selected []bool, parallel int, root, digest string, roots map[string]string,
	r *evidence.CampaignReport, diagnose func(string, string)) {
	type job struct{ experiment, index int }
	var jobs []job
	for i, x := range c.Experiments {
		if selected[i] {
			r.Experiments[i].Attempts = make([]evidence.CampaignAttempt, x.Repeat)
			for n := 1; n <= x.Repeat; n++ {
				jobs = append(jobs, job{i, n})
			}
		}
	}
	// Workers take jobs in declaration order. With one worker this is the
	// sequential lifecycle. Each attempt writes only its own slot, so the
	// report keeps declaration order whatever the completion order is.
	notes := make([][2]string, len(jobs))
	var mu sync.Mutex
	next, tainted := 0, false
	work := func() {
		for {
			mu.Lock()
			if next >= len(jobs) {
				mu.Unlock()
				return
			}
			k, j := next, jobs[next]
			next++
			wasTainted := tainted
			mu.Unlock()

			x := c.Experiments[j.experiment]
			a := evidence.CampaignAttempt{ID: evidence.AttemptID(x.ID, j.index), Index: j.index, Observations: []evidence.Observation{}}
			switch {
			case ctx.Err() != nil:
				a.Execution, a.Reason, a.Detail = domain.NotStarted, domain.ReasonCanceled, "not started: "+ctx.Err().Error()
			case wasTainted:
				a.Execution, a.Reason, a.Detail = domain.Refused, domain.ReasonWorkspaceChanged, "not started: the campaign snapshot changed earlier"
			default:
				if err := verifySnapshot(root, c.Workspace, digest); err != nil {
					a.Execution, a.Reason, a.Detail = domain.Refused, domain.ReasonWorkspaceChanged, "not started: "+err.Error()
					notes[k] = [2]string{"SNAPSHOT_CHANGED", a.ID + ": " + err.Error()}
				} else {
					a = runAttempt(ctx, x, filepath.Join(root, filepath.FromSlash(x.Dir)), roots[x.Dir], a)
					if a.Execution == domain.Refused {
						notes[k] = [2]string{"SOURCE_IDENTITY_MISMATCH", a.ID + ": " + a.Detail}
					}
				}
				if a.Execution == domain.Refused {
					mu.Lock()
					tainted = true
					mu.Unlock()
				}
			}
			a.ReasonCategory = domain.ReasonCategory(a.Reason)
			r.Experiments[j.experiment].Attempts[j.index-1] = a
		}
	}
	var wg sync.WaitGroup
	for range max(1, min(parallel, len(jobs))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	wg.Wait()
	for _, n := range notes {
		if n[0] != "" {
			diagnose(n[0], n[1])
		}
	}
}

// runAttempt runs one attempt through the existing single-experiment code and
// copies its verdict, reason and evidence. The child's verdict rules are the
// only authority; this function never reinterprets them.
func runAttempt(ctx context.Context, x CampaignExperiment, root, digest string, a evidence.CampaignAttempt) evidence.CampaignAttempt {
	started := time.Now().UTC()
	var snapshot *domain.SourceSnapshot
	switch x.Kind {
	case domain.KindMutation:
		m, observations := runMutation(ctx, x.Spec, root, prepareFunc(ctx, x.Spec.Workspace), (*workspace.Workspace).Close)
		m.EngineVersion = engineVersion()
		a.Mutation, a.Observations = &m, observations
		v := string(m.Verdict)
		a.Verdict, a.Reason, snapshot = &v, m.Reason, m.SourceSnapshot
		for _, s := range m.Stages {
			if s.Status == domain.StageFailed {
				a.Detail = s.Name + ": " + s.Detail
			}
		}
	case domain.KindGuardrail:
		p, observations, ok := runChallengeEvidence(ctx, x.Spec, root, x.ReferenceID)
		if !ok {
			a.Execution, a.Reason, a.Detail = domain.Refused, domain.ReasonMalformedEvidence, "challenge produced no proof"
			return a
		}
		a.Proof, a.Observations = &p, observations
		v := string(p.Status)
		a.Verdict, a.Reason, a.Detail, snapshot = &v, p.Reason, p.Detail, p.SourceSnapshot
	}
	if a.Observations == nil {
		a.Observations = []evidence.Observation{}
	}
	if snapshot != nil && snapshot.SHA256 != digest {
		// The attempt evaluated other content than the recorded identity; its
		// evidence is not about this campaign and is dropped.
		return evidence.CampaignAttempt{ID: a.ID, Index: a.Index, Execution: domain.Refused, Reason: domain.ReasonWorkspaceChanged,
			Detail: "attempt evaluated source snapshot " + snapshot.SHA256 + ", not the campaign snapshot", Observations: []evidence.Observation{}}
	}
	finished := time.Now().UTC()
	a.StartedAt, a.FinishedAt, a.DurationMs = &started, &finished, finished.Sub(started).Milliseconds()
	a.Execution = domain.Executed
	if a.Reason == domain.ReasonCanceled {
		a.Execution = domain.Canceled
	}
	return a
}

// finishCampaign derives the aggregates, summaries and decision from the
// attempts, in the same way ValidateCampaign recomputes them.
func finishCampaign(r *evidence.CampaignReport) {
	for i := range r.Experiments {
		x := &r.Experiments[i]
		outcomes := make([]domain.AttemptOutcome, len(x.Attempts))
		for j, a := range x.Attempts {
			outcomes[j].Execution = a.Execution
			if a.Verdict != nil {
				outcomes[j].Verdict = *a.Verdict
			}
		}
		verdict, stability, reason := domain.Aggregate(x.Kind, outcomes)
		x.Stability, x.StabilityReason = stability, reason
		if verdict != "" {
			x.AggregateVerdict = &verdict
		}
	}
	outcomes := r.Outcomes()
	r.MutationSummary = domain.SummarizeMutations(outcomes)
	r.GuardrailSummary = domain.SummarizeGuardrails(outcomes)
	r.AcceptanceEvaluation, r.Decision = domain.EvaluateAcceptance(r.AcceptancePolicy, outcomes, r.Complete())
	if !r.AcceptanceEvaluation.Complete {
		var ids []string
		for _, x := range r.Experiments {
			for _, a := range x.Attempts {
				if x.Selection == domain.Selected && a.Execution != domain.Executed {
					ids = append(ids, a.ID)
				}
			}
		}
		if len(ids) > 0 {
			r.Diagnostics = append(r.Diagnostics, domain.Diagnostic{Code: "CAMPAIGN_INCOMPLETE", Message: "attempts that did not execute: " + strings.Join(ids, ", ")})
		}
	}
}
