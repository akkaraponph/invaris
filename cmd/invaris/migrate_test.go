package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyExample(t *testing.T, dst string, files ...string) {
	t.Helper()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dst, filepath.Base(f)), string(data))
	}
}

type outcome struct {
	Decision string
	Checks   []struct{ ID, Status string }
	Proofs   []struct{ ID, Status, EvidenceStrength, Reason string }
}

func outcomeOf(t *testing.T, stdout []byte) outcome {
	t.Helper()
	var o outcome
	if err := json.Unmarshal(stdout, &o); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return o
}

func TestMigrateLegacyFixturesEndToEnd(t *testing.T) {
	for _, fixture := range [][]string{
		{"invaris.json", "source.txt"},
		{"structured/invaris.json", "structured/source.txt", "structured/guard.sh"},
	} {
		t.Run(fixture[0], func(t *testing.T) {
			dir := t.TempDir()
			copyExample(t, dir, fixture...)
			harness := filepath.Join(dir, "invaris.json")
			os.Chmod(harness, 0o640)
			original, _ := os.ReadFile(harness)
			before := map[string]outcome{}
			for _, action := range []string{"run", "check", "challenge"} {
				code, stdout, _ := invoke(t, dir, action)
				if code != 0 {
					t.Fatalf("%s before migration: %d", action, code)
				}
				before[action] = outcomeOf(t, stdout)
			}

			manifest := treeManifest(t, dir)
			code, stdout, stderr := invoke(t, dir, "migrate", "--dry-run")
			if code != 0 || !strings.Contains(stderr, "nothing was written") || !strings.Contains(string(stdout), `"allowWeakEvidence": true`) {
				t.Fatalf("dry run: %d %s %s", code, stdout, stderr)
			}
			sameManifest(t, manifest, treeManifest(t, dir))
			preview := stdout

			code, stdout, stderr = invoke(t, dir, "migrate", "--from", "v1alpha1", "--to", "v1alpha2", "invaris.json")
			if code != 0 || !strings.HasPrefix(string(stdout), "migrated ") {
				t.Fatalf("migrate: %d %s %s", code, stdout, stderr)
			}
			migrated, _ := os.ReadFile(harness)
			if string(migrated) != string(preview) {
				t.Fatal("migration wrote other bytes than the dry run showed")
			}
			backup, _ := os.ReadFile(harness + ".bak")
			binfo, _ := os.Stat(harness + ".bak")
			hinfo, _ := os.Stat(harness)
			if string(backup) != string(original) || binfo.Mode().Perm() != 0o640 || hinfo.Mode().Perm() != 0o640 {
				t.Fatalf("backup or mode not preserved: %v %v", binfo.Mode(), hinfo.Mode())
			}
			for _, action := range []string{"run", "check", "challenge"} {
				code, stdout, _ := invoke(t, dir, action)
				after := outcomeOf(t, stdout)
				b := before[action]
				if code != 0 || after.Decision != b.Decision || len(after.Checks) != len(b.Checks) || len(after.Proofs) != len(b.Proofs) {
					t.Fatalf("%s changed: %+v -> %+v", action, b, after)
				}
				for i := range b.Proofs {
					if after.Proofs[i] != b.Proofs[i] {
						t.Fatalf("%s proof changed: %+v -> %+v", action, b.Proofs[i], after.Proofs[i])
					}
				}
				for i := range b.Checks {
					if after.Checks[i] != b.Checks[i] {
						t.Fatalf("%s check changed: %+v -> %+v", action, b.Checks[i], after.Checks[i])
					}
				}
			}

			// Already current: a no-op that writes nothing, including no new backup.
			os.Remove(harness + ".bak")
			manifest = treeManifest(t, dir)
			code, stdout, _ = invoke(t, dir, "migrate")
			if code != 0 || !strings.Contains(string(stdout), "nothing changed") {
				t.Fatalf("repeat: %d %s", code, stdout)
			}
			sameManifest(t, manifest, treeManifest(t, dir))
			code, stdout, _ = invoke(t, dir, "migrate", "--dry-run")
			if code != 0 || string(stdout) != string(migrated) {
				t.Fatal("same-version dry run must print the original bytes")
			}
		})
	}
}

func TestMigrateRefusalsWriteNothing(t *testing.T) {
	legacy := `{"version":"invaris.dev/v1alpha1","name":"x","guards":[{"id":"g","program":"true"}]}`
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		args  []string
		msg   string
	}{
		{"v1alpha3 harness", func(t *testing.T, d string) { copyExample(t, d, "mutation/killed.json") }, []string{"migrate", "killed.json"}, "different contract"},
		{"unknown field kept by refusing", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "invaris.json"), strings.Replace(legacy, `"name"`, `"note":"x","name"`, 1))
		}, []string{"migrate"}, "unknown"},
		{"backup collision", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "invaris.json"), legacy)
			mustWrite(t, filepath.Join(d, "invaris.json.bak"), legacy)
		}, []string{"migrate"}, "already exists"},
		{"symlinked harness", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "real.json"), legacy)
			os.Symlink(filepath.Join(d, "real.json"), filepath.Join(d, "invaris.json"))
		}, []string{"migrate"}, "not a regular file"},
		{"hard-linked harness", func(t *testing.T, d string) {
			mustWrite(t, filepath.Join(d, "invaris.json"), legacy)
			os.Link(filepath.Join(d, "invaris.json"), filepath.Join(d, "other.json"))
		}, []string{"migrate"}, "hard links"},
		{"missing harness", nil, []string{"migrate"}, "no such file"},
		{"from mismatch", func(t *testing.T, d string) { mustWrite(t, filepath.Join(d, "invaris.json"), legacy) }, []string{"migrate", "--from", "v1alpha2"}, "does not match"},
		{"bad target", func(t *testing.T, d string) { mustWrite(t, filepath.Join(d, "invaris.json"), legacy) }, []string{"migrate", "--to", "v1alpha4"}, "only migration target"},
		{"repeated flag", nil, []string{"migrate", "--dry-run", "--dry-run"}, "repeated"},
		{"extra positional", nil, []string{"migrate", "a.json", "b.json"}, "more than one"},
		{"unknown flag", nil, []string{"migrate", "--force"}, "unknown flag"},
		{"report not allowed", nil, []string{"--report", "r.json", "migrate"}, "--report"},
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
