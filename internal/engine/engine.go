package engine

import (
	"context"
	"strings"
	"time"

	"github.com/akkaraponph/invaris/internal/adapter"
	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
	"github.com/akkaraponph/invaris/internal/mutation"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// Specification is the validated internal form of a versioned harness. It is a deliberately narrow, versioned execution contract.
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

	// Populated by versioned decoders.
	Experiment           *MutationExperiment `json:"-"`
	TrustMode            string              `json:"-"`
	PolicyDecls          []domain.Policy     `json:"-"`
	Gate                 *domain.Gate        `json:"-"`
	RedactEnv            []string            `json:"-"`
	StoreSensitiveOutput bool                `json:"-"`
	Adapters             []Adapter           `json:"-"`
	ProviderGuards       []ProviderGuard     `json:"-"`
	// Workspace narrows the disposable copy that mutate and challenge use.
	Workspace workspace.Options `json:"-"`
}
type Command struct {
	ID             string   `json:"id"`
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	EnvAllowlist   []string `json:"envAllowlist,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	PolicyID       string   `json:"policyId,omitempty"`
	// MinInspected is a floor declared in the harness for structured guards.
	// It is checked against the guard's self-reported count, so a guard that
	// silently narrows its scope below what the author expects is refused.
	// It does not make the reported count truthful.
	MinInspected int `json:"minInspected,omitempty"`
}
type Challenge struct {
	ID             string `json:"id"`
	Guard          string `json:"guard"`
	File           string `json:"file"`
	Before         string `json:"before"`
	After          string `json:"after"`
	ExpectedOutput string `json:"expectedOutput"`
	// Builtin is set only by the v1alpha4 decoder: the challenge's edit is
	// planned by a built-in operator instead of Before and After.
	Builtin *mutation.Spec `json:"-"`
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
	// Proof is set for challenges: the staged, cleanup-verified proof record.
	Proof *domain.Proof `json:"-"`
	// Observation details used to build evidence; not part of the legacy shape.
	StartedAt  time.Time `json:"-"`
	FinishedAt time.Time `json:"-"`
	// Set for provider guards: the provider identity and its validated check.
	Provider      *adapter.Identity `json:"-"`
	ProviderCheck *domain.Check     `json:"-"`
	// Execution provenance of the command that ran: the resolved program, its
	// file hash (empty when unreadable), and the settings that applied.
	ExecPath, ExecSHA256 string
	TimeoutSeconds       int
	EnvNames             []string
	// reason is the machine-readable reason for a challenge that produced no
	// staged proof (configuration or negotiation failures).
	reason     string
	Stdout     string `json:"-"`
	Stderr     string `json:"-"`
	Truncated  bool   `json:"-"`
	commandErr error
}

// Report is the engine's working result. The CLI emits evidence.Report
// (invaris.evidence/v1alpha2) built from it by RunEvidence.
type Report struct {
	SchemaVersion string    `json:"schemaVersion"`
	ConfigDigest  string    `json:"configDigest,omitempty"`
	Harness       string    `json:"harness"`
	Action        string    `json:"action"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Results       []Result  `json:"results"`
	// Discoveries records the one discovery per provider made before any step.
	Discoveries []Discovery `json:"discoveries,omitempty"`
}

