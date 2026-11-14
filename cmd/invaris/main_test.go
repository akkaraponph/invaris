package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIPreflightPreventsExecution(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) string // returns report destination
	}{
		{"existing file", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "occupied.json")
			mustWrite(t, p, "existing")
			return p
		}},
		{"existing directory", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "occupied")
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"dangling symlink", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "dangling.json")
			if err := os.Symlink(filepath.Join(dir, "missing-target"), p); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"missing parent", func(t *testing.T, dir string) string {
			return filepath.Join(dir, "missing", "report.json")
		}},
		{"parent is a file", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "plain")
			mustWrite(t, p, "x")
			return filepath.Join(p, "report.json")
		}},
		{"unwritable parent", func(t *testing.T, dir string) string {
			if os.Geteuid() == 0 {
				t.Skip("root bypasses directory permissions")
			}
			p := filepath.Join(dir, "locked")
			if err := os.Mkdir(p, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(p, 0o700) })
			return filepath.Join(p, "report.json")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "sentinel")
			config := writeConfig(t, dir, `{"id":"s","program":"touch","args":["`+sentinel+`"]}`)
			report := tc.setup(t, dir)
			before, _ := os.Lstat(report)
			var beforeBytes []byte
			if before != nil && before.Mode().IsRegular() {
				beforeBytes, _ = os.ReadFile(report)
			}
			code, stdout, stderr := invoke(t, dir, "--report", report, "run", config)
			if code != 2 || len(stdout) != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("executed despite unusable report destination: %v", err)
			}
			if beforeBytes != nil {
				after, _ := os.ReadFile(report)
				if string(after) != string(beforeBytes) {
					t.Fatalf("destination modified: %q", after)
				}
			}
			for _, d := range []string{dir, filepath.Dir(report)} {
				if info, err := os.Stat(d); err == nil && info.IsDir() {
					if temps := reportTemps(t, d); len(temps) != 0 {
						t.Fatalf("leaked temp files in %s: %v", d, temps)
					}
				}
			}
		})
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
