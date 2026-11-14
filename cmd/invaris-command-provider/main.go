// Command invaris-command-provider is a generic invaris.adapter/v1alpha1
// provider that bridges a structured command. It runs the command named in the
// guard's options and translates the command's invaris.inspection/v1alpha1
// JSON (stdout) and exit status into a typed check.
//
// It is policy-agnostic and language-agnostic: it advertises structured-command
// support only, never a language, and does not support mutation. It echoes the
// policy IDs it was asked about in discovery and verifies the child's evidence
// against the requested policy at check time. Options:
//
//	{"program": "...", "args": ["..."], "envAllowlist": ["..."], "timeoutSeconds": 0}
//
// The scope is not passed to the child (the v1alpha1 inspection contract has no
// scope input); the engine's client refuses findings outside the scope.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akkaraponph/invaris/internal/adapter"
	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/runner"
)

const (
	providerName    = "invaris-command-provider"
	providerVersion = "0.1.0"
	inspectionV1    = "invaris.inspection/v1alpha1"
)

func main() {
	ctx, stop := signalContext()
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(ctx, os.Stdin, os.Stdout, os.Stderr, cwd))
}

type options struct {
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	EnvAllowlist   []string `json:"envAllowlist"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type inspection struct {
	Version   string `json:"version"`
	PolicyID  string `json:"policyId"`
	Inspected int    `json:"inspected"`
	Findings  []struct {
		PolicyID string `json:"policyId"`
		Path     string `json:"path"`
	} `json:"findings"`
}

// run handles one request. It exits 0 for every well-formed request, including
// a policy violation; only an unparseable request, which has no request ID to
// echo, exits nonzero.
func run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, cwd string) int {
	raw, err := io.ReadAll(io.LimitReader(stdin, adapter.MaxRequestBytes+1))
	var req adapter.Request
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err != nil || len(raw) > adapter.MaxRequestBytes || !utf8.Valid(raw) || d.Decode(&req) != nil || req.RequestID == "" {
		fmt.Fprintln(stderr, "request is not a valid JSON object with a requestId")
		return 1
	}
	var tool *string // the configured command, once known
	reply := func(r adapter.Response) int {
		r.Version, r.RequestID = adapter.Version, req.RequestID
		r.Provider = adapter.Identity{Name: providerName, Version: providerVersion, ToolName: tool}
		if r.Check != nil {
			r.Check.ProviderID = providerName
		}
		out, _ := json.Marshal(r)
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fail := func(code, msg string) int {
		return reply(adapter.Response{Error: &adapter.ProtocolError{Code: code, Message: msg}})
	}

	if req.Version != adapter.Version {
		return fail(adapter.CodeUnsupportedProtocol, "only "+adapter.Version+" is supported")
	}
	switch req.Operation {
	case adapter.OpDiscover, adapter.OpDoctor:
		policies := req.PolicyIDs
		if policies == nil {
			policies = []string{}
		}
		return reply(adapter.Response{Capabilities: &adapter.Capabilities{
			Languages: []string{}, PolicyIDs: policies, Operations: []string{adapter.OpDiscover, adapter.OpCheck, adapter.OpDoctor},
			ProofTypes: []string{}, ReportFormats: []string{inspectionV1}, Available: true, Prerequisites: []string{}}})
	case adapter.OpMutate:
		return fail(adapter.CodeUnsupportedCap, "the command provider does not support mutation")
	case adapter.OpCheck:
	default:
		return fail(adapter.CodeInvalidRequest, "unknown operation")
	}

	if len(req.PolicyIDs) != 1 || len(req.Scope) == 0 {
		return fail(adapter.CodeInvalidRequest, "check requires exactly one policy and a scope")
	}
	opts, err := decodeOptions(req.Options)
	if err != nil {
		return fail(adapter.CodeInvalidRequest, err.Error())
	}
	tool = &opts.Program
	policy := req.PolicyIDs[0]
	out := runner.Run(ctx, cwd, runner.Command{Program: opts.Program, Args: opts.Args, EnvAllowlist: opts.EnvAllowlist, Timeout: time.Duration(opts.TimeoutSeconds) * time.Second})

	var exit *exec.ExitError
	switch {
	case out.Err == nil, errors.As(out.Err, &exit), errors.Is(out.Err, runner.ErrOutputLimit):
	case errors.Is(out.Err, context.DeadlineExceeded):
		return fail(adapter.CodeExecutionTimeout, "the command exceeded its timeout")
	default:
		return fail(adapter.CodeToolUnavailable, out.Err.Error())
	}
	if out.ExitCode == nil {
		return fail(adapter.CodeToolUnavailable, "the command did not report an exit status")
	}
	check := translate(policy, out, *out.ExitCode)
	check.ID = "check-" + req.RequestID[:min(len(req.RequestID), 8)]
	return reply(adapter.Response{Check: &check})
}

func decodeOptions(raw json.RawMessage) (options, error) {
	var o options
	if len(raw) == 0 {
		return o, errors.New("options with a program are required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&o); err != nil {
		return o, fmt.Errorf("invalid options: %v", err)
	}
	if strings.TrimSpace(o.Program) == "" || strings.ContainsRune(o.Program, 0) || o.TimeoutSeconds < 0 || o.TimeoutSeconds > 300 {
		return o, errors.New("options.program is required and timeoutSeconds must be 0-300")
	}
	for _, a := range o.Args {
		if strings.ContainsRune(a, 0) {
			return o, errors.New("options.args must not contain NUL")
		}
	}
	return o, nil
}

// translate turns the child's stdout and native exit status into a check. Only
// evidence that is complete and consistent with the exit status yields PASS or
// FAIL; anything else is a REFUSE that keeps the native exit status. The
// inspected count comes from the child's own report and is never invented.
func translate(policy string, out runner.Result, code int) domain.Check {
	check := domain.Check{PolicyID: policy, Status: domain.Refuse, ExitCode: &code, Findings: []domain.Finding{}, Diagnostics: []domain.Diagnostic{}}
	refuse := func(codeName, msg string) domain.Check {
		check.Diagnostics = append(check.Diagnostics, domain.Diagnostic{Code: codeName, Message: msg})
		return check
	}
	if out.Truncated || errors.Is(out.Err, runner.ErrOutputLimit) {
		return refuse("OUTPUT_LIMIT", "the command output exceeded the limit")
	}
	dec := json.NewDecoder(strings.NewReader(out.Stdout))
	dec.DisallowUnknownFields()
	var ev inspection
	if dec.Decode(&ev) != nil {
		return refuse("NO_INSPECTION_EVIDENCE", fmt.Sprintf("the command (exit %d) did not print invaris.inspection/v1alpha1 JSON", code))
	}
	if _, err := dec.Token(); err != io.EOF {
		return refuse("NO_INSPECTION_EVIDENCE", "the command output is not exactly one JSON value")
	}
	switch {
	case ev.Version != inspectionV1:
		return refuse("INVALID_INSPECTION", "unexpected inspection version")
	case ev.PolicyID != policy:
		return refuse("INVALID_INSPECTION", fmt.Sprintf("inspection reports policy %q, expected %q", ev.PolicyID, policy))
	case ev.Inspected <= 0:
		return refuse("INVALID_INSPECTION", "inspection reports no inspected targets")
	case ev.Findings == nil:
		return refuse("INVALID_INSPECTION", "inspection findings must be a list")
	}
	var findings []domain.Finding
	for _, f := range ev.Findings {
		if f.PolicyID != policy || f.Path == "" {
			return refuse("INVALID_INSPECTION", "a finding has an unexpected policy or no path")
		}
		// The v1alpha1 contract has no separate native rule: the policy is the rule.
		findings = append(findings, domain.Finding{PolicyID: f.PolicyID, RuleID: f.PolicyID, Path: f.Path})
	}
	// Exit 0 is clean and exit 1 is a violation; the evidence must agree.
	if (code != 0 || len(findings) != 0) && (code != 1 || len(findings) == 0) {
		return refuse("EXIT_EVIDENCE_MISMATCH", fmt.Sprintf("native exit status %d contradicts %d finding(s)", code, len(findings)))
	}
	n := ev.Inspected
	check.Inspected = &n
	if len(findings) > 0 {
		check.Status, check.Findings = domain.Fail, findings
	} else {
		check.Status = domain.Pass
	}
	return check
}
