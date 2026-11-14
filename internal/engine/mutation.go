package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/runner"
	"github.com/akkaraponph/invaris/internal/workspace"
)

func runMutationEvidence(ctx context.Context, spec Specification, root string) evidence.Report {
	started := time.Now().UTC()
	if err := spec.Validate(); err != nil || !spec.SupportsAction("mutate") {
		message := "mutate requires a valid v1alpha3 harness"
		if err != nil {
			message = err.Error()
		}
		// Direct callers cannot dispatch invalid experiments. The CLI checks
		// the input before dispatch and rejects this case without a report.
		return evidence.Report{Decision: domain.RefuseDecision, Diagnostics: []domain.Diagnostic{
			{Code: "INVALID_MUTATION_HARNESS", Message: message},
		}}
	}
	experiment, observations := runMutation(ctx, spec, root, workspace.Prepare, (*workspace.Workspace).Close)
	r := evidenceMetadata(ctx, spec, root, "mutate", started, time.Now().UTC())
	r.SchemaVersion = evidence.MutationSchemaVersion
	experiment.EngineVersion = engineVersion()
	r.Gate = domain.Gate{Precedence: domain.FailureFirst}
	r.Experiment, r.Observations = &experiment, observations
	r.Decision = domain.MutationDecision(experiment.Verdict)
	evidence.RedactReport(&r, secretsFor(spec))
	return r
}

