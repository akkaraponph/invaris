package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// nodeProject is a checkout shaped like a Node project: a dependency tree
// with symbolic links and more entries than the default bound.
func nodeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "node_modules", "dep")
	if err := os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range workspace.MaxEntries {
		if err := os.WriteFile(filepath.Join(pkg, "f"+string(rune('a'+i%26))+strings.Repeat("x", i/26)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "marker"), []byte("dep-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dep/marker", filepath.Join(root, "node_modules", ".bin", "dep")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMutationWorkspaceOptionsForNodeProject(t *testing.T) {
	root := nodeProject(t)
	// The test reads the dependency through the parent directory, as Node's
	// resolver does, and writes a tool cache, as Vite does.
	kill := `[ "$(cat ../node_modules/dep/marker)" = dep-ok ] || exit 2; mkdir -p node_modules/.vite .next; printf c > node_modules/.vite/deps; printf b > .next/out; `
	test := kill + `if [ "$(cat src)" = new ]; then ` + emit(strings.ReplaceAll(cleanTests, "PASS", "ASSERTION_FAILED")) + `; exit 1; fi; ` + emit(cleanTests)

	raw := strings.Replace(mutationConfig, `"writablePaths":["src"],`,
		`"writablePaths":["src"],"workspace":{"dependencies":["node_modules"],"exclude":[".next"]},`, 1)
	spec, err := decodeSpecification([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	spec.Experiment.TestIDs = []string{"case"}
	spec.Experiment.Test.Args = []string{"-c", test}
	spec.Experiment.Validation.Args = []string{"-c", emit(cleanValidity)}

	e, obs := runMutation(context.Background(), spec, root, prepareFunc(context.Background(), spec.Workspace), (*workspace.Workspace).Close)
	if e.Verdict != domain.MutationKilled {
		t.Fatalf("verdict %s reason %s stages %+v obs %+v", e.Verdict, e.Reason, e.Stages, obs)
	}
	if _, err := os.Lstat(filepath.Join(root, "node_modules", ".vite")); !os.IsNotExist(err) {
		t.Fatalf("tool cache was written into the source dependency: %v", err)
	}

	// Without the exclusion the cache is a workspace change, and the stage
	// detail names the path instead of only "manifest changed".
	raw = strings.Replace(raw, `,"exclude":[".next"]`, ``, 1)
	spec2, err := decodeSpecification([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	spec2.Experiment = spec.Experiment
	e, _ = runMutation(context.Background(), spec2, root, prepareFunc(context.Background(), spec2.Workspace), (*workspace.Workspace).Close)
	if e.Reason != "workspace_changed" {
		t.Fatalf("reason %s", e.Reason)
	}
	detail := e.Stages[len(e.Stages)-2].Detail
	if !strings.Contains(detail, "+.next") || !strings.Contains(detail, "+.next/out") || strings.Contains(detail, "node_modules") {
		t.Fatalf("detail does not name the changed paths: %q", detail)
	}
}

func TestWorkspaceOptionsDefaultStillRefusesNodeModules(t *testing.T) {
	root := nodeProject(t)
	if err := os.Remove(filepath.Join(root, "node_modules", ".bin", "dep")); err != nil {
		t.Fatal(err)
	}
	spec, _ := mutationTestProject(t)
	e, _ := runMutation(context.Background(), spec, root, prepareFunc(context.Background(), spec.Workspace), (*workspace.Workspace).Close)
	if e.Reason != domain.ReasonPrepareFailed || !strings.Contains(e.Stages[0].Detail, "exceeds 2000 entries") ||
		!strings.Contains(e.Stages[0].Detail, "workspace.dependencies") {
		t.Fatalf("reason %s stages %+v", e.Reason, e.Stages)
	}
	root = nodeProject(t)
	e, _ = runMutation(context.Background(), spec, root, prepareFunc(context.Background(), spec.Workspace), (*workspace.Workspace).Close)
	if !strings.Contains(e.Stages[0].Detail, "symlink not permitted") || !strings.Contains(e.Stages[0].Detail, "workspace.dependencies") {
		t.Fatalf("stages %+v", e.Stages)
	}
}

func TestWorkspaceOptionsValidation(t *testing.T) {
	for name, block := range map[string]string{
		"target excluded": `{"exclude":["src"]}`,
		"unknown field":   `{"ignore":["x"]}`,
		"nested dep":      `{"dependencies":["a/node_modules"]}`,
		"limit too large": `{"maxEntries":999999999}`,
		"bad exclude":     `{"exclude":["../x"]}`,
	} {
		raw := strings.Replace(mutationConfig, `"writablePaths":["src"],`, `"writablePaths":["src"],"workspace":`+block+`,`, 1)
		if _, err := decodeSpecification([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCampaignMergesWorkspaceOptions(t *testing.T) {
	o := mergeWorkspace(workspace.Options{}, "app", workspace.Options{Exclude: []string{".next"}, Dependencies: []string{"node_modules"}, MaxEntries: 9000})
	o = mergeWorkspace(o, ".", workspace.Options{Exclude: []string{"dist"}, MaxEntries: 100})
	if strings.Join(o.Exclude, ",") != "app/.next,app/node_modules,dist" || o.MaxEntries != 9000 || len(o.Dependencies) != 0 {
		t.Fatalf("merged %+v", o)
	}
}
