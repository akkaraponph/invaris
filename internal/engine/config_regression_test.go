package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsInvalidSpecBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	spec := Specification{Version: "wrong", Name: "test", Steps: []Command{{ID: "step", Program: "sh", Args: []string{"-c", "touch sentinel"}}}}
	got := Run(context.Background(), spec, root, "run")
	if got.Status != "REFUSE" {
		t.Fatalf("accepted invalid contract: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
		t.Fatalf("executed invalid contract: %v", err)
	}
}

func TestLoadRejectsAmbiguousJSON(t *testing.T) {
	for _, input := range []string{
		`{"version":"invaris.dev/v1alpha1","name":"a","name":"b"}`,
		`{"version":"invaris.dev/v1alpha1","Name":"a"}`,
		`{"version":"invaris.dev/v1alpha1","name":"a","steps":null}`,
		`{"version":"invaris.dev/v1alpha1","name":"a","steps":[{"id":"a","program":"true","program":"false"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestValidateRejectsUnsafeCommandsAndPaths(t *testing.T) {
	for _, path := range []string{"a/../b", "C:/secret", "a\\b", "a//b", "a/./b"} {
		if validatePath(path) == nil {
			t.Errorf("accepted path %q", path)
		}
	}
	for _, cmd := range []Command{{ID: " a", Program: "true"}, {ID: "a", Program: "true", Args: []string{"x\x00y"}}} {
		if (Specification{Version: "invaris.dev/v1alpha1", Name: "test", Steps: []Command{cmd}}).Validate() == nil {
			t.Errorf("accepted command %+v", cmd)
		}
	}
}

func TestLoadPermissionDeclarations(t *testing.T) {
	for _, tc := range []struct {
		fields string
		valid  bool
	}{
		{`"networkPolicy":"unrestricted"`, true},
		{`"networkPolicy":"isolated"`, false},
		{`"requiredAdapters":["missing"]`, false},
		{`"policies":["p"],"guards":[{"id":"g","program":"true","policyId":"q"}]`, false},
		{`"writablePaths":[],"guards":[{"id":"g","program":"true"}],"challenges":[{"id":"c","guard":"g","file":"x","before":"a","after":"b","expectedOutput":"b"}]`, false},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"version":"invaris.dev/v1alpha1","name":"x",` + tc.fields + `}`
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.fields, err)
		}
	}
}

func TestValidateRejectsDuplicateEnvironmentAndCrossKindIDs(t *testing.T) {
	for _, spec := range []Specification{
		{Version: "invaris.dev/v1alpha1", Name: "x", Steps: []Command{{ID: "s", Program: "true", EnvAllowlist: []string{"PATH", "PATH"}}}},
		{Version: "invaris.dev/v1alpha1", Name: "x", Guards: []Command{{ID: "g", Program: "true"}}, Challenges: []Challenge{{ID: "g", Guard: "g", File: "x", Before: "a", After: "b", ExpectedOutput: "b"}}},
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("accepted invalid spec: %+v", spec)
		}
	}
}

func TestValidationBoundaries(t *testing.T) {
	for _, n := range []int{0, 1, 300} {
		spec := Specification{Version: "invaris.dev/v1alpha1", Name: "boundaries", Steps: []Command{{ID: "s", Program: "true", Args: []string{""}, TimeoutSeconds: n}}}
		if err := spec.Validate(); err != nil {
			t.Errorf("timeout %d: %v", n, err)
		}
	}
	for _, n := range []int{-1, 301} {
		spec := Specification{Version: "invaris.dev/v1alpha1", Name: "boundaries", Steps: []Command{{ID: "s", Program: "true", TimeoutSeconds: n}}}
		if err := spec.Validate(); err == nil {
			t.Errorf("accepted timeout %d", n)
		}
	}
	spec := Specification{Version: "invaris.dev/v1alpha1", Name: "limit"}
	for i := 0; i < 128; i++ {
		spec.Steps = append(spec.Steps, Command{ID: fmt.Sprintf("s%d", i), Program: "true"})
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("128 tasks rejected: %v", err)
	}
	spec.Steps = append(spec.Steps, Command{ID: "extra", Program: "true"})
	if err := spec.Validate(); err == nil {
		t.Fatal("129 tasks accepted")
	}
}