func runMutation(ctx context.Context, spec Specification, root string,
	prepare func(string) (*workspace.Workspace, error),
	cleanup func(*workspace.Workspace) error) (e evidence.MutationExperiment, observations []evidence.Observation) {
	in := spec.Experiment
	m := in.Mutation
	e = evidence.MutationExperiment{
		ID: in.ID, MutationID: m.ID, Operator: m.Operator, Target: m.Path,
		TestCommandID: in.Test.ID, ValidationCommandID: in.Validation.ID, TestIDs: slices.Clone(in.TestIDs),
		Verdict: domain.MutationInconclusive, Stages: []domain.Stage{},
	}
	defer func() { e.ReasonCategory = domain.ReasonCategory(e.Reason) }()
	record := func(name string, start time.Time, reason string, err error) {
		stage := domain.Stage{Name: name, Status: domain.StageOK, DurationMs: time.Since(start).Milliseconds()}
		if reason != "" {
			stage.Status, stage.Detail = domain.StageFailed, reason
			if err != nil {
				stage.Detail += ": " + err.Error()
			}
			e.Verdict, e.Reason = domain.MutationInconclusive, reason
		}
		e.Stages = append(e.Stages, stage)
	}
	contextEnded := func() bool {
		if err := ctx.Err(); err != nil {
			e.Verdict, e.Reason = domain.MutationInconclusive, commandFailure(err)
			return true
		}
		return false
	}

	if contextEnded() {
		return
	}
	start := time.Now()
	ws, err := prepare(root)
	if err == nil && (ws == nil || ws.Root == "" || ws.Root == root) {
		err = errors.New("unsafe mutation workspace")
	}
	if err != nil {
		record("prepare", start, "workspace_prepare_failed", err)
		return
	}
	record("prepare", start, "", nil)
	guard, statErr := newIntegrity(ws.Root)
	workspaceRoot := guard.info
	defer func() {
		start := time.Now()
		info, err := os.Lstat(ws.Root)
		if statErr != nil || err != nil || !os.SameFile(workspaceRoot, info) {
			// The known path can still be removed safely, but it cannot
			// establish that the original workspace was removed.
			err = errors.New("workspace root identity lost before cleanup")
		}
		err = errors.Join(err, closeWorkspace(ws, cleanup))
		contextEnded()
		if err != nil {
			record("cleanup", start, "cleanup_failed", err)
		} else {
			record("cleanup", start, "", nil)
		}
	}()

	// Record the root itself as well as its contents so a command cannot
	// replace it with another directory that happens to have the same files.
	run := func(kind string, cmd Command) (Result, *evidence.MutationCommandResult) {
		res := execute(ctx, ws.Root, cmd)
		res.ID = in.ID + ":" + kind
		observations = append(observations, observation(res, kind, cmd, observationLimit(spec)))
		return res, &evidence.MutationCommandResult{ObservationID: res.ID}
	}
	evaluate := func(kind string, cmd Command, before map[string]workspace.Entry) (*evidence.MutationCommandResult, bool) {
		start := time.Now()
		res, result := run(kind, cmd)
		if res.Status == "REFUSE" || res.ExitCode == nil || *res.ExitCode > 1 {
			record(kind, start, commandFailure(res.commandErr), res.commandErr)
			return result, false
		}
		var err error
		if kind == "validation" {
			result.Validity, err = parseValidityResult(res.Stdout, *res.ExitCode)
		} else {
			result.Tests, err = parseTestResult(res.Stdout, *res.ExitCode, in.TestIDs)
		}
		switch {
		case err != nil:
			record(kind, start, "malformed_evidence", err)
		case ctx.Err() != nil:
			record(kind, start, commandFailure(ctx.Err()), ctx.Err())
		default:
			if err = guard.verify(before); err != nil {
				record(kind, start, "workspace_changed", err)
			} else if kind == "baseline" && *res.ExitCode == 1 {
				record(kind, start, "unhealthy_baseline", nil)
			} else {
				record(kind, start, "", nil)
				return result, true
			}
		}
		return result, false
	}

	if contextEnded() {
		return
	}
	start = time.Now()
	before, err := guard.snapshot()
	if err = errors.Join(statErr, err); err != nil {
		record("baseline", start, "workspace_changed", err)
		return
	}
	e.SourceSnapshot = &domain.SourceSnapshot{SHA256: workspace.ManifestDigest(before), Files: len(before)}
	var ok bool
	e.Baseline, ok = evaluate("baseline", in.Test, before)
	if !ok || contextEnded() {
		return
	}

	start = time.Now()
	applied, err := workspace.ReplaceExpecting(ws.Root, m.Path, m.Before, m.After, m.PreimageSHA256)
	if err != nil {
		record("mutate", start, "mutation_rejected", err)
		return
	}
	e.Mutation = &domain.Mutation{Path: applied.Path, BeforeSHA256: applied.BeforeSHA256, AfterSHA256: applied.AfterSHA256}
	mutated, err := workspace.Snapshot(ws.Root)
	if err != nil {
		record("mutate", start, "workspace_changed", err)
		return
	}
	// Tie the post-mutation integrity reference to the mutation that was
	// recorded, including the expected content hash and unchanged file mode.
	expected := before[m.Path]
	expected.SHA256 = applied.AfterSHA256
	before[m.Path] = expected
	if len(workspace.Diff(before, mutated)) != 0 {
		record("mutate", start, "workspace_changed", errors.New("unexpected manifest after replacement"))
		return
	}
	record("mutate", start, "", nil)
	if contextEnded() {
		return
	}

	e.Validation, ok = evaluate("validation", in.Validation, mutated)
	if !ok || contextEnded() {
		return
	}
	if e.Validation.Validity == domain.MutantInvalid {
		e.Verdict, e.Reason = domain.MutationInvalid, "mutant_invalid"
		return
	}
	e.Tests, ok = evaluate("tests", in.Test, mutated)
	if !ok || contextEnded() {
		return
	}
	if exit, _ := domain.TestExit(e.Tests.Tests, e.TestIDs); exit == 1 {
		e.Verdict, e.Reason = domain.MutationKilled, "assertion_failed"
	} else {
		e.Verdict, e.Reason = domain.MutationSurvived, "all_tests_passed"
	}
	return
}

// engineVersion is the module version of this build, or nil for a
// development build whose version is unknown.
func engineVersion() *string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v := info.Main.Version
		return &v
	}
	return nil
}

func commandFailure(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, runner.ErrOutputLimit):
		return "output_limit"
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return "missing_executable"
	default:
		return "execution_failed"
	}
}
