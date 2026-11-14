package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/runner"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Call runs one provider process for one request. It sends exactly one UTF-8
// JSON request on stdin and accepts exactly one UTF-8 JSON response on stdout;
// stderr is diagnostic only. The provider's transport exit status must be 0 for
// any response to be trusted, including a policy FAIL. Native tool exit status
// travels in Check.ExitCode, where v1alpha1 allows 0 (clean) and 1 (violation).
//
// The returned error wraps ErrTransport, ErrInvalidResponse or ErrInvalidRequest,
// or is the provider's own *ProtocolError. On any error callers must refuse. A
// nil error means the response is well-formed and consistent with the request.
func Call(ctx context.Context, root string, command runner.Command, request Request) (Response, error) {
	if request.Version == "" {
		request.Version = Version
	}
	// Lists are always arrays on the wire, never null.
	if request.PolicyIDs == nil {
		request.PolicyIDs = []string{}
	}
	if request.Scope == nil {
		request.Scope = []string{}
	}
	if request.RequestID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Response{}, fmt.Errorf("%w: request ID: %v", ErrInvalidRequest, err)
		}
		request.RequestID = hex.EncodeToString(b[:])
	}
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if len(body) > MaxRequestBytes {
		return Response{}, fmt.Errorf("%w: request exceeds %d bytes", ErrInvalidRequest, MaxRequestBytes)
	}
	out := runner.RunInput(ctx, root, command, append(body, '\n'))
	if out.Err != nil {
		var exit *exec.ExitError
		switch {
		case errors.Is(out.Err, runner.ErrOutputLimit):
			return Response{}, transportErr("provider output exceeded the limit")
		case errors.As(out.Err, &exit):
			return Response{}, transportErr("provider exited with status %d%s", exit.ExitCode(), stderrNote(out.Stderr))
		default:
			return Response{}, transportErr("%v", out.Err)
		}
	}
	return parseResponse(out.Stdout, request)
}

func stderrNote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return ": " + strings.ToValidUTF8(s, "?")
}

func validateRequest(r Request) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
	}
	if r.Version != Version {
		return bad("version must be %q", Version)
	}
	if r.RequestID == "" || strings.TrimSpace(r.RequestID) != r.RequestID || !utf8.ValidString(r.RequestID) {
		return bad("requestId is required")
	}
	if !operations[r.Operation] {
		return bad("unknown operation %q", r.Operation)
	}
	seen := map[string]bool{}
	for _, id := range r.PolicyIDs {
		if id == "" || seen[id] || !utf8.ValidString(id) {
			return bad("invalid or duplicate policy ID %q", id)
		}
		seen[id] = true
	}
	for _, s := range r.Scope {
		if !validScopeEntry(s) {
			return bad("unsafe scope entry %q", s)
		}
	}
	if (r.Operation == OpCheck || r.Operation == OpMutate) && (len(r.PolicyIDs) == 0 || len(r.Scope) == 0) {
		return bad("%s requires policy IDs and a scope", r.Operation)
	}
	if r.Options != nil && (!json.Valid(r.Options) || bytes.Equal(bytes.TrimSpace(r.Options), []byte("null"))) {
		return bad("options must be valid non-null JSON")
	}
	return nil
}

