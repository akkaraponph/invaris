package engine

import (
	"fmt"
	"reflect"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/mutation"
)

const versionV4 = "invaris.dev/v1alpha4"

// mutationV4 is a built-in operator declaration. Fixture is a pointer so that
// its presence, even as an empty string, can be refused on other operators.
type mutationV4 struct {
	Operator       string  `json:"operator"`
	Version        string  `json:"version"`
	PreimageSHA256 string  `json:"preimageSha256"`
	Fixture        *string `json:"fixture"`
}

// challengeV4 accepts either the literal before/after form or a built-in
// mutation. The forms are told apart by which fields are present, including
// empty strings, and may not mix.
type challengeV4 struct {
	ID             string      `json:"id"`
	Guard          string      `json:"guard"`
	File           string      `json:"file"`
	Before         *string     `json:"before"`
	After          *string     `json:"after"`
	ExpectedOutput *string     `json:"expectedOutput"`
	Mutation       *mutationV4 `json:"mutation"`
}

// specV4 is the v1alpha2 guard harness with built-in challenge mutations. It
// is a separate branch from the v1alpha3 behavioral mutation harness.
type specV4 struct {
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
	Challenges           []challengeV4   `json:"challenges,omitempty"`
}

func decodeV4(raw []byte) (Specification, error) {
	if err := checkConfigJSONType(raw, reflect.TypeOf(specV4{})); err != nil {
		return Specification{}, fmt.Errorf("parse harness: %w", err)
	}
	var in specV4
	if err := decodeStrict(raw, &in); err != nil {
		return Specification{}, fmt.Errorf("parse harness: %w", err)
	}
	s, err := buildV2(specV2{FailFast: in.FailFast, AlwaysRunGuards: in.AlwaysRunGuards, Version: in.Version, Name: in.Name,
		TrustMode: in.TrustMode, NetworkPolicy: in.NetworkPolicy, WritablePaths: in.WritablePaths, Policies: in.Policies,
		Gate: in.Gate, RedactEnv: in.RedactEnv, StoreSensitiveOutput: in.StoreSensitiveOutput, Adapters: in.Adapters,
		RequiredAdapters: in.RequiredAdapters, Steps: in.Steps, Guards: in.Guards})
	if err != nil {
		return s, err
	}
	for _, c := range in.Challenges {
		out := Challenge{ID: c.ID, Guard: c.Guard, File: c.File}
		if c.Mutation != nil {
			if c.Before != nil || c.After != nil || c.ExpectedOutput != nil {
				return s, fmt.Errorf("challenge %q mixes mutation with before, after or expectedOutput", c.ID)
			}
			m := c.Mutation
			spec := mutation.Spec{Operator: m.Operator, Version: m.Version, PreimageSHA256: m.PreimageSHA256}
			if m.Fixture != nil {
				if m.Operator != mutation.SecretLeakInjection {
					return s, fmt.Errorf("challenge %q: fixture is only valid for %s", c.ID, mutation.SecretLeakInjection)
				}
				spec.Fixture = *m.Fixture
			}
			out.Builtin = &spec
		} else {
			for _, f := range []**string{&c.Before, &c.After, &c.ExpectedOutput} {
				if *f == nil {
					empty := ""
					*f = &empty
				}
			}
			out.Before, out.After, out.ExpectedOutput = *c.Before, *c.After, *c.ExpectedOutput
		}
		s.Challenges = append(s.Challenges, out)
	}
	if s.Challenges == nil && in.Challenges != nil {
		s.Challenges = []Challenge{}
	}
	return s, nil
}
