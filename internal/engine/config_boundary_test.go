package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
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

func TestInvalidSpecificationSentinelMatrix(t *testing.T) {
	cases := map[string]func(*Specification){
		"whitespace id":    func(s *Specification) { s.Steps[0].ID = " bad" },
		"negative timeout": func(s *Specification) { s.Steps[0].TimeoutSeconds = -1 },
		"excess timeout":   func(s *Specification) { s.Steps[0].TimeoutSeconds = 301 },
		"nul argument":     func(s *Specification) { s.Steps[0].Args = append(s.Steps[0].Args, "\x00") },
		"duplicate IDs":    func(s *Specification) { s.Guards = []Command{{ID: "s", Program: "true"}} },
		"missing guard": func(s *Specification) {
			s.Challenges = []Challenge{{ID: "c", Guard: "missing", File: "x", Before: "a", After: "b", ExpectedOutput: "b"}}
		},
		"too many tasks": func(s *Specification) {
			for i := 0; i < 128; i++ {
				s.Steps = append(s.Steps, Command{ID: fmt.Sprintf("x%d", i), Program: "true"})
			}
		},
	}
	for _, path := range []string{"../x", "a/../x", "C:/x", `a\x`, "/x", "a//x"} {
		p := path
		cases["path "+p] = func(s *Specification) {
			s.Guards = []Command{{ID: "g", Program: "true"}}
			s.Challenges = []Challenge{{ID: "c", Guard: "g", File: p, Before: "a", After: "b", ExpectedOutput: "b"}}
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s := Specification{Version: "invaris.dev/v1alpha1", Name: "test", Steps: []Command{{ID: "s", Program: "sh", Args: []string{"-c", "touch sentinel"}}}}
			mutate(&s)
			if got := Run(context.Background(), s, root, "run"); got.Status != "REFUSE" {
				t.Fatalf("accepted: %+v", got)
			}
			if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
				t.Fatalf("sentinel: %v", err)
			}
		})
	}
}

func TestDecodeSpecificationDigestUsesExactBytes(t *testing.T) {
	raw := []byte("{\"version\":\"invaris.dev/v1alpha1\",\"name\":\"test\"}\n")
	spec, err := decodeSpecification(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if spec.ConfigDigest != want {
		t.Fatalf("digest = %q, want %q", spec.ConfigDigest, want)
	}
}

func TestInvalidJSONCannotDispatchSentinel(t *testing.T) {
	prefix := `{"version":"invaris.dev/v1alpha1","name":"x","steps":[{"id":"s","program":"sh","args":["-c","touch sentinel"]}],`
	cases := []string{
		prefix + `"guards":[{"id":"g","program":"true","program":"false"}]}`,
		prefix + `"guards":[{"id":"g","Program":"true"}]}`,
		prefix + `"guards":null}`,
		prefix + `"guards":[null]}`,
		prefix + `"guards":[]} {}`,
		prefix + `"guards":[{"id":"g","program":"true","timeoutSeconds":1.5}]}`,
	}
	for _, data := range cases {
		root := t.TempDir()
		path := filepath.Join(root, "harness.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		spec, err := Load(path)
		if err == nil {
			Run(context.Background(), spec, root, "run")
			t.Errorf("accepted %s", data)
		}
		if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
			t.Errorf("sentinel: %v", err)
		}
	}
}
