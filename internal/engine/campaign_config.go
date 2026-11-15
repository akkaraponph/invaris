package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akkaraponph/invaris/internal/domain"
)

// CampaignVersion is the campaign input contract.
const CampaignVersion = "invaris.campaign/v1alpha1"

const (
	maxCampaignBytes       = 1 << 20
	maxCampaignExperiments = 64
	maxCampaignRepeat      = 5
	maxCampaignTools       = 16
)

var campaignIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Campaign is a validated invaris.campaign/v1alpha1 file. Root is the
// canonical directory that holds the campaign file and every referenced
// harness; it is the source copied into the campaign snapshot.
type Campaign struct {
	Name, Root, ConfigDigest string
	File                     string // base name of the campaign file
	Experiments              []CampaignExperiment
	Acceptance               domain.AcceptancePolicy
	// Tools are version commands run once in the snapshot before any attempt.
	// Their output is recorded for provenance and never affects the decision.
	Tools []Command
}

// CampaignExperiment is one campaign entry with its loaded, validated harness.
// ReferenceID is the mutation experiment ID or the challenge ID. Dir is the
// harness's directory relative to the campaign root ("." for the root): the
// experiment evaluates that directory, as `mutate` or `challenge` would.
type CampaignExperiment struct {
	ID            string
	Kind          domain.ExperimentKind
	Harness       string
	Dir           string
	HarnessDigest string
	ReferenceID   string
	Repeat        int
	Spec          Specification
}

type campaignWire struct {
	Version       string              `json:"version"`
	Name          string              `json:"name"`
	TrustMode     string              `json:"trustMode"`
	NetworkPolicy string              `json:"networkPolicy"`
	Experiments   []campaignEntryWire `json:"experiments"`
	Acceptance    *acceptanceWire     `json:"acceptance"`
	Tools         []Adapter           `json:"tools"`
}

type campaignEntryWire struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Harness      string  `json:"harness"`
	ExperimentID *string `json:"experimentId"`
	ChallengeID  *string `json:"challengeId"`
	Repeat       int     `json:"repeat"`
}

type acceptanceWire struct {
	RequireNoSurvivors         *bool    `json:"requireNoSurvivors"`
	RequireAllGuardrailsProven *bool    `json:"requireAllGuardrailsProven"`
	RequireNoInconclusive      *bool    `json:"requireNoInconclusive"`
	RequireNoMixed             *bool    `json:"requireNoMixed"`
	RequireNoInvalid           *bool    `json:"requireNoInvalid"`
	MinMutationScore           *float64 `json:"minMutationScore"`
}

// LoadCampaign reads, decodes and validates a campaign and every harness it
// references. Nothing is executed. Each distinct harness is parsed once.
func LoadCampaign(path string) (Campaign, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Campaign{}, err
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return Campaign{}, fmt.Errorf("campaign directory: %w", err)
	}
	file := filepath.Base(abs)
	if info, err := os.Lstat(filepath.Join(root, file)); err != nil {
		return Campaign{}, err
	} else if !info.Mode().IsRegular() {
		return Campaign{}, errors.New("campaign file must be a regular file, not a symbolic link")
	}
	f, err := os.Open(filepath.Join(root, file))
	if err != nil {
		return Campaign{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCampaignBytes+1))
	if err != nil {
		return Campaign{}, err
	}
	if len(raw) > maxCampaignBytes {
		return Campaign{}, errors.New("campaign exceeds 1 MiB")
	}
	c, err := decodeCampaign(raw, root, file)
	if err != nil {
		return Campaign{}, fmt.Errorf("campaign: %w", err)
	}
	return c, nil
}

