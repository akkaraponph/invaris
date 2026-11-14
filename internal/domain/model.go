// Package domain holds the typed policy, check, proof and gate contracts shared
// by the engine and the evidence format. It performs no I/O.
package domain

// CheckStatus is the outcome of one executed or intended check.
type CheckStatus string

const (
	Pass   CheckStatus = "PASS"
	Fail   CheckStatus = "FAIL"
	Refuse CheckStatus = "REFUSE"
	NotRun CheckStatus = "NOT_RUN"
)

// ProofStatus is the outcome of a proof that a guard detects an injected fault.
type ProofStatus string

const (
	Proven       ProofStatus = "PROVEN"
	Unproven     ProofStatus = "UNPROVEN"
	Inconclusive ProofStatus = "INCONCLUSIVE"
	ProofNotRun  ProofStatus = "NOT_RUN"
)

// Decision is the gate verdict over a set of results.
type Decision string

const (
	Accept Decision = "ACCEPT"
	Reject Decision = "REJECT"
	// RefuseDecision is the gate decision "REFUSE"; the check status of the same
	// spelling is Refuse.
	RefuseDecision Decision = "REFUSE"
)

// Evidence strengths for proofs.
const (
	StrengthWeak   = "weak"   // marker-based: output text only
	StrengthStrong = "strong" // exact policy and path correlation
)

// Gate precedence modes decide which signal wins when failures and refusals
// are both present. Every individual result is retained either way.
const (
	FailureFirst = "failure-first"
	RefusalFirst = "refusal-first"
)

type Policy struct {
	ID         string `json:"id"`
	AllowEmpty bool   `json:"allowEmpty"`
}

type Gate struct {
	RequiredGuards    []string `json:"requiredGuards"`
	RequiredProofs    []string `json:"requiredProofs"`
	Precedence        string   `json:"precedence"`
	AllowWeakEvidence bool     `json:"allowWeakEvidence"`
}

type Finding struct {
	PolicyID string `json:"policyId"`
	RuleID   string `json:"ruleId"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Check is one policy evaluation. Inspected and ExitCode are null when unknown.
type Check struct {
	ID          string       `json:"id"`
	PolicyID    string       `json:"policyId"`
	ProviderID  string       `json:"providerId"`
	Status      CheckStatus  `json:"status"`
	Inspected   *int         `json:"inspected"`
	Findings    []Finding    `json:"findings"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	ExitCode    *int         `json:"exitCode"`
	// Reason is the machine-readable failure reason for a REFUSE result. It is
	// consumed when a proof is classified and is not serialized on checks.
	Reason string `json:"-"`
}

// Proof stage names, in execution order. Cleanup always runs once a workspace
// exists, including after an earlier stage failed.
const (
	StagePrepare    = "prepare"
	StageBaseline   = "baseline"
	StageMutate     = "mutate"
	StageChallenged = "challenged"
	StageCleanup    = "cleanup"
)

// Proof stage statuses.
const (
	StageOK     = "OK"
	StageFailed = "FAILED"
)

// Mutation records the engine-managed replacement made for a proof: the one
// declared file and the SHA-256 of its bytes before and after.
type Mutation struct {
	Path         string `json:"path"`
	BeforeSHA256 string `json:"beforeSha256"`
	AfterSHA256  string `json:"afterSha256"`
}

// SourceSnapshot is a digest of the workspace manifest (paths, modes, file
// SHA-256 values) taken right after the copy. It identifies the inputs that
// were copied, including dirty and untracked files that a Git revision does
// not. It does not identify the tools, the environment or the machine.
type SourceSnapshot struct {
	SHA256 string `json:"sha256"`
	Files  int    `json:"files"`
}

// Stage is one attempted stage of a proof with its outcome and duration.
// Stages that were never reached because of an earlier failure are absent.
type Stage struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Detail     string `json:"detail"`
}

// Proof records that a guard was challenged with an injected fault. A PROVEN
// proof has a recorded mutation and a successful cleanup stage.
type Proof struct {
	ID               string      `json:"id"`
	PolicyID         string      `json:"policyId"`
	GuardID          string      `json:"guardId"`
	Target           string      `json:"target"`
	FaultID          string      `json:"faultId"`
	EvidenceStrength string      `json:"evidenceStrength"`
	Status           ProofStatus `json:"status"`
	Reason           string      `json:"reason"`
	ReasonCategory   string      `json:"reasonCategory"`
	Detail           string      `json:"detail"`
	// SourceSnapshot identifies the files copied into the disposable workspace
	// before the baseline ran; null when no workspace was prepared.
	SourceSnapshot *SourceSnapshot `json:"sourceSnapshot"`
	Mutation       *Mutation       `json:"mutation"`
	Stages         []Stage         `json:"stages"`
	Baseline       *Check          `json:"baseline"`
	Challenged     *Check          `json:"challenged"`
}
