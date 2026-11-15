package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treeManifest records every entry under root (kind, mode, content hash, link
// target) so a test can prove a command wrote nothing.
func treeManifest(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		entry := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, _ := os.Readlink(p)
			entry += " -> " + link
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entry += fmt.Sprintf(" %x", sha256.Sum256(data))
		}
		out[rel] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameManifest(t *testing.T, a, b map[string]string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("tree changed: %v -> %v", a, b)
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("tree changed at %s: %q -> %q", k, v, b[k])
		}
	}
}

// A project whose package scripts would leave a sentinel if anything ran them.
func hostileNodeProject(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	mustWrite(t, filepath.Join(dir, "package.json"), `{"scripts":{"preinstall":"touch `+sentinel+`","test":"touch `+sentinel+`"}}`)
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/x\n")
	return dir, sentinel
}

func TestInitDetectAndDryRunWriteNothing(t *testing.T) {
	dir, sentinel := hostileNodeProject(t)
	before := treeManifest(t, dir)

	code, stdout, stderr := invoke(t, dir, "init", "--detect")
	if code != 0 || stderr != "" {
		t.Fatalf("detect: code=%d stderr=%q", code, stderr)
	}
	var got struct {
		Root            string
		Markers         map[string]bool
		Recommendations []string
	}
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Markers["goMod"] || !got.Markers["packageJson"] || got.Markers["cargoToml"] || len(got.Recommendations) == 0 {
		t.Fatalf("detection: %s", stdout)
	}
	sameManifest(t, before, treeManifest(t, dir))

	for _, preset := range []string{"generic", "go"} {
		code, stdout, stderr = invoke(t, dir, "init", "--dry-run", "--preset", preset)
		if code != 0 || !strings.Contains(stderr, "nothing was written") || !strings.Contains(string(stdout), `"invaris.dev/v1alpha2"`) {
			t.Fatalf("dry run %s: code=%d stdout=%s stderr=%q", preset, code, stdout, stderr)
		}
		sameManifest(t, before, treeManifest(t, dir))
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("a package script ran")
	}
}

func TestInitCreatesValidConfigThatRefusesEmptyWork(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := invoke(t, dir, "init")
	if code != 0 || !strings.HasPrefix(string(stdout), "created ") || !strings.Contains(stderr, "refuses") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	info, err := os.Stat(filepath.Join(dir, "invaris.json"))
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config mode %v, %v", info.Mode(), err)
	}
	code, stdout, _ = invoke(t, dir, "check")
	var report decoded
	if code != 2 || json.Unmarshal(stdout, &report) != nil || report.Decision != "REFUSE" {
		t.Fatalf("empty generic config must refuse: code=%d %s", code, stdout)
	}
}

func TestInitGoPresetInSelectedPath(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "project")
	os.Mkdir(dir, 0o700)
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/x\n")
	link := filepath.Join(parent, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	// The root's own symlink is resolved once; the config lands in the real directory.
	code, _, stderr := invoke(t, parent, "init", "--path", link, "--preset", "go")
	if code != 0 || !strings.Contains(stderr, "go test ./...") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "invaris.json"))
	if err != nil || !strings.Contains(string(data), `"go-tests"`) || !strings.Contains(string(data), `"GOMODCACHE"`) {
		t.Fatalf("go config: %s, %v", data, err)
	}
}

func TestInitRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		args  []string
		msg   string
	}{
		{"existing regular config, identical bytes", func(t *testing.T, d string) {
			code, _, _ := invoke(t, d, "init")
			if code != 0 {
				t.Fatal("first init failed")
			}
		}, []string{"init"}, "already exists"},
		{"existing directory", func(t *testing.T, d string) { os.Mkdir(filepath.Join(d, "invaris.json"), 0o700) }, []string{"init"}, "already exists"},
		{"existing symlink", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "other.json"), "{}")
			os.Symlink(filepath.Join(d, "other.json"), filepath.Join(d, "invaris.json"))
		}, []string{"init"}, "already exists"},
		{"dangling symlink", func(t *testing.T, d string) {
			os.Symlink(filepath.Join(d, "nowhere.json"), filepath.Join(d, "invaris.json"))
		}, []string{"init"}, "already exists"},
		{"go preset without go.mod", nil, []string{"init", "--preset", "go"}, "go.mod"},
		{"unknown preset", nil, []string{"init", "--preset", "node"}, "unsupported preset"},
		{"detect with preset", nil, []string{"init", "--detect", "--preset", "go"}, "--detect"},
		{"detect with dry run", nil, []string{"init", "--detect", "--dry-run"}, "--detect"},
		{"repeated flag", nil, []string{"init", "--dry-run", "--dry-run"}, "repeated"},
		{"empty path", nil, []string{"init", "--path", ""}, "requires a value"},
		{"unknown flag", nil, []string{"init", "--force"}, "unknown flag"},
		{"positional", nil, []string{"init", "invaris.json"}, "unexpected argument"},
		{"missing path", nil, []string{"init", "--path", "missing"}, "project root"},
		{"report not allowed", nil, []string{"--report", "r.json", "init"}, "--report"},
		{"symlinked marker", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "real.mod"), "module x\n")
			os.Symlink(filepath.Join(d, "real.mod"), filepath.Join(d, "go.mod"))
		}, []string{"init", "--preset", "go"}, "not a regular file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			before := treeManifest(t, dir)
			code, stdout, stderr := invoke(t, dir, tc.args...)
			if code != 2 || len(stdout) != 0 || !strings.Contains(stderr, tc.msg) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			sameManifest(t, before, treeManifest(t, dir))
		})
	}
}
