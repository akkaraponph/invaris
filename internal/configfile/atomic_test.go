package configfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var errInjected = errors.New("injected")

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".invaris-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestCreatePublishesExactBytesAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invaris.json")
	if err := Create(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	info, _ := os.Stat(path)
	if err != nil || string(data) != "{}\n" || info.Mode().Perm() != 0o600 {
		t.Fatalf("data=%q mode=%v err=%v", data, info.Mode(), err)
	}
	if l := leftovers(t, dir); len(l) != 0 {
		t.Fatalf("temporary files left: %v", l)
	}
}

func TestCreateNeverReplaces(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"regular file with same bytes", func(t *testing.T, p string) { os.WriteFile(p, []byte("{}\n"), 0o600) }},
		{"directory", func(t *testing.T, p string) { os.Mkdir(p, 0o700) }},
		{"symlink", func(t *testing.T, p string) {
			target := filepath.Join(filepath.Dir(p), "target")
			os.WriteFile(target, []byte("target"), 0o600)
			os.Symlink(target, p)
		}},
		{"dangling symlink", func(t *testing.T, p string) { os.Symlink(filepath.Join(filepath.Dir(p), "missing"), p) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "invaris.json")
			tc.setup(t, path)
			before, _ := os.Lstat(path)
			err := Create(path, []byte("{}\n"), 0o600)
			if !errors.Is(err, ErrExists) {
				t.Fatalf("err=%v, want ErrExists", err)
			}
			after, _ := os.Lstat(path)
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("existing entry changed")
			}
			if _, err := os.Stat(filepath.Join(dir, "missing")); err == nil {
				t.Fatal("dangling symlink target was created")
			}
			if l := leftovers(t, dir); len(l) != 0 {
				t.Fatalf("temporary files left: %v", l)
			}
		})
	}
}

// The existence check before staging is not authoritative: a destination that
// appears afterwards must still make the hard link fail.
func TestCreateRaceLosesToLateCreator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invaris.json")
	defer func() { fsys = osOps }()
	fsys.Link = func(o, n string) error {
		os.WriteFile(path, []byte("other"), 0o600)
		return os.Link(o, n)
	}
	if err := Create(path, []byte("{}\n"), 0o600); !errors.Is(err, ErrExists) {
		t.Fatalf("err=%v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "other" {
		t.Fatalf("late creator's file replaced: %q", data)
	}
}

func TestCreateConcurrentOnlyOneSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invaris.json")
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Create(path, []byte{byte('a' + i), '\n'}, 0o600)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrExists):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d creators succeeded", ok)
	}
	if l := leftovers(t, dir); len(l) != 0 {
		t.Fatalf("temporary files left: %v", l)
	}
}

type faultyFile struct {
	file
	write, chmod, sync, close bool
}

func (f faultyFile) Write(p []byte) (int, error) {
	if f.write {
		return 0, errInjected
	}
	return f.file.Write(p)
}
func (f faultyFile) Chmod(m fs.FileMode) error {
	if f.chmod {
		return errInjected
	}
	return f.file.Chmod(m)
}
func (f faultyFile) Sync() error {
	if f.sync {
		return errInjected
	}
	return f.file.Sync()
}
func (f faultyFile) Close() error {
	err := f.file.Close()
	if f.close {
		return errInjected
	}
	return err
}

func TestCreateFaultsLeaveNoFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inject func()
		synced bool // the file was published; only the directory sync failed
	}{
		{"create temp", func() { fsys.CreateTemp = func(string, string) (file, error) { return nil, errInjected } }, false},
		{"write", func() { wrap(faultyFile{write: true}) }, false},
		{"chmod", func() { wrap(faultyFile{chmod: true}) }, false},
		{"sync", func() { wrap(faultyFile{sync: true}) }, false},
		{"close", func() { wrap(faultyFile{close: true}) }, false},
		{"link unsupported", func() { fsys.Link = func(string, string) error { return errInjected } }, false},
		{"directory sync", func() { fsys.SyncDir = func(string) error { return errInjected } }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { fsys = osOps }()
			tc.inject()
			dir := t.TempDir()
			path := filepath.Join(dir, "invaris.json")
			err := Create(path, []byte("{}\n"), 0o600)
			if !errors.Is(err, errInjected) {
				t.Fatalf("err=%v", err)
			}
			if _, statErr := os.Lstat(path); (statErr == nil) != tc.synced {
				t.Fatalf("final file present=%v", statErr == nil)
			}
			if l := leftovers(t, dir); len(l) != 0 {
				t.Fatalf("temporary files left: %v", l)
			}
		})
	}
}

func wrap(f faultyFile) {
	fsys.CreateTemp = func(dir, pattern string) (file, error) {
		real, err := osOps.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		f.file = real
		return f, nil
	}
}
