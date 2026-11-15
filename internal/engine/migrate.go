package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/akkaraponph/invaris/internal/domain"
)

// MigrationResult is the outcome of MigrateConfig. Bytes is the destination
// document; when Changed is false it is the source bytes, unmodified.
type MigrationResult struct {
	SourceVersion, TargetVersion string
	Changed                      bool
	Bytes                        []byte
}

// shortVersions maps the short names accepted by migrate to harness versions.
var shortVersions = map[string]string{"v1alpha1": versionV1, "v1alpha2": versionV2, "v1alpha3": versionV3}

// migratedV2 is the destination wire document. Command, guard and challenge
// lists are copied from the source as raw JSON, so every literal value is
// preserved; only the fields whose wire shape differs are rebuilt.
type migratedV2 struct {
	Version          string          `json:"version"`
	Name             string          `json:"name"`
	TrustMode        string          `json:"trustMode"`
	NetworkPolicy    string          `json:"networkPolicy"`
	FailFast         *bool           `json:"failFast,omitempty"`
	AlwaysRunGuards  *bool           `json:"alwaysRunGuards,omitempty"`
	WritablePaths    json.RawMessage `json:"writablePaths"`
	Policies         json.RawMessage `json:"policies,omitempty"`
	RequiredAdapters json.RawMessage `json:"requiredAdapters,omitempty"`
	Steps            json.RawMessage `json:"steps,omitempty"`
	Guards           json.RawMessage `json:"guards,omitempty"`
	Challenges       json.RawMessage `json:"challenges,omitempty"`
	Gate             domain.Gate     `json:"gate"`
}

// MigrateConfig converts harness bytes between versions without running
// anything. from and to are short names ("v1alpha1"); empty from means the
// detected version and empty to means v1alpha2. The only conversion is
// invaris.dev/v1alpha1 to invaris.dev/v1alpha2; a source already at the
// target is returned unchanged. The source and the result are decoded with
// the strict loader, and the result is refused unless every supported action
// would run the same commands under the same effective gate, policies,
// writable targets and scheduling.
func MigrateConfig(raw []byte, from, to string) (MigrationResult, error) {
	if to == "" {
		to = "v1alpha2"
	}
	if shortVersions[to] != versionV2 {
		return MigrationResult{}, fmt.Errorf("unsupported target version %q: the only migration target is v1alpha2", to)
	}
	if from != "" && shortVersions[from] == "" {
		return MigrationResult{}, fmt.Errorf("unsupported source version %q", from)
	}
	src, err := Decode(raw)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("source harness: %w", err)
	}
	if from != "" && shortVersions[from] != src.Version {
		return MigrationResult{}, fmt.Errorf("--from %s does not match the harness version %s", from, src.Version)
	}
	result := MigrationResult{SourceVersion: src.Version, TargetVersion: versionV2}
	switch src.Version {
	case versionV2:
		result.Bytes = raw
		return result, nil
	case versionV1:
	case versionV3:
		return MigrationResult{}, errors.New("invaris.dev/v1alpha3 is a behavioral mutation harness, a different contract from guard harnesses; it has no migration path")
	default:
		return MigrationResult{}, fmt.Errorf("no migration path from %s", src.Version)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return MigrationResult{}, err
	}
	out := migratedV2{Version: versionV2, Name: src.Name, TrustMode: "trusted-local", NetworkPolicy: "unrestricted",
		FailFast: src.FailFast, AlwaysRunGuards: src.AlwaysRunGuards,
		RequiredAdapters: fields["requiredAdapters"], Steps: fields["steps"], Guards: fields["guards"], Challenges: fields["challenges"],
		Gate: domain.Gate{RequiredGuards: []string{}, RequiredProofs: []string{}, Precedence: domain.FailureFirst, AllowWeakEvidence: true}}
	if w, ok := fields["writablePaths"]; ok {
		out.WritablePaths = w // explicit, including an empty list, is kept
	} else {
		// Omission permitted any challenge target; list exactly those targets.
		paths := []string{}
		for _, c := range src.Challenges {
			if !slices.Contains(paths, c.File) {
				paths = append(paths, c.File)
			}
		}
		if out.WritablePaths, err = json.Marshal(paths); err != nil {
			return MigrationResult{}, err
		}
	}
	if _, ok := fields["policies"]; ok {
		decls := []domain.Policy{}
		for _, id := range src.Policies {
			decls = append(decls, domain.Policy{ID: id})
		}
		if out.Policies, err = json.Marshal(decls); err != nil {
			return MigrationResult{}, err
		}
	}
	// The v1alpha1 derived gate requires every guard (run, check) or every
	// challenge (challenge) and permits weak marker evidence; materialize it.
	for _, g := range src.Guards {
		out.Gate.RequiredGuards = append(out.Gate.RequiredGuards, g.ID)
	}
	for _, c := range src.Challenges {
		out.Gate.RequiredProofs = append(out.Gate.RequiredProofs, c.ID)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return MigrationResult{}, err
	}
	dst, err := Decode(buf.Bytes())
	if err != nil {
		return MigrationResult{}, fmt.Errorf("migrated harness is invalid: %w", err)
	}
	if err := sameSemantics(src, dst); err != nil {
		return MigrationResult{}, fmt.Errorf("refusing to migrate: %w", err)
	}
	result.Changed, result.Bytes = true, buf.Bytes()
	return result, nil
}

