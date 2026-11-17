package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDetectProjectMarkers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
		want  ProjectDetection
	}{
		{"none", func(*testing.T, string) {}, ProjectDetection{}},
		{"go", func(t *testing.T, r string) { write(t, r, "go.mod", "module x\n") }, ProjectDetection{GoMod: true}},
		{"go workspace", func(t *testing.T, r string) { write(t, r, "go.work", "go 1.27\n") }, ProjectDetection{GoWork: true}},
		{"node", func(t *testing.T, r string) { write(t, r, "package.json", `{"scripts":{}}`) }, ProjectDetection{PackageJSON: true}},
		{"rust", func(t *testing.T, r string) { write(t, r, "Cargo.toml", "[package]\n") }, ProjectDetection{CargoToml: true}},
		{"mixed with git dir and submodules", func(t *testing.T, r string) {
			write(t, r, "go.mod", "module x\n")
			write(t, r, "package.json", "{}")
			write(t, r, ".gitmodules", "")
			os.Mkdir(filepath.Join(r, ".git"), 0o700)
		}, ProjectDetection{GoMod: true, PackageJSON: true, Gitmodules: true, GitDir: true}},
		{"git file (worktree)", func(t *testing.T, r string) { write(t, r, ".git", "gitdir: elsewhere\n") }, ProjectDetection{GitFile: true}},
		{"nested markers are not followed", func(t *testing.T, r string) {
			os.Mkdir(filepath.Join(r, "sub"), 0o700)
			write(t, r, "sub/go.mod", "module y\n")
		}, ProjectDetection{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			got, err := DetectProject(root)
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
			if len(got.Recommendations()) == 0 {
				t.Fatal("recommendations must never be empty")
			}
		})
	}
}

func TestDetectProjectRefusesUnsafeMarkers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"symlinked go.mod", func(t *testing.T, r string) {
			write(t, r, "real.mod", "module x\n")
			os.Symlink(filepath.Join(r, "real.mod"), filepath.Join(r, "go.mod"))
		}},
		{"dangling package.json", func(t *testing.T, r string) {
			os.Symlink(filepath.Join(r, "missing"), filepath.Join(r, "package.json"))
		}},
		{"directory go.mod", func(t *testing.T, r string) { os.Mkdir(filepath.Join(r, "go.mod"), 0o700) }},
		{"symlinked .git", func(t *testing.T, r string) { os.Symlink(r, filepath.Join(r, ".git")) }},
		{"oversized Cargo.toml", func(t *testing.T, r string) { write(t, r, "Cargo.toml", strings.Repeat("x", maxMarkerBytes+1)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			if _, err := DetectProject(root); err == nil {
				t.Fatal("unsafe marker accepted")
			}
		})
	}
	if os.Geteuid() != 0 {
		root := t.TempDir()
		write(t, root, "go.mod", "module x\n")
		os.Chmod(filepath.Join(root, "go.mod"), 0)
		if _, err := DetectProject(root); err == nil {
			t.Fatal("unreadable marker accepted")
		}
	}
}

func TestDetectionRecommendsWithoutSelecting(t *testing.T) {
	r := ProjectDetection{GoMod: true, PackageJSON: true}.Recommendations()
	if !slices.ContainsFunc(r, func(s string) bool { return strings.Contains(s, "no preset is selected automatically") }) {
		t.Fatalf("mixed markers must say no preset is selected: %v", r)
	}
}

func TestRenderInitialConfig(t *testing.T) {
	generic, err := RenderInitialConfig(PresetGeneric, ProjectDetection{GoMod: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": "invaris.dev/v1alpha2",
  "name": "invaris-project",
  "trustMode": "trusted-local",
  "networkPolicy": "unrestricted",
  "writablePaths": [],
  "policies": [],
  "steps": [],
  "guards": [],
  "challenges": []
}
`
	if string(generic) != want {
		t.Fatalf("generic config:\n%s", generic)
	}
	spec, err := Decode(generic)
	if err != nil || spec.Gate != nil || len(spec.Guards) != 0 || spec.WritablePaths == nil || len(spec.WritablePaths) != 0 {
		t.Fatalf("generic spec %+v, %v", spec, err)
	}

	goConfig, err := RenderInitialConfig(PresetGo, ProjectDetection{GoMod: true})
	if err != nil {
		t.Fatal(err)
	}
	spec, err = Decode(goConfig)
	if err != nil || len(spec.Guards) != 1 || len(spec.Challenges) != 0 {
		t.Fatalf("go spec %+v, %v", spec, err)
	}
	g := spec.Guards[0]
	if g.ID != "go-tests" || g.Program != "go" || !slices.Equal(g.Args, []string{"test", "./..."}) ||
		!slices.Equal(g.EnvAllowlist, []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH"}) || g.PolicyID != "" {
		t.Fatalf("go guard %+v", g)
	}
	if _, err := RenderInitialConfig(PresetGo, ProjectDetection{GoWork: true}); err == nil {
		t.Fatal("go preset without go.mod accepted")
	}
	if _, err := RenderInitialConfig("node", ProjectDetection{PackageJSON: true}); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func write(t *testing.T, root, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
