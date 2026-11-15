package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// limitCtx ends after Err has been consulted limit times, so a test can
// cancel a copy at an exact point without timing.
type limitCtx struct {
	context.Context
	calls, limit int32
}

func (c *limitCtx) Err() error {
	if atomic.AddInt32(&c.calls, 1) > c.limit {
		return context.Canceled
	}
	return nil
}

func emptyTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

func assertNoWorkspaces(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "invaris-proof-") {
			t.Fatalf("partial workspace left behind: %s", e.Name())
		}
	}
}

func TestPrepareContextCancellationRemovesPartialCopy(t *testing.T) {
	src := t.TempDir()
	for i := range 20 {
		write(t, src, filepath.Join("d", string(rune('a'+i))+".txt"), strings.Repeat("x", 100), 0o644)
	}
	write(t, src, "big.bin", strings.Repeat("y", 3*copyChunk+5), 0o644)
	before, err := Snapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int32{0, 1, 3, 10, 22, 24} {
		tmp := emptyTemp(t)
		w, err := PrepareContext(&limitCtx{Context: context.Background(), limit: limit}, src)
		if !errors.Is(err, context.Canceled) || w != nil {
			t.Fatalf("limit %d: err=%v w=%v", limit, err, w)
		}
		assertNoWorkspaces(t, tmp)
	}
	after, _ := Snapshot(src)
	if d := Diff(before, after); len(d) != 0 {
		t.Fatalf("source changed: %v", d)
	}
	// A context that never ends copies everything.
	emptyTemp(t)
	w, err := PrepareContext(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	copied, _ := Snapshot(w.Root)
	for p, e := range before {
		// Directories are created owner-only in the copy; files keep modes.
		if c, ok := copied[p]; !ok || e.Kind == "file" && c != e {
			t.Fatalf("copy differs at %s", p)
		}
	}
}

// Nested Git metadata is skipped at every level; read-only and executable
// files keep their modes; nothing in the source changes across a full
// prepare, replace and repeated close.
func TestPrepareNestedGitAndModes(t *testing.T) {
	src := t.TempDir()
	write(t, src, "sub/repo/.git/HEAD", "ref: x\n", 0o644)
	write(t, src, "sub/repo/file.txt", "SAFE\n", 0o644)
	write(t, src, "sub/repo/.gitmodules", "[submodule]\n", 0o644)
	write(t, src, "ro.txt", "fixed\n", 0o444)
	write(t, src, "tool.sh", "#!/bin/sh\n", 0o755)
	before, _ := Snapshot(src)
	w, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(w.Root, "sub/repo/.git")); err == nil {
		t.Fatal("nested .git copied")
	}
	if _, err := os.Lstat(filepath.Join(w.Root, "sub/repo/.gitmodules")); err != nil {
		t.Fatal(".gitmodules is an ordinary file and must be copied")
	}
	if info, _ := os.Stat(filepath.Join(w.Root, "ro.txt")); info.Mode().Perm() != 0o444 {
		t.Fatalf("read-only mode lost: %v", info.Mode())
	}
	if _, err := ReplaceExpecting(w.Root, "ro.txt", "fixed", "broken", ""); err == nil {
		t.Fatal("read-only target accepted")
	}
	if _, err := Replace(w.Root, "sub/repo/file.txt", "SAFE", "BAD"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := Snapshot(src)
	if d := Diff(before, after); len(d) != 0 {
		t.Fatalf("source changed: %v", d)
	}
}

// ReadTarget applies exactly the target checks ReplaceExpecting applies.
func TestReadTargetMatchesReplaceValidation(t *testing.T) {
	w := prepared(t, map[string]string{"a.txt": "SAFE\n", "dir/b.txt": "SAFE\n", "big.txt": "x"})
	if err := os.WriteFile(filepath.Join(w.Root, "big.txt"), []byte(strings.Repeat("z", MaxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(w.Root, "ro.txt"), []byte("SAFE\n"), 0o444)
	os.Symlink(filepath.Join(w.Root, "a.txt"), filepath.Join(w.Root, "link.txt"))
	os.Symlink(filepath.Join(w.Root, "dir"), filepath.Join(w.Root, "linkdir"))
	os.Mkdir(filepath.Join(w.Root, "emptydir"), 0o700)
	data, err := ReadTarget(w.Root, "dir/b.txt")
	if err != nil || string(data) != "SAFE\n" {
		t.Fatalf("read: %q %v", data, err)
	}
	for _, path := range []string{"", "/etc/passwd", "../a.txt", "dir/../a.txt", "./a.txt", "a\\b", "c:x", "missing.txt",
		"link.txt", "linkdir/b.txt", "emptydir", "ro.txt", "big.txt"} {
		_, rerr := ReadTarget(w.Root, path)
		_, werr := ReplaceExpecting(w.Root, path, "SAFE", "BAD", "")
		if rerr == nil || werr == nil {
			t.Errorf("%q: read err=%v, replace err=%v; both must refuse", path, rerr, werr)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(w.Root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTarget(alias, "a.txt"); err == nil {
		t.Error("non-canonical root accepted")
	}
}