// sameSemantics compares what each action of a guard harness would execute
// and decide. It is deliberately stricter than necessary: any difference it
// cannot explain is a refusal rather than a guess.
func sameSemantics(a, b Specification) error {
	type view struct {
		Steps, Guards     []Command
		Challenges        []Challenge
		ProviderGuards    []ProviderGuard
		RequiredAdapters  []string
		Policies          []string
		PolicyRecords     []domain.Policy
		FailFast, Always  bool
		Network           string
		RedactEnv         []string
		StoreSensitive    bool
		Gates             map[string]domain.Gate
		WritableExplicit  bool
		Writable          []string
		ChallengeWritable []bool
	}
	flag := func(p *bool) bool { return p == nil || *p }
	network := func(s Specification) string {
		if s.NetworkPolicy == "" {
			return "unrestricted"
		}
		return s.NetworkPolicy
	}
	mk := func(s Specification, legacyWritable bool) view {
		v := view{Steps: s.Steps, Guards: s.Guards, Challenges: s.Challenges, ProviderGuards: s.ProviderGuards,
			RequiredAdapters: s.RequiredAdapters, PolicyRecords: policies(s), FailFast: flag(s.FailFast), Always: flag(s.AlwaysRunGuards),
			Network: network(s), RedactEnv: s.RedactEnv, StoreSensitive: s.StoreSensitiveOutput, Gates: map[string]domain.Gate{}}
		if s.Policies != nil {
			v.Policies = append([]string{}, s.Policies...)
		}
		for _, action := range []string{"run", "check", "challenge"} {
			g := effectiveGate(s, action)
			g.RequiredGuards = append([]string{}, g.RequiredGuards...)
			g.RequiredProofs = append([]string{}, g.RequiredProofs...)
			v.Gates[action] = g
		}
		// What a challenge may write: a v1alpha1 harness without writablePaths
		// permits every declared target.
		for _, c := range s.Challenges {
			v.ChallengeWritable = append(v.ChallengeWritable, legacyWritable || slices.Contains(s.WritablePaths, c.File))
		}
		if !legacyWritable {
			v.WritableExplicit, v.Writable = true, append([]string{}, s.WritablePaths...)
		}
		return v
	}
	va := mk(a, a.Version == versionV1 && a.WritablePaths == nil)
	vb := mk(b, false)
	if va.WritableExplicit {
		// An explicit source list must be carried over exactly.
		if !slices.Equal(va.Writable, vb.Writable) {
			return errors.New("writablePaths changed")
		}
	}
	va.WritableExplicit, va.Writable, vb.WritableExplicit, vb.Writable = false, nil, false, nil
	norm := func(v *view) {
		if len(v.RequiredAdapters) == 0 {
			v.RequiredAdapters = nil
		}
		if len(v.RedactEnv) == 0 {
			v.RedactEnv = nil
		}
		if len(v.ProviderGuards) == 0 {
			v.ProviderGuards = nil
		}
		// An empty list and an omitted list run the same (no) commands.
		if len(v.Steps) == 0 {
			v.Steps = nil
		}
		if len(v.Guards) == 0 {
			v.Guards = nil
		}
		if len(v.Challenges) == 0 {
			v.Challenges = nil
		}
	}
	norm(&va)
	norm(&vb)
	if !reflect.DeepEqual(va, vb) {
		return errors.New("the effective commands, gates, policies, targets or scheduling would differ")
	}
	return nil
}