func decodeCampaign(raw []byte, root, file string) (Campaign, error) {
	var in campaignWire
	if err := decodeStrict(raw, &in); err != nil {
		return Campaign{}, fmt.Errorf("parse: %w", err)
	}
	if in.Version != CampaignVersion {
		return Campaign{}, fmt.Errorf("unsupported version %q", in.Version)
	}
	if strings.TrimSpace(in.Name) == "" {
		return Campaign{}, errors.New("name is required")
	}
	if in.TrustMode != "trusted-local" {
		return Campaign{}, errors.New(`trustMode must be "trusted-local"`)
	}
	if in.NetworkPolicy != "unrestricted" {
		return Campaign{}, errors.New(`networkPolicy must be "unrestricted"`)
	}
	if len(in.Experiments) == 0 || len(in.Experiments) > maxCampaignExperiments {
		return Campaign{}, fmt.Errorf("experiments must contain 1 to %d entries", maxCampaignExperiments)
	}
	c := Campaign{Name: in.Name, Root: root, File: file, ConfigDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(raw))}
	specs := map[string]Specification{}
	ids, refs := map[string]bool{}, map[string]bool{}
	kinds := map[domain.ExperimentKind]bool{}
	for _, e := range in.Experiments {
		if !campaignIDPattern.MatchString(e.ID) {
			return Campaign{}, fmt.Errorf("experiment ID %q must match %s", e.ID, campaignIDPattern)
		}
		if ids[e.ID] {
			return Campaign{}, fmt.Errorf("duplicate experiment ID %q", e.ID)
		}
		ids[e.ID] = true
		if e.Repeat < 1 || e.Repeat > maxCampaignRepeat {
			return Campaign{}, fmt.Errorf("experiment %s: repeat must be 1 to %d", e.ID, maxCampaignRepeat)
		}
		if err := campaignHarnessName(e.Harness, file); err != nil {
			return Campaign{}, fmt.Errorf("experiment %s: %w", e.ID, err)
		}
		spec, ok := specs[e.Harness]
		if !ok {
			var err error
			if spec, err = loadCampaignHarness(root, e.Harness); err != nil {
				return Campaign{}, fmt.Errorf("experiment %s: harness %s: %w", e.ID, e.Harness, err)
			}
			specs[e.Harness] = spec
		}
		x := CampaignExperiment{ID: e.ID, Kind: domain.ExperimentKind(e.Kind), Harness: e.Harness, Dir: path.Dir(e.Harness),
			HarnessDigest: spec.ConfigDigest, Repeat: e.Repeat, Spec: spec}
		switch x.Kind {
		case domain.KindMutation:
			if e.ExperimentID == nil || e.ChallengeID != nil {
				return Campaign{}, fmt.Errorf("experiment %s: a mutation entry needs experimentId and no challengeId", e.ID)
			}
			if spec.Version != versionV3 {
				return Campaign{}, fmt.Errorf("experiment %s: harness %s is not an %s mutation harness", e.ID, e.Harness, versionV3)
			}
			if spec.Experiment.ID != *e.ExperimentID {
				return Campaign{}, fmt.Errorf("experiment %s: harness %s has no mutation experiment %q", e.ID, e.Harness, *e.ExperimentID)
			}
			x.ReferenceID = *e.ExperimentID
		case domain.KindGuardrail:
			if e.ChallengeID == nil || e.ExperimentID != nil {
				return Campaign{}, fmt.Errorf("experiment %s: a guardrail entry needs challengeId and no experimentId", e.ID)
			}
			if spec.Version != versionV2 && spec.Version != versionV4 {
				return Campaign{}, fmt.Errorf("experiment %s: harness %s is not an %s challenge harness (or %s)", e.ID, e.Harness, versionV2, versionV4)
			}
			if err := structuredChallenge(spec, *e.ChallengeID); err != nil {
				return Campaign{}, fmt.Errorf("experiment %s: %w", e.ID, err)
			}
			x.ReferenceID = *e.ChallengeID
		default:
			return Campaign{}, fmt.Errorf("experiment %s: unknown kind %q", e.ID, e.Kind)
		}
		ref := e.Harness + "\x00" + x.ReferenceID
		if refs[ref] {
			return Campaign{}, fmt.Errorf("experiment %s: %s/%s is already referenced by another experiment", e.ID, e.Harness, x.ReferenceID)
		}
		refs[ref] = true
		kinds[x.Kind] = true
		c.Experiments = append(c.Experiments, x)
	}
	p, err := acceptancePolicy(in.Acceptance, kinds)
	if err != nil {
		return Campaign{}, err
	}
	c.Acceptance = p
	if c.Tools, err = campaignTools(in.Tools); err != nil {
		return Campaign{}, err
	}
	return c, nil
}

