package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

const (
	versionV1 = "invaris.dev/v1alpha1"
	versionV2 = "invaris.dev/v1alpha2"
)

// Adapter declares an external provider process (v1alpha2).
type Adapter struct {
	ID             string   `json:"id"`
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	EnvAllowlist   []string `json:"envAllowlist,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

// ProviderGuard is the provider form of a v1alpha2 guard. It is decoded and
// carried so it can be reported, but is refused until providers are executable.
type ProviderGuard struct {
	ID        string          `json:"id"`
	AdapterID string          `json:"adapterId"`
	PolicyID  string          `json:"policyId"`
	Scope     []string        `json:"scope"`
	Options   json.RawMessage `json:"options,omitempty"`
}

// guardV2 is the strict union of the legacy command form and the provider form.
// The two forms are told apart by adapterId and may not mix their fields.
type guardV2 struct {
	ID             string          `json:"id"`
	Program        string          `json:"program,omitempty"`
	Args           []string        `json:"args,omitempty"`
	EnvAllowlist   []string        `json:"envAllowlist,omitempty"`
	TimeoutSeconds int             `json:"timeoutSeconds,omitempty"`
	PolicyID       string          `json:"policyId,omitempty"`
	MinInspected   int             `json:"minInspected,omitempty"`
	AdapterID      string          `json:"adapterId,omitempty"`
	Scope          []string        `json:"scope,omitempty"`
	Options        json.RawMessage `json:"options,omitempty"`
}

type specV2 struct {
	FailFast             *bool           `json:"failFast,omitempty"`
	AlwaysRunGuards      *bool           `json:"alwaysRunGuards,omitempty"`
	Version              string          `json:"version"`
	Name                 string          `json:"name"`
	TrustMode            string          `json:"trustMode"`
	NetworkPolicy        string          `json:"networkPolicy"`
	WritablePaths        []string        `json:"writablePaths,omitempty"`
	Policies             []domain.Policy `json:"policies,omitempty"`
	Gate                 *domain.Gate    `json:"gate,omitempty"`
	RedactEnv            []string        `json:"redactEnv,omitempty"`
	StoreSensitiveOutput bool            `json:"storeSensitiveOutput,omitempty"`
	Adapters             []Adapter       `json:"adapters,omitempty"`
	RequiredAdapters     []string        `json:"requiredAdapters,omitempty"`
	Steps                []Command       `json:"steps,omitempty"`
	Guards               []guardV2       `json:"guards,omitempty"`
	Challenges           []Challenge     `json:"challenges,omitempty"`
}

func peekVersion(raw []byte) string {
	var v struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Version
}

func decodeV2(raw []byte) (Specification, error) {
	if err := checkConfigJSONType(raw, reflect.TypeOf(specV2{})); err != nil {
		return Specification{}, fmt.Errorf("parse harness: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var in specV2
	if err := d.Decode(&in); err != nil {
		return Specification{}, fmt.Errorf("parse harness: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Specification{}, errors.New("harness must contain exactly one JSON object")
	}
	return buildV2(in)
}

// buildV2 converts the v1alpha2 wire form (also used by v1alpha4 after its
// challenges are converted) to the internal specification.
func buildV2(in specV2) (Specification, error) {
	s := Specification{
		FailFast: in.FailFast, AlwaysRunGuards: in.AlwaysRunGuards, Version: in.Version, Name: in.Name,
		Steps: in.Steps, Challenges: in.Challenges, NetworkPolicy: in.NetworkPolicy, TrustMode: in.TrustMode,
		RequiredAdapters: in.RequiredAdapters, Adapters: in.Adapters, Gate: in.Gate,
		RedactEnv: in.RedactEnv, StoreSensitiveOutput: in.StoreSensitiveOutput,
		// An explicit contract: omitted writablePaths means no mutation.
		WritablePaths: in.WritablePaths, PolicyDecls: in.Policies,
	}
	if s.WritablePaths == nil {
		s.WritablePaths = []string{}
	}
	if in.Policies != nil {
		s.Policies = []string{}
		for _, p := range in.Policies {
			s.Policies = append(s.Policies, p.ID)
		}
	}
	if s.Gate != nil && s.Gate.Precedence == "" {
		g := *s.Gate
		g.Precedence = domain.FailureFirst
		s.Gate = &g
	}
	for _, g := range in.Guards {
		if g.AdapterID == "" {
			if g.Scope != nil || g.Options != nil {
				return s, fmt.Errorf("guard %q mixes command and provider fields", g.ID)
			}
			s.Guards = append(s.Guards, Command{ID: g.ID, Program: g.Program, Args: g.Args, EnvAllowlist: g.EnvAllowlist, TimeoutSeconds: g.TimeoutSeconds, PolicyID: g.PolicyID, MinInspected: g.MinInspected})
			continue
		}
		if g.Program != "" || g.Args != nil || g.EnvAllowlist != nil || g.TimeoutSeconds != 0 || g.MinInspected != 0 {
			return s, fmt.Errorf("guard %q mixes command and provider fields", g.ID)
		}
		s.ProviderGuards = append(s.ProviderGuards, ProviderGuard{ID: g.ID, AdapterID: g.AdapterID, PolicyID: g.PolicyID, Scope: g.Scope, Options: g.Options})
	}
	return s, nil
}

// validateV2 checks the v1alpha2-only fields. Shared rules run in Validate.
func (s Specification) validateV2() error {
	if s.TrustMode != "trusted-local" {
		return errors.New(`trustMode must be "trusted-local"`)
	}
	if s.NetworkPolicy != "unrestricted" {
		return errors.New(`networkPolicy must be "unrestricted" in v1alpha2`)
	}
	seen := map[string]bool{}
	for _, p := range s.PolicyDecls {
		if seen[p.ID] {
			return errors.New("invalid or duplicate policy")
		}
		seen[p.ID] = true
	}
	redact := map[string]bool{}
	for _, key := range s.RedactEnv {
		if key == "" || strings.ContainsAny(key, "=\x00") || redact[key] {
			return fmt.Errorf("invalid redactEnv key %q", key)
		}
		redact[key] = true
	}
	if g := s.Gate; g != nil {
		if g.Precedence != domain.FailureFirst && g.Precedence != domain.RefusalFirst {
			return fmt.Errorf("gate precedence must be %q or %q", domain.FailureFirst, domain.RefusalFirst)
		}
		guards, proofs := map[string]bool{}, map[string]bool{}
		for _, c := range s.Guards {
			guards[c.ID] = true
		}
		for _, c := range s.ProviderGuards {
			guards[c.ID] = true
		}
		for _, c := range s.Challenges {
			proofs[c.ID] = true
		}
		for _, id := range g.RequiredGuards {
			if !guards[id] {
				return fmt.Errorf("gate requires unknown guard %q", id)
			}
		}
		for _, id := range g.RequiredProofs {
			if !proofs[id] {
				return fmt.Errorf("gate requires unknown challenge %q", id)
			}
		}
	}
	return nil
}

// validateProviders checks adapter declarations and provider guards. Adapter
// IDs have their own namespace; provider guard IDs share the executable
// namespace (ids) with steps and command guards.
func (s Specification) validateProviders(ids, policies map[string]bool) error {
	adapters := map[string]bool{}
	for _, a := range s.Adapters {
		if a.ID == "" || strings.TrimSpace(a.ID) != a.ID || adapters[a.ID] || strings.TrimSpace(a.Program) == "" || strings.ContainsRune(a.Program, 0) || a.TimeoutSeconds < 0 || a.TimeoutSeconds > 300 {
			return fmt.Errorf("invalid or duplicate adapter %q", a.ID)
		}
		for _, arg := range a.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("invalid argument for adapter %q", a.ID)
			}
		}
		keys := map[string]bool{}
		for _, key := range a.EnvAllowlist {
			if key == "" || strings.ContainsAny(key, "=\x00") || key == "PWD" || keys[key] {
				return fmt.Errorf("invalid environment key for adapter %q", a.ID)
			}
			keys[key] = true
		}
		adapters[a.ID] = true
	}
	required := map[string]bool{}
	for _, id := range s.RequiredAdapters {
		if !adapters[id] || required[id] {
			return fmt.Errorf("required adapter %q is not declared or is repeated", id)
		}
		required[id] = true
	}
	for _, g := range s.ProviderGuards {
		if g.ID == "" || strings.TrimSpace(g.ID) != g.ID || ids[g.ID] {
			return fmt.Errorf("invalid or duplicate guard %q", g.ID)
		}
		ids[g.ID] = true
		if !adapters[g.AdapterID] {
			return fmt.Errorf("guard %q names undeclared adapter %q", g.ID, g.AdapterID)
		}
		if g.PolicyID == "" || strings.TrimSpace(g.PolicyID) != g.PolicyID || (s.Policies != nil && !policies[g.PolicyID]) {
			return fmt.Errorf("invalid policy ID for guard %q", g.ID)
		}
		if len(g.Scope) == 0 {
			return fmt.Errorf("guard %q requires a scope", g.ID)
		}
		seen := map[string]bool{}
		for _, entry := range g.Scope {
			if (entry != "." && validatePath(entry) != nil) || seen[entry] {
				return fmt.Errorf("guard %q has an invalid or duplicate scope entry %q", g.ID, entry)
			}
			seen[entry] = true
		}
	}
	return nil
}
