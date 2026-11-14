package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/akkaraponph/invaris/internal/runner"
)

// runLifecycle executes steps then guards serially. Every configured task is
// recorded in configuration order: attempted work with its outcome, and
// unattempted work as NOT_RUN with the reason it was not started.
func runLifecycle(ctx context.Context, spec Specification, root, action string, report *Report) {
	if len(spec.Guards) == 0 && (action == "check" || len(spec.Steps) == 0) {
		report.Status = "REFUSE"
		report.Results = append(report.Results, Result{ID: "guards", Status: "REFUSE", Detail: "no executable work configured"})
	}
	failFast := spec.FailFast == nil || *spec.FailFast
	alwaysGuards := spec.AlwaysRunGuards == nil || *spec.AlwaysRunGuards
	failedStep := ""
	record := func(result Result) {
		report.Results = append(report.Results, result)
		report.Status = aggregate(report.Status, result.Status)
	}
	if action == "run" {
		for _, step := range spec.Steps {
			switch {
			case ctx.Err() != nil:
				record(notRun(step.ID, "context ended before start: "+ctx.Err().Error()))
			case failedStep != "" && failFast:
				// The preceding FAIL or REFUSE already determines the report status.
				report.Results = append(report.Results, notRun(step.ID, fmt.Sprintf("failFast stopped steps after %q did not pass", failedStep)))
			default:
				result := execute(ctx, root, step)
				record(result)
				if result.Status != "PASS" && failedStep == "" {
					failedStep = step.ID
				}
			}
		}
	}
	for _, guard := range spec.Guards {
		switch {
		case ctx.Err() != nil:
			record(notRun(guard.ID, "context ended before start: "+ctx.Err().Error()))
		case failedStep != "" && !alwaysGuards:
			record(notRun(guard.ID, fmt.Sprintf("alwaysRunGuards is false and step %q did not pass", failedStep)))
		default:
			record(checkGuard(ctx, root, guard))
		}
	}
}

// notRun is a placeholder for scheduled work that was never spawned. Its
// aggregate effect is REFUSE: an unevaluated guard cannot support PASS.
func notRun(id, reason string) Result {
	return Result{ID: id, Status: "NOT_RUN", Detail: "not run: " + reason}
}

// execute runs one configured command through the shared runner and maps the
// outcome: zero exit PASS, nonzero exit FAIL, anything else REFUSE.
func execute(ctx context.Context, root string, c Command) Result {
	out := runner.Run(ctx, root, runner.Command{Program: c.Program, Args: c.Args, EnvAllowlist: c.EnvAllowlist, Timeout: time.Duration(c.TimeoutSeconds) * time.Second})
	r := Result{ID: c.ID, Status: "REFUSE", ExitCode: out.ExitCode, DurationMs: out.FinishedAt.Sub(out.StartedAt).Milliseconds()}
	// Structured guards parse stdout only. Legacy output keeps both streams,
	// stdout first; each stream is bounded separately.
	r.Output = out.Stdout
	if c.PolicyID == "" {
		r.Output += out.Stderr
	}
	var exit *exec.ExitError
	switch {
	case out.Err == nil:
		r.Status = "PASS"
	case errors.Is(out.Err, context.Canceled) || errors.Is(out.Err, context.DeadlineExceeded) || errors.Is(out.Err, runner.ErrOutputLimit):
		r.Detail = out.Err.Error()
	case errors.As(out.Err, &exit) && exit.ExitCode() >= 0:
		r.Status = "FAIL"
		r.Detail = fmt.Sprintf("exit status %d", exit.ExitCode())
	default:
		r.Detail = out.Err.Error()
	}
	return r
}

// aggregate gives actual failures precedence over inability to evaluate;
// individual results retain both.
func aggregate(current, next string) string {
	for _, status := range []string{"FAIL", "REFUSE", "INCONCLUSIVE"} {
		if current == status || next == status {
			return status
		}
	}
	if next == "NOT_RUN" {
		return aggregate(current, "REFUSE")
	}
	return "PASS"
}
