package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akkaraponph/invaris/internal/adapter"
	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/runner"
)

// Discovery records one provider discovery. Provider and Capabilities are nil
// when the provider could not be discovered; Error then says why.
type Discovery struct {
	AdapterID    string
	Program      string
	Args         []string
	Provider     *adapter.Identity
	Capabilities *adapter.Capabilities
	Error        string
	StartedAt    time.Time
	FinishedAt   time.Time
}

type providerState struct {
	responses   map[string]adapter.Response
	failures    map[string]string
	discoveries []Discovery
}

func adapterCommand(a Adapter) runner.Command {
	return runner.Command{Program: a.Program, Args: a.Args, EnvAllowlist: a.EnvAllowlist, Timeout: time.Duration(a.TimeoutSeconds) * time.Second}
}

func adapterByID(spec Specification, id string) (Adapter, bool) {
	for _, a := range spec.Adapters {
		if a.ID == id {
			return a, true
		}
	}
	return Adapter{}, false
}

// usedAdapters lists, in declaration order, the adapters a run depends on:
// those named by a provider guard or by requiredAdapters.
func usedAdapters(spec Specification) []Adapter {
	used := map[string]bool{}
	for _, g := range spec.ProviderGuards {
		used[g.AdapterID] = true
	}
	for _, id := range spec.RequiredAdapters {
		used[id] = true
	}
	var out []Adapter
	for _, a := range spec.Adapters {
		if used[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// discover performs exactly one discover call per used provider. A provider
// whose discovery fails is recorded in failures; a provider that answers but
// reports itself unavailable stays in responses and is judged by unsupported.
func discover(ctx context.Context, spec Specification, root string) providerState {
	st := providerState{responses: map[string]adapter.Response{}, failures: map[string]string{}}
	for _, a := range usedAdapters(spec) {
		var request adapter.Request
		request.Operation = adapter.OpDiscover
		var optionConflict bool
		var haveGuard bool
		for _, g := range spec.ProviderGuards {
			if g.AdapterID != a.ID {
				continue
			}
			if !haveGuard {
				request.Options = g.Options
				haveGuard = true
			} else if !bytes.Equal(compactOptions(request.Options), compactOptions(g.Options)) {
				optionConflict = true
			}
			if !contains(request.PolicyIDs, g.PolicyID) {
				request.PolicyIDs = append(request.PolicyIDs, g.PolicyID)
			}
		}
		// Earlier providers negotiate without options, including when each
		// guard has different check options. A provider requiring scoped
		// options can use one adapter declaration per option set.
		if optionConflict {
			request.Options = nil
		}
		d := Discovery{AdapterID: a.ID, Program: a.Program, Args: a.Args, StartedAt: time.Now().UTC()}
		resp, err := adapter.Call(ctx, root, adapterCommand(a), request)
		d.FinishedAt = time.Now().UTC()
		if err != nil {
			d.Error = err.Error()
			st.failures[a.ID] = d.Error
		} else {
			id, caps := resp.Provider, *resp.Capabilities
			d.Provider, d.Capabilities = &id, &caps
			st.responses[a.ID] = resp
		}
		st.discoveries = append(st.discoveries, d)
	}
	return st
}

func compactOptions(raw json.RawMessage) []byte {
	var out bytes.Buffer
	if json.Compact(&out, raw) != nil {
		return raw
	}
	return out.Bytes()
}

// resolveProviders discovers every used provider once and returns the valid
// responses. The error joins every provider that could not be discovered or
// reports itself unavailable; it does not decide whether that provider is
// required (negotiate does).
func resolveProviders(ctx context.Context, spec Specification, root string) (map[string]adapter.Response, error) {
	st := discover(ctx, spec, root)
	var errs []error
	for _, a := range usedAdapters(spec) {
		if why, ok := st.failures[a.ID]; ok {
			errs = append(errs, fmt.Errorf("provider %q: %s", a.ID, why))
		} else if caps := st.responses[a.ID].Capabilities; !caps.Available {
			errs = append(errs, fmt.Errorf("provider %q reports itself unavailable%s", a.ID, prerequisites(caps)))
		}
	}
	return st.responses, errors.Join(errs...)
}

func prerequisites(c *adapter.Capabilities) string {
	if len(c.Prerequisites) == 0 {
		return ""
	}
	return " (prerequisites: " + strings.Join(c.Prerequisites, ", ") + ")"
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (st providerState) unavailable(id string) string {
	if why, ok := st.failures[id]; ok {
		return fmt.Sprintf("provider %q could not be discovered: %s", id, why)
	}
	if caps := st.responses[id].Capabilities; !caps.Available {
		return fmt.Sprintf("provider %q reports itself unavailable%s", id, prerequisites(caps))
	}
	return ""
}

// unsupported returns why a provider cannot run a guard, or "" when it can.
func (st providerState) unsupported(g ProviderGuard) string {
	if why := st.unavailable(g.AdapterID); why != "" {
		return why
	}
	caps := st.responses[g.AdapterID].Capabilities
	switch {
	case !contains(caps.Operations, adapter.OpCheck):
		return fmt.Sprintf("provider %q does not support the check operation", g.AdapterID)
	case !contains(caps.PolicyIDs, g.PolicyID):
		return fmt.Sprintf("provider %q does not support policy %q", g.AdapterID, g.PolicyID)
	}
	return ""
}

type negotiation struct {
	state    providerState
	refusals []Result          // required capability not met: refuse before any step
	notRun   map[string]string // optional guards the provider cannot run, with the reason
}

// negotiate verifies provider capabilities before anything is dispatched. A
// requiredAdapters entry must be discoverable and available. A provider guard
// the applied gate requires must be supported; an optional one that is not is
// NOT_RUN. A required requirement is never downgraded or run locally instead.
func negotiate(ctx context.Context, spec Specification, root, action string) negotiation {
	n := negotiation{state: discover(ctx, spec, root), notRun: map[string]string{}}
	required := map[string]bool{}
	for _, id := range effectiveGate(spec, action).RequiredGuards {
		required[id] = true
	}
	refuse := func(id, why string) {
		n.refusals = append(n.refusals, Result{ID: id, Status: "REFUSE", Detail: why})
	}
	for _, id := range spec.RequiredAdapters {
		if why, ok := n.state.failures[id]; ok {
			refuse("provider:"+id, "required provider could not be discovered: "+why)
		} else if caps := n.state.responses[id].Capabilities; !caps.Available {
			refuse("provider:"+id, "required provider reports itself unavailable"+prerequisites(caps))
		}
	}
	for _, g := range spec.ProviderGuards {
		if why := n.state.unsupported(g); why != "" {
			if required[g.ID] {
				refuse("provider-guard:"+g.ID, "required capability not met: "+why)
			} else {
				n.notRun[g.ID] = "optional provider guard not run: " + why
			}
		}
	}
	return n
}

// evaluateProviderGuard sends one check request and returns the typed check.
// A provider or protocol failure is a REFUSE check, never PASS or FAIL.
func evaluateProviderGuard(ctx context.Context, root string, guard ProviderGuard, provider Adapter) domain.Check {
	check, _ := invokeProviderGuard(ctx, root, guard, provider)
	return check
}

func invokeProviderGuard(ctx context.Context, root string, guard ProviderGuard, provider Adapter) (domain.Check, adapter.Identity) {
	resp, err := adapter.Call(ctx, root, adapterCommand(provider), adapter.Request{
		Operation: adapter.OpCheck, PolicyIDs: []string{guard.PolicyID}, Scope: guard.Scope, Options: guard.Options})
	if err != nil {
		code := "PROVIDER_PROTOCOL"
		var pe *adapter.ProtocolError
		switch {
		case errors.As(err, &pe):
			code = pe.Code
		case errors.Is(err, adapter.ErrTransport):
			code = "PROVIDER_TRANSPORT"
		}
		return domain.Check{ID: guard.ID, PolicyID: guard.PolicyID, ProviderID: provider.ID, Status: domain.Refuse,
			Findings: []domain.Finding{}, Diagnostics: []domain.Diagnostic{{Code: code, Message: err.Error()}}}, adapter.Identity{}
	}
	check := *resp.Check
	check.ID = guard.ID
	return check, resp.Provider
}

// runProviderGuard executes a negotiated provider guard as a Result.
func runProviderGuard(ctx context.Context, root string, spec Specification, st providerState, g ProviderGuard) Result {
	decl, _ := adapterByID(spec, g.AdapterID)
	started := time.Now().UTC()
	check, identity := invokeProviderGuard(ctx, root, g, decl)
	r := Result{ID: g.ID, Status: string(check.Status), ExitCode: check.ExitCode, StartedAt: started, FinishedAt: time.Now().UTC(), ProviderCheck: &check}
	r.DurationMs = r.FinishedAt.Sub(started).Milliseconds()
	discovered := st.responses[g.AdapterID].Provider
	if identity.Name != "" && !matchesDiscovery(identity, discovered) {
		// A different identity answered the check than the one discovered.
		check.Status = domain.Refuse
		check.Findings = []domain.Finding{}
		check.Diagnostics = append(check.Diagnostics, domain.Diagnostic{Code: "PROVIDER_IDENTITY_CHANGED", Message: "provider or tool identity differs from discovery"})
		r.Status, r.ProviderCheck = "REFUSE", &check
	}
	if identity.Name == "" {
		identity = discovered
	}
	r.Provider = &identity
	if r.Status == "REFUSE" {
		check.Reason = providerReason(ctx, check)
	}
	switch {
	case r.Status == "FAIL":
		r.Detail = fmt.Sprintf("policy violation: %d finding(s)", len(check.Findings))
	case r.Status != "PASS" && len(check.Diagnostics) > 0:
		r.Detail = check.Diagnostics[0].Code + ": " + check.Diagnostics[0].Message
	}
	return r
}

// providerReason classifies a refused provider check: a transport failure or
// an ended context is infrastructure; anything else is unusable evidence.
func providerReason(ctx context.Context, c domain.Check) string {
	if err := ctx.Err(); err != nil {
		return commandFailure(err)
	}
	for _, d := range c.Diagnostics {
		if d.Code == "PROVIDER_TRANSPORT" {
			return domain.ReasonExecutionFailed
		}
	}
	return domain.ReasonMalformedEvidence
}

func matchesDiscovery(actual, discovered adapter.Identity) bool {
	matches := func(value, expected *string) bool {
		return expected == nil || (value != nil && *value == *expected)
	}
	return actual.Name == discovered.Name && actual.Version == discovered.Version &&
		matches(actual.ToolName, discovered.ToolName) && matches(actual.ToolVersion, discovered.ToolVersion)
}