// parseResponse enforces the single-response boundary and every response rule.
func parseResponse(stdout string, req Request) (Response, error) {
	if !utf8.ValidString(stdout) {
		return Response{}, invalidResponse("response is not valid UTF-8")
	}
	raw := []byte(stdout)
	if strings.TrimSpace(stdout) == "" {
		return Response{}, invalidResponse("response is empty")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return Response{}, invalidResponse("%v", err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var resp Response
	if err := d.Decode(&resp); err != nil {
		return Response{}, invalidResponse("malformed response: %v", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return Response{}, invalidResponse("response must contain exactly one JSON value")
	}
	if err := validateResponse(resp, req); err != nil {
		return Response{}, err
	}
	if resp.Error != nil {
		return resp, resp.Error
	}
	return resp, nil
}

func validateResponse(resp Response, req Request) error {
	if resp.Version != Version {
		return invalidResponse("version %q, want %q", resp.Version, Version)
	}
	if resp.RequestID != req.RequestID {
		return invalidResponse("requestId %q does not match request", resp.RequestID)
	}
	if strings.TrimSpace(resp.Provider.Name) == "" || strings.TrimSpace(resp.Provider.Version) == "" {
		return invalidResponse("provider name and version are required")
	}
	present := 0
	for _, ok := range []bool{resp.Capabilities != nil, resp.Check != nil, resp.Mutation != nil, resp.Error != nil} {
		if ok {
			present++
		}
	}
	if present != 1 {
		return invalidResponse("response must carry exactly one of capabilities, check, mutation or error")
	}
	switch {
	case resp.Error != nil:
		if !errorCodes[resp.Error.Code] {
			return invalidResponse("unknown error code %q", resp.Error.Code)
		}
	case resp.Capabilities != nil:
		if req.Operation != OpDiscover && req.Operation != OpDoctor {
			return invalidResponse("capabilities returned for %s", req.Operation)
		}
		return validateCapabilities(*resp.Capabilities)
	case resp.Check != nil:
		if req.Operation != OpCheck {
			return invalidResponse("check returned for %s", req.Operation)
		}
		return validateCheck(*resp.Check, resp.Provider, req)
	case resp.Mutation != nil:
		if req.Operation != OpMutate {
			return invalidResponse("mutation returned for %s", req.Operation)
		}
		return validateMutation(*resp.Mutation, req)
	}
	return nil
}

func validateCapabilities(c Capabilities) error {
	for name, list := range map[string][]string{"languages": c.Languages, "policyIds": c.PolicyIDs, "operations": c.Operations,
		"proofTypes": c.ProofTypes, "reportFormats": c.ReportFormats, "prerequisites": c.Prerequisites} {
		if list == nil {
			return invalidResponse("capabilities.%s must be a list", name)
		}
		seen := map[string]bool{}
		for _, v := range list {
			if v == "" || seen[v] {
				return invalidResponse("capabilities.%s has an empty or duplicate entry", name)
			}
			seen[v] = true
		}
	}
	for _, op := range c.Operations {
		if !operations[op] {
			return invalidResponse("capabilities advertise unknown operation %q", op)
		}
	}
	return nil
}

func validateCheck(c domain.Check, provider Identity, req Request) error {
	if c.ID == "" {
		return invalidResponse("check id is required")
	}
	if c.ProviderID != provider.Name {
		return invalidResponse("check providerId %q does not match provider %q", c.ProviderID, provider.Name)
	}
	requested := false
	for _, id := range req.PolicyIDs {
		requested = requested || id == c.PolicyID
	}
	if !requested {
		return invalidResponse("check policy %q was not requested", c.PolicyID)
	}
	if c.Findings == nil || c.Diagnostics == nil {
		return invalidResponse("check findings and diagnostics must be lists")
	}
	for _, d := range c.Diagnostics {
		if d.Code == "" {
			return invalidResponse("diagnostic without a code")
		}
	}
	for _, f := range c.Findings {
		switch {
		case f.PolicyID != c.PolicyID:
			return invalidResponse("finding policy %q differs from check policy %q", f.PolicyID, c.PolicyID)
		case strings.TrimSpace(f.RuleID) == "":
			return invalidResponse("finding without a native rule ID")
		case !validRelative(f.Path):
			return invalidResponse("finding path %q is not a canonical relative path", f.Path)
		case !inScope(f.Path, req.Scope):
			return invalidResponse("finding path %q is outside the requested scope", f.Path)
		}
	}
	switch c.Status {
	case domain.Pass, domain.Fail:
		if c.Inspected == nil || *c.Inspected <= 0 {
			return invalidResponse("%s without inspected targets", c.Status)
		}
		if c.ExitCode == nil {
			return invalidResponse("%s without a native exit code", c.Status)
		}
		// v1alpha1: native exit 0 is clean, exit 1 is a violation; nothing else.
		if c.Status == domain.Pass && (*c.ExitCode != 0 || len(c.Findings) != 0) {
			return invalidResponse("PASS contradicts native exit status %d with %d findings", *c.ExitCode, len(c.Findings))
		}
		if c.Status == domain.Fail && (*c.ExitCode != 1 || len(c.Findings) == 0) {
			return invalidResponse("FAIL contradicts native exit status %d with %d findings", *c.ExitCode, len(c.Findings))
		}
	case domain.Refuse:
		// A provider may state that it could not evaluate; this never accepts.
		if len(c.Findings) != 0 || len(c.Diagnostics) == 0 {
			return invalidResponse("REFUSE requires diagnostics and no findings")
		}
	default:
		return invalidResponse("check status %q is not allowed from a provider", c.Status)
	}
	return nil
}

func validateMutation(m Mutation, req Request) error {
	switch {
	case m.FaultID == "":
		return invalidResponse("mutation without a fault ID")
	case !validRelative(m.Path) || !inScope(m.Path, req.Scope):
		return invalidResponse("mutation path %q is outside the requested scope", m.Path)
	case !sha256Pattern.MatchString(m.BeforeSHA256) || !sha256Pattern.MatchString(m.AfterSHA256):
		return invalidResponse("mutation hashes must be lowercase SHA-256 hex")
	case m.BeforeSHA256 == m.AfterSHA256:
		return invalidResponse("mutation did not change the file")
	}
	return nil
}

// rejectDuplicateKeys fails on a duplicate object key at any depth, which
// encoding/json would otherwise resolve silently by keeping the last value.
func rejectDuplicateKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errors.New("JSON nesting exceeds limit")
		}
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("malformed response: %v", err)
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		seen := map[string]bool{}
		for d.More() {
			if delim == '{' {
				k, err := d.Token()
				if err != nil {
					return fmt.Errorf("malformed response: %v", err)
				}
				key := k.(string)
				if seen[key] {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = true
			}
			if err := walk(depth + 1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return walk(0)
}
