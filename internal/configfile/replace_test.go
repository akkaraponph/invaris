package configfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	original = `{"version":"invaris.dev/v1alpha1","name":"x"}`
	migrated = "{\n  \"version\": \"invaris.dev/v1alpha2\"\n}\n"
)

func setupSource(t *testing.T, mode fs.FileMode) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "invaris.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func assertFile(t *testing.T, path, data string, mode fs.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	info, serr := os.Lstat(path)
	if err != nil || serr != nil || string(got) != data || info.Mode().Perm() != mode {
		t.Fatalf("%s: data=%q mode=%v err=%v %v; want %q %v", path, got, info.Mode().Perm(), err, serr, data, mode)
	}
}

func assertAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s should not exist (%v)", p, err)
		}
	}
}

func TestReplaceWithBackupSucceeds(t *testing.T) {
	dir, path := setupSource(t, 0o640)
	if err := ReplaceWithBackup(path, []byte(original), []byte(migrated)); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, migrated, 0o640)
	assertFile(t, path+".bak", original, 0o640)
	assertAbsent(t, path+".migrate.lock")
	if l := leftovers(t, dir); len(l) != 0 {
		t.Fatalf("temporary files left: %v", l)
	}
}

func TestReplaceWithBackupRefusesUnsafeSources(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir, path string)
		msg   string
	}{
		{"symlink", func(t *testing.T, dir, path string) {
			os.Rename(path, filepath.Join(dir, "real.json"))
			os.Symlink(filepath.Join(dir, "real.json"), path)
		}, "not a regular file"},
		{"hard link", func(t *testing.T, dir, path string) { os.Link(path, filepath.Join(dir, "other.json")) }, "hard links"},
		{"read-only", func(t *testing.T, dir, path string) { os.Chmod(path, 0o444) }, "not writable"},
		{"changed since read", func(t *testing.T, dir, path string) { os.WriteFile(path, []byte(original+" "), 0o600) }, "changed since it was read"},
		{"stale lock", func(t *testing.T, dir, path string) { os.WriteFile(path+".migrate.lock", nil, 0o600) }, "another migration"},
		{"backup collision with identical bytes", func(t *testing.T, dir, path string) { os.WriteFile(path+".bak", []byte(original), 0o600) }, "already exists"},
		{"backup collision as dangling link", func(t *testing.T, dir, path string) { os.Symlink(filepath.Join(dir, "gone"), path+".bak") }, "already exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := setupSource(t, 0o600)
			tc.setup(t, dir, path)
			before := snapshotDir(t, dir)
			err := ReplaceWithBackup(path, []byte(original), []byte(migrated))
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err=%v, want %q", err, tc.msg)
			}
			if after := snapshotDir(t, dir); after != before {
				t.Fatalf("directory changed:\n%s\n%s", before, after)
			}
		})
	}
}

func TestReplaceWithBackupFaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		inject   func(path string)
		replaced bool
	}{
		{"lock create", func(string) {
			fsys.OpenFile = func(string, int, fs.FileMode) (file, error) { return nil, errInjected }
		}, false},
		{"backup stage write", func(string) {
			calls := 0
			fsys.CreateTemp = func(dir, pattern string) (file, error) {
				calls++
				f, err := osOps.CreateTemp(dir, pattern)
				if calls == 1 && err == nil {
					return faultyFile{file: f, write: true}, nil
				}
				return f, err
			}
		}, false},
		{"new bytes sync", func(string) {
			calls := 0
			fsys.CreateTemp = func(dir, pattern string) (file, error) {
				calls++
				f, err := osOps.CreateTemp(dir, pattern)
				if calls == 2 && err == nil {
					return faultyFile{file: f, sync: true}, nil
				}
				return f, err
			}
		}, false},
		{"source edited while staging", func(path string) {
			calls := 0
			fsys.CreateTemp = func(dir, pattern string) (file, error) {
				calls++
				if calls == 2 {
					os.WriteFile(path, []byte(original+"\n"), 0o600)
				}
				return osOps.CreateTemp(dir, pattern)
			}
		}, false},
		{"source replaced while staging", func(path string) {
			calls := 0
			fsys.CreateTemp = func(dir, pattern string) (file, error) {
				calls++
				if calls == 2 {
					tmp := path + ".swap"
					os.WriteFile(tmp, []byte(original), 0o600)
					os.Rename(tmp, path)
				}
				return osOps.CreateTemp(dir, pattern)
			}
		}, false},
		{"backup directory sync", func(string) {
			fsys.SyncDir = func(string) error { return errInjected }
		}, false},
		{"rename", func(string) { fsys.Rename = func(string, string) error { return errInjected } }, false},
		{"directory sync after rename", func(string) {
			calls := 0
			fsys.SyncDir = func(dir string) error {
				calls++
				if calls == 2 { // the first sync publishes the backup
					return errInjected
				}
				return syncDir(dir)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { fsys = osOps }()
			dir, path := setupSource(t, 0o600)
			current, _ := os.ReadFile(path)
			tc.inject(path)
			err := ReplaceWithBackup(path, current, []byte(migrated))
			fsys = osOps
			if err == nil {
				t.Fatal("fault not reported")
			}
			if errors.Is(err, ErrReplaced) != tc.replaced {
				t.Fatalf("ErrReplaced=%v for %v", errors.Is(err, ErrReplaced), err)
			}
			assertAbsent(t, path+".migrate.lock")
			if tc.replaced {
				assertFile(t, path, migrated, 0o600)
				assertFile(t, path+".bak", original, 0o600)
				if !strings.Contains(err.Error(), "backup kept") {
					t.Fatalf("error must explain recovery: %v", err)
				}
			} else {
				if got, _ := os.ReadFile(path); string(got) == migrated {
					t.Fatal("source replaced despite failure")
				}
				assertAbsent(t, path+".bak")
			}
			if l := leftovers(t, dir); len(l) != 0 {
				t.Fatalf("temporary files left: %v", l)
			}
		})
	}
}

func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, _ := os.Lstat(p)
		data, _ := os.ReadFile(p)
		link, _ := os.Readlink(p)
		b.WriteString(e.Name() + " " + info.Mode().String() + " " + link + " " + string(data) + "\n")
	}
	return b.String()
}