func Run(ctx context.Context, spec Specification, root, action string) Report {
	report := Report{SchemaVersion: "invaris.engine/result", ConfigDigest: spec.ConfigDigest, Harness: spec.Name, Action: action, Status: "PASS", StartedAt: time.Now().UTC(), Results: []Result{}}
	if err := spec.Validate(); err != nil {
		report.Status = "REFUSE"
		report.Results = append(report.Results, Result{ID: "config", Status: "REFUSE", Detail: err.Error()})
		report.FinishedAt = time.Now().UTC()
		return report
	}
	if spec.Version == versionV3 {
		report.Status = "REFUSE"
		report.Results = append(report.Results, Result{ID: action, Status: "REFUSE", Detail: "unsupported action for harness version"})
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
		var providers providerState
		if len(spec.Challenges) > 0 {
			// Discover only providers needed by proofs and explicitly required providers.
			proofSpec := spec
			proofSpec.ProviderGuards = nil
			for _, g := range spec.ProviderGuards {
				for _, c := range spec.Challenges {
					if c.Guard == g.ID {
						proofSpec.ProviderGuards = append(proofSpec.ProviderGuards, g)
						break
					}
				}
			}
			providers = discover(ctx, proofSpec, root)
			report.Discoveries = providers.discoveries
		}
		for _, c := range spec.Challenges {
			r := challenge(ctx, root, spec, providers, c)
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

// runChallengeEvidence runs one challenge of a harness exactly as `challenge`
// would, and returns its proof and observations. The harness gate is dropped
// because it may name other challenges; the campaign applies its own policy.
// ok is false when the evidence does not contain exactly that proof.
func runChallengeEvidence(ctx context.Context, spec Specification, root, challengeID string) (domain.Proof, []evidence.Observation, bool) {
	one := spec
	one.Gate, one.Challenges = nil, nil
	for _, c := range spec.Challenges {
		if c.ID == challengeID {
			one.Challenges = []Challenge{c}
		}
	}
	r := buildEvidence(ctx, one, root, "challenge", Run(ctx, one, root, "challenge"))
	if len(r.Proofs) != 1 || r.Proofs[0].ID != challengeID {
		return domain.Proof{}, r.Observations, false
	}
	return r.Proofs[0], r.Observations, true
}

func challenge(ctx context.Context, root string, spec Specification, providers providerState, c Challenge) (r Result) {
	started := time.Now()
	r = Result{ID: c.ID, Status: "INCONCLUSIVE", StartedAt: started.UTC()}
	defer func() { r.DurationMs = time.Since(started).Milliseconds(); r.FinishedAt = time.Now().UTC() }()
	if err := validatePath(c.File); err != nil {
		r.Detail, r.reason = err.Error(), domain.ReasonInvalidTarget
		return
	}
	for _, id := range spec.RequiredAdapters {
		if why := providers.unavailable(id); why != "" {
			r.Detail, r.reason = why, domain.ReasonProviderUnavailable
			return
		}
	}
	var runs []Result
	var evaluate func(context.Context, string) domain.Check
	var expect expectation
	for _, g := range spec.ProviderGuards {
		if g.ID != c.Guard {
			continue
		}
		if why := providers.unsupported(g); why != "" {
			r.Detail, r.reason = why, domain.ReasonProviderUnavailable
			return
		}
		expect = expectation{Policy: g.PolicyID, Rule: g.PolicyID}
		evaluate = func(ctx context.Context, workRoot string) domain.Check {
			res := runProviderGuard(ctx, workRoot, spec, providers, g)
			runs = append(runs, res)
			return *res.ProviderCheck
		}
	}
	for _, g := range spec.Guards {
		if g.ID != c.Guard {
			continue
		}
		expect = expectation{Policy: g.PolicyID, Rule: g.PolicyID}
		evaluate = func(ctx context.Context, workRoot string) domain.Check {
			res := checkGuard(ctx, workRoot, g)
			runs = append(runs, res)
			return toCheck(res, g)
		}
		if g.PolicyID == "" {
			expect.Marker = func(domain.Check) bool {
				return len(runs) > 0 && strings.Contains(runs[len(runs)-1].Output, c.ExpectedOutput)
			}
		}
	}
	if evaluate == nil {
		r.Detail, r.reason = "guard not found", domain.ReasonGuardNotFound
		return
	}
	proof := prove(ctx, root, c, expect, evaluate, prepareFunc(ctx, spec.Workspace), (*workspace.Workspace).Close)
	r.Proof = &proof
	r.Status, r.Detail = string(proof.Status), proof.Detail
	if len(runs) > 0 {
		r.Baseline = &runs[0]
	}
	if len(runs) > 1 {
		r.Challenged = &runs[1]
		r.Output = runs[1].Output
	}
	return r
}
