package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Specification is a deliberately narrow, versioned execution contract.
// Arguments are never interpreted by a shell.
type Specification struct {
	FailFast         *bool       `json:"failFast,omitempty"`
	AlwaysRunGuards  *bool       `json:"alwaysRunGuards,omitempty"`
	ConfigDigest     string      `json:"-"`
	Version          string      `json:"version"`
	Name             string      `json:"name"`
	Steps            []Command   `json:"steps"`
	Guards           []Command   `json:"guards"`
	Challenges       []Challenge `json:"challenges"`
	Policies         []string    `json:"policies,omitempty"`
	NetworkPolicy    string      `json:"networkPolicy,omitempty"`
	WritablePaths    []string    `json:"writablePaths,omitempty"`
	RequiredAdapters []string    `json:"requiredAdapters,omitempty"`
}
type Command struct {
	ID             string   `json:"id"`
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	EnvAllowlist   []string `json:"envAllowlist,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	PolicyID       string   `json:"policyId,omitempty"`
}
type Challenge struct {
	ID             string `json:"id"`
	Guard          string `json:"guard"`
	File           string `json:"file"`
	Before         string `json:"before"`
	After          string `json:"after"`
	ExpectedOutput string `json:"expectedOutput"`
}
type Result struct {
	ID         string      `json:"id"`
	Status     string      `json:"status"`
	Output     string      `json:"output,omitempty"`
	Detail     string      `json:"detail,omitempty"`
	ExitCode   *int        `json:"exitCode,omitempty"`
	DurationMs int64       `json:"durationMs"`
	Inspection *Inspection `json:"inspection,omitempty"`
	Baseline   *Result     `json:"baseline,omitempty"`
	Challenged *Result     `json:"challenged,omitempty"`
}
type Report struct {
	SchemaVersion string    `json:"schemaVersion"`
	ConfigDigest  string    `json:"configDigest,omitempty"`
	Harness       string    `json:"harness"`
	Action        string    `json:"action"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Results       []Result  `json:"results"`
}

func Run(ctx context.Context, spec Specification, root, action string) Report {
	report := Report{SchemaVersion: "invaris.evidence/v1alpha1", ConfigDigest: spec.ConfigDigest, Harness: spec.Name, Action: action, Status: "PASS", StartedAt: time.Now().UTC(), Results: []Result{}}
	if err := spec.Validate(); err != nil {
		report.Status = "REFUSE"
		report.Results = append(report.Results, Result{ID: "config", Status: "REFUSE", Detail: err.Error()})
		report.FinishedAt = time.Now().UTC()
		return report
	}
	switch action {
	case "run", "check":
		runLifecycle(ctx, spec, root, action, &report)
	case "challenge":
		if len(spec.Challenges) == 0 {
			report.Status = "REFUSE"
			report.Results = append(report.Results, Result{ID: "challenges", Status: "REFUSE", Detail: "no challenges configured"})
		}
		for _, c := range spec.Challenges {
			r := challenge(ctx, root, spec.Guards, c)
			report.Results = append(report.Results, r)
			if r.Status != "PROVEN" {
				status := "INCONCLUSIVE"
				if r.Status == "UNPROVEN" {
					status = "FAIL"
				}
				report.Status = aggregate(report.Status, status)
			}
		}
	default:
		report.Status = "REFUSE"
		report.Results = append(report.Results, Result{ID: action, Status: "REFUSE", Detail: "unknown action"})
	}
	report.FinishedAt = time.Now().UTC()
	return report
}

func challenge(ctx context.Context, root string, guards []Command, c Challenge) (r Result) {
	started := time.Now()
	r = Result{ID: c.ID, Status: "INCONCLUSIVE"}
	defer func() { r.DurationMs = time.Since(started).Milliseconds() }()
	var guard Command
	for _, g := range guards {
		if g.ID == c.Guard {
			guard = g
			break
		}
	}
	if guard.ID == "" {
		r.Detail = "guard not found"
		return
	}
	if err := validatePath(c.File); err != nil {
		r.Detail = err.Error()
		return
	}
	if c.Before == "" || c.Before == c.After || (guard.PolicyID == "" && c.ExpectedOutput == "") {
		r.Detail = "invalid mutation or expected output"
		return
	}
	tmp, err := os.MkdirTemp("", "invaris-proof-*")
	if err != nil {
		r.Detail = err.Error()
		return
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			r.Status = "INCONCLUSIVE"
			r.Detail += "; cleanup failed: " + err.Error()
		}
	}()
	canonical, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		r.Detail = err.Error()
		return
	}
	counter := 0
	if err = copyWorkspace(root, canonical, &counter); err != nil {
		r.Detail = "copy workspace: " + err.Error()
		return
	}
	baseline := checkGuard(ctx, canonical, guard)
	r.Baseline = &baseline
	if baseline.Status != "PASS" {
		r.Detail = "baseline guard did not pass: " + baseline.Status
		return
	}
	target := filepath.Join(canonical, filepath.FromSlash(c.File))
	// A baseline can change the copy. Reject redirected or non-regular targets.
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil || resolved != target {
		r.Detail = "mutation target unavailable or redirected"
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 10<<20 {
		r.Detail = "mutation target is not a bounded regular file"
		return
	}
	data, err := os.ReadFile(target)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	// Exactly one occurrence prevents ambiguous mutation placement.
	if strings.Count(string(data), c.Before) != 1 {
		r.Detail = "before marker must occur exactly once"
		return r
	}
	modified := strings.Replace(string(data), c.Before, c.After, 1)
	if err = os.WriteFile(target, []byte(modified), info.Mode().Perm()); err != nil {
		r.Detail = err.Error()
		return r
	}
	mutated := checkGuard(ctx, canonical, guard)
	r.Challenged = &mutated
	if guard.PolicyID != "" {
		return classifyStructuredProof(r, guard.PolicyID, filepath.ToSlash(filepath.Clean(c.File)))
	}
	switch {
	case mutated.Status == "FAIL" && strings.Contains(mutated.Output, c.ExpectedOutput):
		r.Status = "PROVEN"
		r.Detail = "weak evidence: baseline passed; injected violation triggered expected output marker"
	case mutated.Status == "REFUSE":
		r.Detail = "guard could not execute after injection: " + mutated.Detail
	default:
		r.Status = "UNPROVEN"
		r.Detail = "injected violation did not produce the expected failing guard evidence"
	}
	r.Output = mutated.Output
	r.DurationMs = time.Since(started).Milliseconds()
	return r
}

// copyWorkspace does not follow symbolic links, deliberately refusing a challenge
// that might otherwise escape its isolated working directory.
func copyWorkspace(src, dst string, count *int) error {
	if *count > 2000 {
		return errors.New("workspace exceeds 2000 entries")
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".invaris" {
			continue
		}
		*count++
		if *count > 2000 {
			return errors.New("workspace exceeds 2000 entries")
		}
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not permitted: %s", from)
		}
		if entry.IsDir() {
			if err = os.Mkdir(to, 0700); err != nil {
				return err
			}
			if err = copyWorkspace(from, to, count); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > 10<<20 {
			return fmt.Errorf("unsupported or oversized file %s", from)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err = os.WriteFile(to, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}
