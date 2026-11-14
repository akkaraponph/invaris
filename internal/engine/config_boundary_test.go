package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAdditionalInvalidContracts(t *testing.T) {
	cases := []string{
		`{"version":"invaris.dev/v1alpha1","name":"x"} {}`,
		`{"version":"invaris.dev/v1alpha1","name":"x","steps":[null]}`,
		`{"version":"invaris.dev/v1alpha1","name":"x","steps":[{"id":"s","program":"true","timeoutSeconds":1.5}]}`,
		`{"version":"invaris.dev/v1alpha1","name":"x","guards":[{"id":"g","program":"true"}],"challenges":[{"id":"c","guard":"missing","file":"x","before":"a","after":"b","expectedOutput":"b"}]}`,
		string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}),
		`{"version":"invaris.dev/v1alpha1","name":"` + strings.Repeat("x", 1<<20) + `"}`,
	}
	for i, input := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestInvalidDirectContractsNeverDispatch(t *testing.T) {
	for _, mutate := range []func(*Specification){
		func(s *Specification) { s.Steps[0].Args = append(s.Steps[0].Args, "bad\x00arg") },
		func(s *Specification) { s.Steps[0].TimeoutSeconds = 301 },
		func(s *Specification) { s.Guards = []Command{{ID: "s", Program: "true"}} },
		func(s *Specification) {
			s.Challenges = []Challenge{{ID: "c", Guard: "missing", File: "x", Before: "a", After: "b", ExpectedOutput: "b"}}
		},
	} {
		root := t.TempDir()
		spec := Specification{Version: "invaris.dev/v1alpha1", Name: "test", Steps: []Command{{ID: "s", Program: "sh", Args: []string{"-c", "touch sentinel"}}}}
		mutate(&spec)
		if report := Run(context.Background(), spec, root, "run"); report.Status != "REFUSE" {
			t.Errorf("invalid spec accepted: %+v", report)
		}
		if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
			t.Errorf("sentinel exists or stat failed: %v", err)
		}
	}
}

func TestShippedHarnessCompatibility(t *testing.T) {
	for _, path := range []string{"../../examples/invaris.json", "../../examples/structured/invaris.json"} {
		if _, err := Load(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}
