// Package adapter implements the invaris.adapter/v1alpha1 protocol: one JSON
// request on a provider process's stdin, one JSON response on its stdout.
//
// A policy violation is a typed FAIL result inside a valid response. A transport
// or protocol failure is a Go error; it is never reported as a passing or failing
// check, so callers must treat any error as a refusal.
package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

const (
	Version = "invaris.adapter/v1alpha1"

	OpDiscover = "discover"
	OpCheck    = "check"
	OpMutate   = "mutate"
	OpDoctor   = "doctor"

	MaxRequestBytes = 1 << 20
)

// Protocol error codes a provider may return.
const (
	CodeInvalidRequest      = "INVALID_REQUEST"
	CodeUnsupportedProtocol = "UNSUPPORTED_PROTOCOL"
	CodeUnsupportedCap      = "UNSUPPORTED_CAPABILITY"
	CodeToolUnavailable     = "TOOL_UNAVAILABLE"
	CodeEvidenceUnavailable = "EVIDENCE_UNAVAILABLE"
	CodeExecutionTimeout    = "EXECUTION_TIMEOUT"
	CodeMalformedOutput     = "MALFORMED_OUTPUT"
	CodeInternalFailure     = "INTERNAL_PROVIDER_FAILURE"
)

var errorCodes = map[string]bool{
	CodeInvalidRequest: true, CodeUnsupportedProtocol: true, CodeUnsupportedCap: true, CodeToolUnavailable: true,
	CodeEvidenceUnavailable: true, CodeExecutionTimeout: true, CodeMalformedOutput: true, CodeInternalFailure: true,
}

var operations = map[string]bool{OpDiscover: true, OpCheck: true, OpMutate: true, OpDoctor: true}

// Request is what the engine sends. Scope entries are canonical relative paths
// ("." only as a scope root); Options is opaque to the engine.
type Request struct {
	Version   string          `json:"version"`
	RequestID string          `json:"requestId"`
	Operation string          `json:"operation"`
	PolicyIDs []string        `json:"policyIds"`
	Scope     []string        `json:"scope"`
	Options   json.RawMessage `json:"options,omitempty"`
}

type Identity struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	ToolName    *string `json:"toolName"`
	ToolVersion *string `json:"toolVersion"`
}

type Capabilities struct {
	Languages     []string `json:"languages"`
	PolicyIDs     []string `json:"policyIds"`
	Operations    []string `json:"operations"`
	ProofTypes    []string `json:"proofTypes"`
	ReportFormats []string `json:"reportFormats"`
	Available     bool     `json:"available"`
	Prerequisites []string `json:"prerequisites"`
}

type Mutation struct {
	FaultID      string `json:"faultId"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"beforeSha256"`
	AfterSHA256  string `json:"afterSha256"`
}

// ProtocolError is a provider's typed refusal to perform an operation. It
// satisfies error so Call can return it directly.
type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ProtocolError) Error() string { return "provider error " + e.Code + ": " + e.Message }

// Response carries exactly one of Capabilities (discover, doctor), Check
// (check), Mutation (mutate) or Error (any operation).
type Response struct {
	Version      string         `json:"version"`
	RequestID    string         `json:"requestId"`
	Provider     Identity       `json:"provider"`
	Capabilities *Capabilities  `json:"capabilities,omitempty"`
	Check        *domain.Check  `json:"check,omitempty"`
	Mutation     *Mutation      `json:"mutation,omitempty"`
	Error        *ProtocolError `json:"error,omitempty"`
}

var (
	// ErrTransport: the provider could not be run, exceeded a limit, timed out or
	// exited nonzero. No response is trusted.
	ErrTransport = errors.New("adapter transport failure")
	// ErrInvalidResponse: the provider ran but its response broke the protocol.
	ErrInvalidResponse = errors.New("adapter protocol violation")
	// ErrInvalidRequest: the engine-side request was malformed; nothing was run.
	ErrInvalidRequest = errors.New("invalid adapter request")
)

func transportErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrTransport, fmt.Sprintf(format, args...))
}

func invalidResponse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidResponse, fmt.Sprintf(format, args...))
}

// validRelative reports whether name is a canonical relative path: forward
// slashes, no empty, "." or ".." segments, no drive or backslash, not absolute.
func validRelative(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validScopeEntry(s string) bool { return s == "." || validRelative(s) }

// inScope reports whether path is a scope entry or lies beneath one.
func inScope(path string, scope []string) bool {
	for _, s := range scope {
		if s == "." || path == s || strings.HasPrefix(path, s+"/") {
			return true
		}
	}
	return false
}
