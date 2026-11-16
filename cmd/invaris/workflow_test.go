package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/evidence"
)

// Adoption: detect, create, add a guard, check; nothing runs before check and
// the project is unchanged by every Invaris command.
func TestWorkflowInitThenCheck(t *testing.T) {
	for _, tc := range []struct{ name, marker, content string }{
		{"node", "package.json", `{"scripts":{"postinstall":"touch SENTINEL"}}`},
		{"rust", "Cargo.toml", "[package]\nname = \"x\"\n"},
		{"mixed workspace", "go.work", "go 1.27\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, tc.marker), tc.content)
			mustWrite(t, filepath.Join(dir, "README.md"), "hello\n")
			before := treeManifest(t, dir)
			code, out, _ := runBinary(t, dir, nil, "init", "--detect")
			if code != 0 || !strings.Contains(string(out), "true") {
				t.Fatalf("detect: %d %s", code, out)
			}
			sameManifest(t, before, treeManifest(t, dir))
			if code, _, _ := runBinary(t, dir, nil, "init"); code != 0 {
				t.Fatal("init failed")
			}
			if code, _, _ := runBinary(t, dir, nil, "check"); code != 2 {
				t.Fatal("empty generic harness must refuse")
			}
			// The adopter adds a guard by hand.
			raw, _ := os.ReadFile(filepath.Join(dir, ".invaris", "invaris.json"))
			edited := strings.Replace(string(raw), `"guards": []`, `"guards": [{"id": "readme", "program": "test", "args": ["-s", "README.md"]}]`, 1)
			mustWrite(t, filepath.Join(dir, ".invaris", "invaris.json"), edited)
			after := treeManifest(t, dir)
			code, out, _ = runBinary(t, dir, nil, "check", "--format", "compact")
			if code != 0 || !strings.HasPrefix(string(out), "PASS readme") {
				t.Fatalf("check: %d %s", code, out)
			}
			sameManifest(t, after, treeManifest(t, dir))
			if _, err := os.Stat(filepath.Join(dir, "SENTINEL")); err == nil {
				t.Fatal("a package script ran")
			}
		})
	}
}

// A minimal sanitized stand-in for the external example's contract: a
// v1alpha1 harness whose guard runs `git diff --check` in a nested checkout.
// check works before and after migration; challenge copies exclude .git, so
// such guards cannot be challenged.
func TestWorkflowLegacyGitGuardFixture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	os.Mkdir(app, 0o755)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", app, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	mustWrite(t, filepath.Join(app, "main.txt"), "clean\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	mustWrite(t, filepath.Join(dir, "invaris.json"), `{"version":"invaris.dev/v1alpha1","name":"git-whitespace","guards":[{"id":"whitespace","program":"git","args":["-C","app","diff","--check"],"envAllowlist":["PATH","HOME"]}]}`)
	for _, step := range []string{"legacy", "migrated"} {
		mustWrite(t, filepath.Join(app, "main.txt"), "clean\n")
		code, _, stderr := runBinary(t, dir, nil, "check")
		if code != 0 {
			t.Fatalf("%s clean check: %d %s", step, code, stderr)
		}
		mustWrite(t, filepath.Join(app, "main.txt"), "dirty \n")
		if code, _, _ := runBinary(t, dir, nil, "check"); code != 1 {
			t.Fatalf("%s whitespace check: exit %d, want 1", step, code)
		}
		if step == "legacy" {
			if code, _, stderr := runBinary(t, dir, nil, "migrate"); code != 0 {
				t.Fatalf("migrate: %s", stderr)
			}
		}
	}
}

// Built-in operator workflow: standalone, filtered and parallel campaigns,
// a stale preimage and a redirected target; the source never changes.
func TestWorkflowOperators(t *testing.T) {
	dir := operatorProject(t, t.TempDir(), "p")
	before := treeManifest(t, dir)
	for _, tc := range []struct {
		args []string
		exit int
	}{
		{[]string{"challenge"}, 0},
		{[]string{"campaign", "--parallel", "3"}, 0},
		{[]string{"campaign", "--only", "spacing", "--format", "compact"}, 0},
	} {
		if code, out, stderr := runBinary(t, dir, nil, tc.args...); code != tc.exit {
			t.Fatalf("%v: %d %s %s", tc.args, code, out, stderr)
		}
		sameManifest(t, before, treeManifest(t, dir))
	}
	_, out, _ := runBinary(t, dir, nil, "campaign", "--only", "spacing")
	r, err := evidence.DecodeCampaignV5(out)
	if err != nil || r.Selection.Filtered != 2 || len(r.MutationMetadata) != 2 {
		t.Fatalf("filtered campaign: %v %+v", err, r.Selection)
	}

	// A target replaced by a symbolic link cannot be copied: the snapshot is
	// refused before any attempt.
	link := operatorProject(t, t.TempDir(), "link")
	os.Rename(filepath.Join(link, "notes.txt"), filepath.Join(link, "real.txt"))
	os.Symlink("real.txt", filepath.Join(link, "notes.txt"))
	code, out, _ := runBinary(t, link, nil, "challenge")
	var rep struct {
		Decision string
		Proofs   []struct{ Status, Reason string }
	}
	json.Unmarshal(out, &rep)
	if code != 2 || rep.Decision != "REFUSE" || len(rep.Proofs) != 3 || rep.Proofs[0].Reason != domain.ReasonPrepareFailed {
		t.Fatalf("symlinked target: %d %s", code, out)
	}
}
