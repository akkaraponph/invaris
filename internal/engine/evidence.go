package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akkaraponph/invaris/internal/adapter"
	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/runner"
)

const (
	excerptLimit    = 4 << 10  // default bounded excerpt per stream
	sensitiveLimit  = 64 << 10 // excerpt per stream with storeSensitiveOutput
	commandProvider = "invaris-command"
)

// RunEvidence runs the harness and builds the action’s versioned report.
// Configured secrets are redacted before the report is returned. The report
// validator must accept the evidence before it can be published.
func RunEvidence(ctx context.Context, spec Specification, root, action string) evidence.Report {
	if action == "mutate" {
		return runMutationEvidence(ctx, spec, root)
	}
	legacy := Run(ctx, spec, root, action)
	return buildEvidence(ctx, spec, root, action, legacy)
}

func evidenceMetadata(ctx context.Context, spec Specification, root, action string, started, finished time.Time) evidence.Report {
	r := evidence.Report{
		SchemaVersion: evidence.SchemaVersion,
		RunID:         newRunID(),
		Action:        action,
		Target:        root,
		Harness:       spec.Name,
		Environment:   evidence.Environment{OS: runtime.GOOS, Arch: runtime.GOARCH},
		StartedAt:     started,
		FinishedAt:    finished,
		DurationMs:    finished.Sub(started).Milliseconds(),
		GitRevision:   gitRevision(ctx, root),
		GitDirty:      gitDirty(ctx, root),
		Compatibility: evidence.Compatibility{Status: "native", InputVersion: spec.Version, Warnings: []string{}},
	}
	if spec.ConfigDigest != "" {
		d := spec.ConfigDigest
		r.ConfigDigest = &d
	}
	return r
}

func buildEvidence(ctx context.Context, spec Specification, root, action string, legacy Report) evidence.Report {
	r := evidenceMetadata(ctx, spec, root, action, legacy.StartedAt, legacy.FinishedAt)
	legacyInput := spec.Version != versionV2
	if legacyInput {
		r.Compatibility.Status = "legacy"
		r.Compatibility.Warnings = []string{"v1alpha1 harness: trusted-local execution with weak marker evidence permitted for compatibility; migrate to invaris.dev/v1alpha2 for explicit gates"}
	}
	r.Gate = effectiveGate(spec, action)
	r.Policies = policies(spec)

	steps, guards, challenges := map[string]Command{}, map[string]Command{}, map[string]Challenge{}
	providerGuards := map[string]ProviderGuard{}
	for _, g := range spec.ProviderGuards {
		providerGuards[g.ID] = g
	}
	for _, d := range legacy.Discoveries {
		r.Observations = append(r.Observations, discoveryObservation(d))
	}
	for _, c := range spec.Steps {
		steps[c.ID] = c
	}
	for _, c := range spec.Guards {
		guards[c.ID] = c
	}
	for _, c := range spec.Challenges {
		challenges[c.ID] = c
	}
	limit := observationLimit(spec)
	units := 0
	count := func(c domain.Check) {
		if c.Status != domain.Pass && c.Status != domain.Fail {
			return
		}
		if c.Inspected != nil {
			units += *c.Inspected
		} else {
			units++
		}
	}
	for _, res := range legacy.Results {
		kind, cmd := "", Command{}
		switch {
		case has(steps, res.ID):
			kind, cmd = "step", steps[res.ID]
		case has(guards, res.ID):
			kind, cmd = "guard", guards[res.ID]
		case hasChallenge(challenges, res.ID):
			kind = "challenge"
		}
		if pg, ok := providerGuards[res.ID]; ok {
			kind = "provider-guard"
			cmd = Command{ID: pg.ID, PolicyID: pg.PolicyID}
		}
		event := evidence.Event{ID: res.ID, Kind: kind, Status: res.Status}
		if kind == "" {
			event.Kind = "engine"
		} else if kind == "provider-guard" {
			event.Kind = "guard" // provider guards are guards in the lifecycle
		}
		if !res.StartedAt.IsZero() {
			s, f := res.StartedAt, res.FinishedAt
			event.StartedAt, event.FinishedAt = &s, &f
		}
		r.Lifecycle = append(r.Lifecycle, event)
		switch kind {
		case "step":
			c := toCheck(res, cmd)
			count(c)
			r.Steps = append(r.Steps, c)
			r.Observations = append(r.Observations, observation(res, "step", cmd, limit))
		case "provider-guard":
			pg := providerGuards[res.ID]
			c := providerCheck(res, pg)
			count(c)
			r.Checks = append(r.Checks, c)
			r.Observations = append(r.Observations, providerObservation(res, pg, spec, limit))
		case "guard":
			c := toCheck(res, cmd)
			count(c)
			r.Checks = append(r.Checks, c)
			r.Observations = append(r.Observations, observation(res, "guard", cmd, limit))
		case "challenge":
			ch := challenges[res.ID]
			g := guards[ch.Guard]
			pg, external := providerGuards[ch.Guard]
			if external {
				g.PolicyID = pg.PolicyID
			}
			p := toProof(res, ch, g)
			if p.Status == domain.Proven || p.Status == domain.Unproven {
				units++
			}
			r.Proofs = append(r.Proofs, p)
			if p.Status != domain.Proven && res.Detail != "" {
				r.Diagnostics = append(r.Diagnostics, domain.Diagnostic{Code: "PROOF_" + res.Status, Message: res.ID + ": " + res.Detail})
			}
			appendObservation := func(result *Result, kind string) {
				if result == nil {
					return
				}
				if external {
					o := providerObservation(*result, pg, spec, limit)
					o.ID, o.Kind = res.ID+":"+kind, kind
					r.Observations = append(r.Observations, o)
				} else {
					r.Observations = append(r.Observations, observation(*result, kind, g, limit))
				}
			}
			appendObservation(res.Baseline, "baseline")
			appendObservation(res.Challenged, "challenged")
		default:
			// Results the harness did not declare (configuration, missing work,
			// unknown action) are refusals; they must be visible to the gate.
			r.Checks = append(r.Checks, domain.Check{ID: res.ID, ProviderID: commandProvider, Status: checkStatus(res.Status),
				Diagnostics: []domain.Diagnostic{{Code: "ENGINE_" + res.Status, Message: res.Detail}}})
			r.Diagnostics = append(r.Diagnostics, domain.Diagnostic{Code: "ENGINE_" + res.Status, Message: res.ID + ": " + res.Detail})
		}
	}
	r.TargetCount = &units
	r.Decision = domain.Decide(r.Gate, r.Steps, r.Checks, r.Proofs)
	evidence.RedactReport(&r, secretsFor(spec))
	if err := evidence.Validate(r); err != nil && r.Decision == domain.Accept {
		// Incomplete or inconsistent evidence can never be accepted.
		msg := err.Error()
		r.Checks = append(r.Checks, domain.Check{ID: "evidence", ProviderID: commandProvider, Status: domain.Refuse,
			Diagnostics: []domain.Diagnostic{{Code: "EVIDENCE_INCOMPLETE", Message: msg}}})
		r.Diagnostics = append(r.Diagnostics, domain.Diagnostic{Code: "EVIDENCE_INCOMPLETE", Message: msg})
		r.Decision = domain.Decide(r.Gate, r.Steps, r.Checks, r.Proofs)
	}
	return r
}