// campaignTools validates the version commands with the rules that apply to
// harness commands: literal arguments, an environment allowlist and a bounded
// timeout. Their IDs have their own namespace.
func campaignTools(in []Adapter) ([]Command, error) {
	if len(in) > maxCampaignTools {
		return nil, fmt.Errorf("tools must contain at most %d entries", maxCampaignTools)
	}
	var out []Command
	seen := map[string]bool{}
	for _, t := range in {
		if !campaignIDPattern.MatchString(t.ID) || seen[t.ID] {
			return nil, fmt.Errorf("tool ID %q is invalid or repeated", t.ID)
		}
		seen[t.ID] = true
		c := Command{ID: t.ID, Program: t.Program, Args: t.Args, EnvAllowlist: t.EnvAllowlist, TimeoutSeconds: t.TimeoutSeconds}
		check := Specification{Version: versionV2, Name: "tool", TrustMode: "trusted-local", NetworkPolicy: "unrestricted", Steps: []Command{c}}
		if err := check.Validate(); err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.ID, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// campaignHarnessName accepts a canonical relative slash path inside the
// campaign directory. Directories the snapshot skips cannot hold a harness.
func campaignHarnessName(name, campaignFile string) error {
	if validatePath(name) != nil {
		return fmt.Errorf("harness %q must be a relative path inside the campaign directory", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".git" || part == ".invaris" {
			return fmt.Errorf("harness %q is inside a directory the snapshot excludes", name)
		}
	}
	if name == campaignFile {
		return errors.New("harness cannot be the campaign file itself")
	}
	return nil
}

// loadCampaignHarness refuses symbolic links anywhere on the path, so the
// harness and its directory are the ones the snapshot copies.
func loadCampaignHarness(root, name string) (Specification, error) {
	parts := strings.Split(name, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return Specification{}, err
		}
		last := i == len(parts)-1
		if last && !info.Mode().IsRegular() {
			return Specification{}, errors.New("not a regular file (symbolic links are not followed)")
		}
		if !last && !info.IsDir() {
			return Specification{}, fmt.Errorf("%s is not a directory (symbolic links are not followed)", strings.Join(parts[:i+1], "/"))
		}
	}
	return Load(current)
}

// structuredChallenge requires the challenge to exist and its guard to declare
// a policy, so a proof can only be strong evidence. Marker guards are refused.
func structuredChallenge(spec Specification, id string) error {
	for _, c := range spec.Challenges {
		if c.ID != id {
			continue
		}
		for _, g := range spec.Guards {
			if g.ID == c.Guard && g.PolicyID != "" {
				return nil
			}
		}
		for _, g := range spec.ProviderGuards {
			if g.ID == c.Guard {
				return nil
			}
		}
		return fmt.Errorf("challenge %q uses guard %q without a policyId; campaigns require structured evidence", id, c.Guard)
	}
	return fmt.Errorf("harness has no challenge %q", id)
}

func acceptancePolicy(in *acceptanceWire, kinds map[domain.ExperimentKind]bool) (domain.AcceptancePolicy, error) {
	if in == nil || in.RequireNoSurvivors == nil || in.RequireAllGuardrailsProven == nil || in.RequireNoInconclusive == nil || in.RequireNoMixed == nil {
		return domain.AcceptancePolicy{}, errors.New("acceptance must set requireNoSurvivors, requireAllGuardrailsProven, requireNoInconclusive and requireNoMixed")
	}
	p := domain.AcceptancePolicy{
		RequireNoSurvivors: *in.RequireNoSurvivors, RequireAllGuardrailsProven: *in.RequireAllGuardrailsProven,
		RequireNoInconclusive: *in.RequireNoInconclusive, RequireNoMixed: *in.RequireNoMixed,
		MinMutationScore: in.MinMutationScore,
	}
	if in.RequireNoInvalid != nil {
		p.RequireNoInvalid = *in.RequireNoInvalid
	}
	if !p.RequireNoSurvivors && !p.RequireAllGuardrailsProven && !p.RequireNoInconclusive && !p.RequireNoMixed && !p.RequireNoInvalid && p.MinMutationScore == nil {
		return p, errors.New("acceptance must enable at least one requirement")
	}
	if s := p.MinMutationScore; s != nil && (*s <= 0 || *s > 1) {
		return p, errors.New("minMutationScore must be greater than 0 and at most 1")
	}
	if (p.RequireNoSurvivors || p.RequireNoInvalid || p.MinMutationScore != nil) && !kinds[domain.KindMutation] {
		return p, errors.New("a mutation requirement is enabled but the campaign has no mutation experiment")
	}
	if p.RequireAllGuardrailsProven && !kinds[domain.KindGuardrail] {
		return p, errors.New("requireAllGuardrailsProven is enabled but the campaign has no guardrail experiment")
	}
	return p, nil
}

// Select returns one flag per experiment. An empty only selects everything;
// otherwise each ID must name a campaign experiment exactly once.
func (c Campaign) Select(only []string) ([]bool, error) {
	selected := make([]bool, len(c.Experiments))
	if len(only) == 0 {
		for i := range selected {
			selected[i] = true
		}
		return selected, nil
	}
	index := map[string]int{}
	for i, e := range c.Experiments {
		index[e.ID] = i
	}
	seen := map[string]bool{}
	for _, id := range only {
		i, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("--only %q names no experiment in the campaign", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("--only %q is repeated", id)
		}
		seen[id] = true
		selected[i] = true
	}
	return selected, nil
}
