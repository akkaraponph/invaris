package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/akkaraponph/invaris/internal/workspace"
)

// workspaceWire is the optional "workspace" block of a v1alpha3 mutation
// harness or a v1alpha4 challenge harness. All paths are relative to the
// harness directory.
type workspaceWire struct {
	Exclude      []string `json:"exclude,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	MaxEntries   int      `json:"maxEntries,omitempty"`
}

func (w *workspaceWire) options() workspace.Options {
	if w == nil {
		return workspace.Options{}
	}
	return workspace.Options{MaxEntries: w.MaxEntries, Exclude: w.Exclude, Dependencies: w.Dependencies}
}

// validateWorkspace checks the workspace options and that no file the engine
// edits sits where it would be neither copied nor compared.
func (s Specification) validateWorkspace(targets ...string) error {
	if err := s.Workspace.Validate(); err != nil {
		return err
	}
	for _, t := range targets {
		if s.Workspace.Excludes(t) {
			return fmt.Errorf("mutation target %q is inside an excluded workspace path", t)
		}
	}
	return nil
}

const versionV3 = "invaris.dev/v1alpha3"

type MutationExperiment struct {
	ID               string
	Mutation         Replacement
	Validation, Test Command
	TestIDs          []string
}

type Replacement struct {
	ID       string `json:"id"`
	Operator string `json:"operator"`
	Path     string `json:"path"`
	Before   string `json:"before"`
	After    string `json:"after"`
	// PreimageSHA256 optionally pins the whole target file's lowercase hex
	// SHA-256 before replacement; a mismatch rejects the mutation.
	PreimageSHA256 string `json:"preimageSha256,omitempty"`
}

// Use the executable fields of Adapter on the wire. These commands do not
// accept policyId or participate in provider discovery.
type mutationSpec struct {
	Version              string         `json:"version"`
	Name                 string         `json:"name"`
	TrustMode            string         `json:"trustMode"`
	NetworkPolicy        string         `json:"networkPolicy"`
	WritablePaths        []string       `json:"writablePaths"`
	RedactEnv            []string       `json:"redactEnv,omitempty"`
	StoreSensitiveOutput bool           `json:"storeSensitiveOutput,omitempty"`
	Workspace            *workspaceWire `json:"workspace,omitempty"`
	Experiment           struct {
		ID         string      `json:"id"`
		Mutation   Replacement `json:"mutation"`
		Validation Adapter     `json:"validation"`
		Test       Adapter     `json:"test"`
		TestIDs    []string    `json:"testIds"`
	} `json:"experiment"`
}

func decodeMutation(raw []byte) (Specification, error) {
	var in mutationSpec
	if err := decodeStrict(raw, &in); err != nil {
		return Specification{}, fmt.Errorf("parse mutation harness: %w", err)
	}
	command := func(c Adapter) Command {
		return Command{ID: c.ID, Program: c.Program, Args: c.Args, EnvAllowlist: c.EnvAllowlist, TimeoutSeconds: c.TimeoutSeconds}
	}
	return Specification{
		Version: in.Version, Name: in.Name, TrustMode: in.TrustMode, NetworkPolicy: in.NetworkPolicy,
		WritablePaths: in.WritablePaths, RedactEnv: in.RedactEnv, StoreSensitiveOutput: in.StoreSensitiveOutput,
		Workspace: in.Workspace.options(),
		Experiment: &MutationExperiment{ID: in.Experiment.ID, Mutation: in.Experiment.Mutation,
			Validation: command(in.Experiment.Validation), Test: command(in.Experiment.Test), TestIDs: in.Experiment.TestIDs},
	}, nil
}

func decodeStrict(raw []byte, dst any) error {
	if err := checkConfigJSONType(raw, reflect.TypeOf(dst).Elem()); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func validMutationID(id string) bool {
	return id != "" && id == strings.TrimSpace(id)
}

func (s Specification) validateMutation() error {
	e := s.Experiment
	if e == nil || !validMutationID(e.ID) || !validMutationID(e.Mutation.ID) {
		return errors.New("experiment and mutation IDs are required")
	}
	if len(s.Steps)+len(s.Guards)+len(s.Challenges)+len(s.Adapters)+len(s.RequiredAdapters)+len(s.ProviderGuards) != 0 ||
		s.Policies != nil || s.PolicyDecls != nil || s.Gate != nil || s.FailFast != nil || s.AlwaysRunGuards != nil ||
		e.Validation.PolicyID != "" || e.Test.PolicyID != "" {
		return errors.New("guard and workflow fields are outside the mutation harness")
	}
	m := e.Mutation
	if m.Operator != "replace" || m.Before == "" || m.After == "" || m.Before == m.After {
		return errors.New("mutation must specify a nonempty, distinct replacement")
	}
	if err := validatePath(m.Path); err != nil {
		return err
	}
	if d := m.PreimageSHA256; d != "" && (len(d) != 64 || strings.Trim(d, "0123456789abcdef") != "") {
		return errors.New("preimageSha256 must be 64 lowercase hex characters")
	}
	if !slices.Contains(s.WritablePaths, m.Path) {
		return errors.New("mutation target is outside writablePaths")
	}
	if err := s.validateWorkspace(m.Path); err != nil {
		return err
	}
	if len(e.TestIDs) < 1 || len(e.TestIDs) > 128 {
		return errors.New("testIds must contain 1 to 128 IDs")
	}
	seen := make(map[string]bool, len(e.TestIDs))
	for _, id := range e.TestIDs {
		if !validMutationID(id) || seen[id] {
			return errors.New("invalid or duplicate test ID")
		}
		seen[id] = true
	}
	// Reuse native checks for trusted execution, paths, redaction and commands.
	s.Version, s.Experiment, s.Workspace = versionV2, nil, workspace.Options{}
	s.Steps = []Command{e.Validation, e.Test}
	return s.Validate()
}

// SupportsAction checks the input/action pairing after a successful Load.
func (s Specification) SupportsAction(action string) bool {
	switch action {
	case "mutate":
		return s.Version == versionV3
	case "run", "check", "challenge":
		return s.Version == versionV1 || s.Version == versionV2 || s.Version == versionV4
	default:
		return false
	}
}