func has(m map[string]Command, id string) bool { _, ok := m[id]; return ok }

func hasChallenge(m map[string]Challenge, id string) bool { _, ok := m[id]; return ok }

// effectiveGate is the gate applied to one action. A configured gate is
// restricted to the results the action can produce: run and check evaluate
// guards, challenge evaluates proofs. An action whose applied gate requires
// nothing (and has no steps) is refused as empty work. Without a configured
// gate every declared guard (run/check) or challenge (challenge) is required.
// v1alpha1 inputs permit weak evidence for compatibility; v1alpha2 does not
// unless its gate says so.
func effectiveGate(spec Specification, action string) domain.Gate {
	if spec.Gate != nil {
		g := *spec.Gate
		if action == "challenge" {
			g.RequiredGuards = nil
		} else {
			g.RequiredProofs = nil
		}
		return g
	}
	g := domain.Gate{Precedence: domain.FailureFirst, AllowWeakEvidence: spec.Version != versionV2}
	if action == "challenge" {
		for _, c := range spec.Challenges {
			g.RequiredProofs = append(g.RequiredProofs, c.ID)
		}
	} else {
		for _, c := range spec.Guards {
			g.RequiredGuards = append(g.RequiredGuards, c.ID)
		}
		for _, c := range spec.ProviderGuards {
			g.RequiredGuards = append(g.RequiredGuards, c.ID)
		}
	}
	return g
}

func policies(spec Specification) []domain.Policy {
	out := append([]domain.Policy{}, spec.PolicyDecls...)
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.ID] = true
	}
	for _, id := range spec.Policies {
		if !seen[id] {
			seen[id] = true
			out = append(out, domain.Policy{ID: id})
		}
	}
	for _, g := range spec.Guards {
		if g.PolicyID != "" && !seen[g.PolicyID] {
			seen[g.PolicyID] = true
			out = append(out, domain.Policy{ID: g.PolicyID})
		}
	}
	for _, g := range spec.ProviderGuards {
		if !seen[g.PolicyID] {
			seen[g.PolicyID] = true
			out = append(out, domain.Policy{ID: g.PolicyID})
		}
	}
	return out
}

