package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akkaraponph/invaris/internal/mutation"
)

func Load(path string) (Specification, error) {
	f, err := os.Open(path)
	if err != nil {
		return Specification{}, err
	}
	defer f.Close()
	const maxConfigBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Specification{}, err
	}
	if len(raw) > maxConfigBytes {
		return Specification{}, errors.New("harness exceeds 1 MiB")
	}
	return decodeSpecification(raw)
}

func decodeSpecification(raw []byte) (Specification, error) {
	if len(raw) > 1<<20 {
		return Specification{}, errors.New("harness exceeds 1 MiB")
	}
	var s Specification
	var err error
	switch peekVersion(raw) {
	case versionV3:
		s, err = decodeMutation(raw)
	case versionV2:
		s, err = decodeV2(raw)
	case versionV4:
		s, err = decodeV4(raw)
	default:
		s, err = decodeV1(raw)
	}
	if err != nil {
		return s, err
	}
	if err := s.Validate(); err != nil {
		return s, err
	}
	s.ConfigDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	return s, nil
}

func decodeV1(raw []byte) (Specification, error) {
	if err := checkConfigJSON(raw); err != nil {
		return Specification{}, fmt.Errorf("parse harness: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var s Specification
	if err := d.Decode(&s); err != nil {
		return s, fmt.Errorf("parse harness: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return s, errors.New("harness must contain exactly one JSON object")
	}
	return s, nil
}

func (s Specification) Validate() error {
	if s.Version == versionV3 {
		return s.validateMutation()
	}
	if s.Experiment != nil {
		return errors.New("experiments require invaris.dev/v1alpha3")
	}
	if s.Version != versionV1 && s.Version != versionV2 && s.Version != versionV4 {
		return fmt.Errorf("unsupported harness version %q", s.Version)
	}
	if s.Version == versionV2 || s.Version == versionV4 {
		if err := s.validateV2(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("harness name is required")
	}
	if len(s.Steps)+len(s.Guards)+len(s.ProviderGuards)+len(s.Challenges) > 128 {
		return errors.New("harness exceeds 128 tasks")
	}
	if s.NetworkPolicy != "" && s.NetworkPolicy != "unrestricted" {
		return errors.New("unsupported network policy")
	}
	if s.Version == versionV1 && (len(s.RequiredAdapters) > 0 || len(s.Adapters) > 0 || len(s.ProviderGuards) > 0) {
		return errors.New("providers require invaris.dev/v1alpha2")
	}
	policies := map[string]bool{}
	for _, id := range s.Policies {
		if id == "" || strings.TrimSpace(id) != id || policies[id] {
			return errors.New("invalid or duplicate policy")
		}
		policies[id] = true
	}
	writable := map[string]bool{}
	for _, path := range s.WritablePaths {
		if err := validatePath(path); err != nil || writable[path] {
			return fmt.Errorf("invalid writable path %q", path)
		}
		writable[path] = true
	}
	ids := map[string]bool{}
	for _, c := range append(append([]Command{}, s.Steps...), s.Guards...) {
		if c.ID == "" || strings.TrimSpace(c.ID) != c.ID || strings.TrimSpace(c.Program) == "" || strings.ContainsRune(c.Program, 0) || ids[c.ID] || c.TimeoutSeconds < 0 || c.TimeoutSeconds > 300 {
			return fmt.Errorf("invalid or duplicate command %q", c.ID)
		}
		if c.PolicyID != "" && (strings.TrimSpace(c.PolicyID) != c.PolicyID || (s.Policies != nil && !policies[c.PolicyID])) {
			return fmt.Errorf("invalid policy ID for command %q", c.ID)
		}
		if c.MinInspected < 0 || c.MinInspected > 0 && c.PolicyID == "" {
			return fmt.Errorf("minInspected for command %q requires a policyId and a positive value", c.ID)
		}
		for _, arg := range c.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("invalid argument for command %q", c.ID)
			}
		}
		envKeys := map[string]bool{}
		for _, key := range c.EnvAllowlist {
			if key == "" || strings.ContainsAny(key, "=\x00") || key == "PWD" || envKeys[key] {
				return fmt.Errorf("invalid environment key for command %q", c.ID)
			}
			envKeys[key] = true
		}
		ids[c.ID] = true
	}
	if err := s.validateProviders(ids, policies); err != nil {
		return err
	}
	guards := map[string]string{}
	for _, g := range s.Guards {
		guards[g.ID] = g.PolicyID
	}
	for _, g := range s.ProviderGuards {
		guards[g.ID] = g.PolicyID
	}
	challenges := map[string]bool{}
	for _, c := range s.Challenges {
		policy, knownGuard := guards[c.Guard]
		if c.ID == "" || strings.TrimSpace(c.ID) != c.ID || ids[c.ID] || challenges[c.ID] || !knownGuard {
			return fmt.Errorf("invalid challenge %q", c.ID)
		}
		if c.Builtin != nil {
			// Built-in operators prove only with strong, structured evidence.
			if s.Version != versionV4 {
				return fmt.Errorf("challenge %s: built-in mutations require %s", c.ID, versionV4)
			}
			if err := mutation.Validate(*c.Builtin); err != nil {
				return fmt.Errorf("challenge %s: %w", c.ID, err)
			}
			if policy == "" {
				return fmt.Errorf("challenge %s: a built-in mutation requires a guard with a policyId", c.ID)
			}
			if c.Before != "" || c.After != "" || c.ExpectedOutput != "" {
				return fmt.Errorf("challenge %s mixes mutation with before, after or expectedOutput", c.ID)
			}
		} else if c.Before == "" || c.After == "" || c.Before == c.After || (policy == "" && c.ExpectedOutput == "") {
			return fmt.Errorf("invalid challenge %q", c.ID)
		}
		if s.WritablePaths != nil && !writable[c.File] {
			return fmt.Errorf("challenge %s: target is not writable", c.ID)
		}
		if err := validatePath(c.File); err != nil {
			return fmt.Errorf("challenge %s: %w", c.ID, err)
		}
		challenges[c.ID] = true
	}
	return nil
}

func validatePath(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("unsafe relative path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe relative path %q", name)
		}
	}
	return nil
}
