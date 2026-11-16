package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceConfigDiscoveryAndProjectRoot(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "project\n")
	if code, _, stderr := runBinary(t, dir, nil, "init"); code != 0 {
		t.Fatal(stderr)
	}
	config := filepath.Join(dir, ".invaris", "invaris.json")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"guards": []`, `"guards": [{"id":"readme","program":"test","args":["-s","README.md"]}]`, 1)
	mustWrite(t, config, edited)
	// A bad legacy config must not hide a valid workspace config.
	mustWrite(t, filepath.Join(dir, "invaris.json"), "invalid")
	for _, args := range [][]string{{"check"}, {"check", ".invaris/invaris.json"}, {"doctor"}, {"migrate", "--dry-run"}} {
		if code, out, stderr := runBinary(t, dir, nil, args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	// An invalid workspace file must not silently fall back to a legacy file.
	mustWrite(t, config, "invalid")
	mustWrite(t, filepath.Join(dir, "invaris.json"), edited)
	if code, _, _ := runBinary(t, dir, nil, "check"); code != 2 {
		t.Fatalf("invalid workspace: exit %d", code)
	}
	if code, _, stderr := runBinary(t, dir, nil, "check", "invaris.json"); code != 0 {
		t.Fatal(stderr)
	}
}

func TestInitRejectsUnsafeWorkspace(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			workspace := filepath.Join(dir, ".invaris")
			switch kind {
			case "file":
				mustWrite(t, workspace, "occupied")
			case "symlink":
				if err := os.Symlink(t.TempDir(), workspace); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Symlink(filepath.Join(dir, "missing"), workspace); err != nil {
					t.Fatal(err)
				}
			}
			before := treeManifest(t, dir)
			if code, _, stderr := runBinary(t, dir, nil, "init"); code != 2 || !strings.Contains(stderr, "real directory") {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			sameManifest(t, before, treeManifest(t, dir))
		})
	}
}

func TestWorkspaceCampaign(t *testing.T) {
	dir := operatorProject(t, t.TempDir(), "p")
	if err := os.Mkdir(filepath.Join(dir, ".invaris"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"invaris.json", "campaign.json"} {
		if err := os.Rename(filepath.Join(dir, file), filepath.Join(dir, ".invaris", file)); err != nil {
			t.Fatal(err)
		}
	}
	before := treeManifest(t, dir)
	for _, args := range [][]string{{"challenge"}, {"campaign"}, {"campaign", "--parallel", "3", ".invaris/campaign.json"}} {
		if code, out, stderr := runBinary(t, dir, nil, args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	sameManifest(t, before, treeManifest(t, dir))
}