func checkStatus(status string) domain.CheckStatus {
	switch status {
	case "PASS":
		return domain.Pass
	case "FAIL":
		return domain.Fail
	case "NOT_RUN":
		return domain.NotRun
	}
	return domain.Refuse // REFUSE, INCONCLUSIVE and anything unrecognized
}

func diagnostics(res Result) []domain.Diagnostic {
	if res.Detail == "" {
		return nil
	}
	code := map[string]string{"FAIL": "COMMAND_FAILED", "NOT_RUN": "NOT_RUN"}[res.Status]
	if code == "" {
		code = "REFUSED"
	}
	return []domain.Diagnostic{{Code: code, Message: res.Detail}}
}

func toCheck(res Result, cmd Command) domain.Check {
	c := domain.Check{ID: res.ID, PolicyID: cmd.PolicyID, ProviderID: commandProvider, Status: checkStatus(res.Status), ExitCode: res.ExitCode, Diagnostics: diagnostics(res)}
	if c.Status != domain.Pass && c.Status != domain.Fail {
		c.Reason = resultReason(res)
	}
	if res.Inspection != nil {
		n := res.Inspection.Inspected
		c.Inspected = &n
		for _, f := range res.Inspection.Findings {
			// v1alpha1 inspections identify a policy, not a separate native rule.
			c.Findings = append(c.Findings, domain.Finding{PolicyID: f.PolicyID, RuleID: f.PolicyID, Path: f.Path})
		}
	}
	return c
}

// resultReason maps a command result that could not be evaluated to a reason:
// rejected evidence from a completed assessment, then the runner failure,
// otherwise unusable evidence.
func resultReason(res Result) string {
	if res.reason != "" {
		return res.reason // set when the command exited but its evidence was rejected
	}
	if res.commandErr != nil {
		return commandFailure(res.commandErr)
	}
	if res.Status == "REFUSE" {
		return domain.ReasonMalformedEvidence
	}
	return domain.ReasonExecutionFailed
}

func toProof(res Result, ch Challenge, guard Command) domain.Proof {
	sum := sha256.Sum256([]byte(strings.Join([]string{ch.ID, ch.File, ch.Before, ch.After}, "\x00")))
	p := domain.Proof{ID: res.ID, PolicyID: guard.PolicyID, GuardID: ch.Guard, Target: ch.File,
		FaultID: "fault-" + hex.EncodeToString(sum[:8]), EvidenceStrength: domain.StrengthWeak, Stages: []domain.Stage{}}
	if guard.PolicyID != "" {
		p.EvidenceStrength = domain.StrengthStrong
	}
	if res.Proof != nil {
		// The staged record is authoritative: it carries the mutation digests,
		// the digest-derived fault ID and the cleanup outcome.
		p = *res.Proof
		p.ID = res.ID
		if p.Stages == nil {
			p.Stages = []domain.Stage{}
		}
		return p
	}
	switch res.Status {
	case "NOT_RUN":
		p.Status = domain.ProofNotRun
	default:
		p.Status = domain.Inconclusive
	}
	p.Detail = res.Detail
	if p.Status == domain.Inconclusive {
		p.Reason = res.reason
		if p.Reason == "" {
			p.Reason = domain.ReasonExecutionFailed
		}
		p.ReasonCategory = domain.ReasonCategory(p.Reason)
	}
	return p
}

func observationLimit(spec Specification) int {
	if spec.StoreSensitiveOutput {
		return sensitiveLimit
	}
	return excerptLimit
}

func observation(res Result, kind string, cmd Command, limit int) evidence.Observation {
	o := evidence.Observation{ID: res.ID, Kind: kind, Program: cmd.Program, Args: append([]string{}, cmd.Args...),
		Provider: evidence.Provider{Name: commandProvider}, Scope: []string{}, ExitCode: res.ExitCode, Truncated: res.Truncated,
		Diagnostics: []domain.Diagnostic{}}
	if !res.StartedAt.IsZero() {
		s, f := res.StartedAt, res.FinishedAt
		o.StartedAt, o.FinishedAt = &s, &f
	}
	var cut1, cut2 bool
	o.Stdout, cut1 = excerpt(res.Stdout, limit)
	o.Stderr, cut2 = excerpt(res.Stderr, limit)
	o.Truncated = o.Truncated || cut1 || cut2
	if d := diagnostics(res); d != nil {
		o.Diagnostics = d
	}
	o.EnvAllowlist = append([]string{}, res.EnvNames...)
	if res.ExecPath != "" {
		o.Executable = &evidence.Executable{Path: res.ExecPath}
		if res.ExecSHA256 != "" {
			sum := res.ExecSHA256
			o.Executable.SHA256 = &sum
		}
	}
	if res.ExecPath != "" || res.TimeoutSeconds != 0 {
		t := res.TimeoutSeconds
		o.TimeoutSeconds = &t
	}
	return o
}

func excerpt(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}

// secretsFor reads configured redactEnv values from the process environment.
// The values are used only to redact; they are never serialized.
func secretsFor(spec Specification) []string {
	var out []string
	for _, name := range spec.RedactEnv {
		if v := os.Getenv(name); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "" // an empty ID fails validation; no report is emitted without one
	}
	return hex.EncodeToString(b[:])
}

// gitRevision does a bounded read-only lookup; unavailable means null.
func gitRevision(ctx context.Context, root string) *string {
	out := runner.Run(ctx, root, runner.Command{Program: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Timeout: 2 * time.Second})
	rev := strings.TrimSpace(out.Stdout)
	if out.Err != nil || len(rev) < 40 || len(rev) > 64 || strings.Trim(rev, "0123456789abcdef") != "" {
		return nil
	}
	return &rev
}

// gitDirty reports whether the target has uncommitted or untracked changes,
// using a bounded read-only `git status`. Unavailable means null.
func gitDirty(ctx context.Context, root string) *bool {
	out := runner.Run(ctx, root, runner.Command{Program: "git", Args: []string{"status", "--porcelain", "--untracked-files=all", "--", "."}, Timeout: 5 * time.Second})
	if out.Err != nil || out.Truncated && out.Stdout == "" {
		return nil
	}
	dirty := strings.TrimSpace(out.Stdout) != "" || out.Truncated
	return &dirty
}

// providerCheck converts a provider guard result. A guard that never ran has no
// provider check; it is reported as NOT_RUN against its adapter.
func providerCheck(res Result, g ProviderGuard) domain.Check {
	if res.ProviderCheck != nil {
		c := *res.ProviderCheck
		c.ID = res.ID
		return c
	}
	return domain.Check{ID: res.ID, PolicyID: g.PolicyID, ProviderID: g.AdapterID, Status: checkStatus(res.Status), Diagnostics: diagnostics(res)}
}

func providerObservation(res Result, g ProviderGuard, spec Specification, limit int) evidence.Observation {
	decl, _ := adapterByID(spec, g.AdapterID)
	o := observation(res, "guard", Command{Program: decl.Program, Args: decl.Args}, limit)
	o.Scope = append([]string{}, g.Scope...)
	o.EnvAllowlist = append([]string{}, decl.EnvAllowlist...)
	o.Provider = evidence.Provider{Name: g.AdapterID}
	if id := res.Provider; id != nil {
		o.Provider = identityProvider(*id)
	}
	if c := res.ProviderCheck; c != nil {
		o.Diagnostics = append(o.Diagnostics[:0:0], c.Diagnostics...)
		if o.Diagnostics == nil {
			o.Diagnostics = []domain.Diagnostic{}
		}
	}
	return o
}

func identityProvider(id adapter.Identity) evidence.Provider {
	return evidence.Provider{Name: id.Name, Version: &id.Version, ToolName: id.ToolName, ToolVersion: id.ToolVersion}
}

// discoveryObservation records the single discovery made for a provider, with
// its identity and availability, or why discovery failed.
func discoveryObservation(d Discovery) evidence.Observation {
	started, finished := d.StartedAt, d.FinishedAt
	o := evidence.Observation{ID: "discover:" + d.AdapterID, Kind: "discover", Program: d.Program, Args: append([]string{}, d.Args...),
		Provider: evidence.Provider{Name: d.AdapterID}, Scope: []string{}, StartedAt: &started, FinishedAt: &finished, Diagnostics: []domain.Diagnostic{}, EnvAllowlist: []string{}}
	if d.Provider != nil {
		o.Provider = identityProvider(*d.Provider)
	}
	switch {
	case d.Error != "":
		o.Diagnostics = append(o.Diagnostics, domain.Diagnostic{Code: "DISCOVERY_FAILED", Message: d.Error})
	case d.Capabilities != nil && !d.Capabilities.Available:
		o.Diagnostics = append(o.Diagnostics, domain.Diagnostic{Code: "PROVIDER_UNAVAILABLE", Message: "provider reports itself unavailable" + prerequisites(d.Capabilities)})
	}
	return o
}
