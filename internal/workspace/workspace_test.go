package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func prepared(t *testing.T, files map[string]string) *Workspace {
	t.Helper()
	src := t.TempDir()
	for n, b := range files {
		write(t, src, n, b, 0o644)
	}
	w, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestPrepareCopiesAndNeverTouchesSource(t *testing.T) {
	src := t.TempDir()
	write(t, src, "a/b.txt", "hello", 0o640)
	write(t, src, "run.sh", "#!/bin/sh\n", 0o755)
	write(t, src, ".git/config", "ignored", 0o644)
	write(t, src, ".invaris/x", "ignored", 0o644)
	w, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	if w.Root == src || filepath.Clean(w.Root) != w.Root {
		t.Fatalf("workspace root %q must be a distinct canonical path", w.Root)
	}
	got, err := os.ReadFile(filepath.Join(w.Root, "a", "b.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("copy missing: %v %q", err, got)
	}
	if info, _ := os.Stat(filepath.Join(w.Root, "run.sh")); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
	for _, skipped := range []string{".git", ".invaris"} {
		if _, err := os.Lstat(filepath.Join(w.Root, skipped)); err == nil {
			t.Errorf("%s must not be copied", skipped)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(w.Root); err == nil {
		t.Fatal("workspace exists after Close")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "a", "b.txt")); string(b) != "hello" {
		t.Fatal("source changed")
	}
}

func TestPrepareLimits(t *testing.T) {
	t.Run("symlink refused", func(t *testing.T) {
		src := t.TempDir()
		write(t, src, "real", "x", 0o644)
		if err := os.Symlink("real", filepath.Join(src, "link")); err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare(src); err == nil || !strings.Contains(err.Error(), "symlink not permitted") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("too many entries", func(t *testing.T) {
		src := t.TempDir()
		for i := 0; i <= MaxEntries; i++ {
			write(t, src, "f"+strings.Repeat("0", 4)+"/"+hex.EncodeToString([]byte{byte(i >> 8), byte(i)}), "", 0o644)
		}
		if _, err := Prepare(src); err == nil || !strings.Contains(err.Error(), "workspace exceeds 2000 entries") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		src := t.TempDir()
		write(t, src, "big", strings.Repeat("x", MaxFileBytes+1), 0o644)
		if _, err := Prepare(src); err == nil || !strings.Contains(err.Error(), "unsupported or oversized file") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("file at the limit is accepted", func(t *testing.T) {
		src := t.TempDir()
		write(t, src, "big", strings.Repeat("x", MaxFileBytes), 0o644)
		w, err := Prepare(src)
		if err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
	})
	t.Run("not a directory", func(t *testing.T) {
		src := t.TempDir()
		write(t, src, "f", "x", 0o644)
		if _, err := Prepare(filepath.Join(src, "f")); err == nil {
			t.Fatal("file accepted as source")
		}
	})
	t.Run("failed copy leaves no temp directory", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		src := t.TempDir()
		write(t, src, "big", strings.Repeat("x", MaxFileBytes+1), 0o644)
		if _, err := Prepare(src); err == nil {
			t.Fatal("expected failure")
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Fatalf("partial workspace left behind: %v", left)
		}
	})
	t.Run("temp inside source refused", func(t *testing.T) {
		src := t.TempDir()
		t.Setenv("TMPDIR", src)
		if _, err := Prepare(src); err == nil || !strings.Contains(err.Error(), "temporary directory is inside the source") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestReplace(t *testing.T) {
	w := prepared(t, map[string]string{"src/a.txt": "SAFE\n", "other.txt": "SAFE\n"})
	m, err := Replace(w.Root, "src/a.txt", "SAFE", "BAD")
	if err != nil {
		t.Fatal(err)
	}
	if m.Path != "src/a.txt" || m.BeforeSHA256 != sum("SAFE\n") || m.AfterSHA256 != sum("BAD\n") || m.FaultID != "" {
		t.Fatalf("mutation = %+v", m)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Root, "src", "a.txt")); string(b) != "BAD\n" {
		t.Fatalf("content %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Root, "other.txt")); string(b) != "SAFE\n" {
		t.Fatal("an undeclared file changed")
	}
}

func TestReplacePreservesMode(t *testing.T) {
	src := t.TempDir()
	write(t, src, "x.sh", "SAFE", 0o750)
	w, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := Replace(w.Root, "x.sh", "SAFE", "BAD"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(w.Root, "x.sh")); info.Mode().Perm() != 0o750 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

func TestReplaceRefusals(t *testing.T) {
	w := prepared(t, map[string]string{"once": "A B", "twice": "A A", "overlap": "aaa", "none": "C", "dir/x": "A"})
	write(t, w.Root, "ro", "A", 0o444)
	external := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(external, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(w.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(w.Root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ path, before, after string }{
		"overlapping marker":     {"overlap", "aa", "bb"},
		"ambiguous marker":       {"twice", "A", "Z"},
		"absent marker":          {"none", "A", "Z"},
		"empty marker":           {"once", "", "Z"},
		"identical replacement":  {"once", "A", "A"},
		"unwritable target":      {"ro", "A", "Z"},
		"symlink target":         {"link", "A", "Z"},
		"directory symlink":      {"dirlink/x", "A", "Z"},
		"missing target":         {"missing", "A", "Z"},
		"directory target":       {"dir", "A", "Z"},
		"parent escape":          {"../outside", "A", "Z"},
		"absolute path":          {"/etc/hosts", "A", "Z"},
		"backslash path":         {`a\b`, "A", "Z"},
		"non-canonical path":     {"dir//x", "A", "Z"},
		"current directory part": {"./once", "A", "Z"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before, _ := Snapshot(w.Root)
			if _, err := Replace(w.Root, tc.path, tc.before, tc.after); err == nil {
				t.Fatal("expected refusal")
			}
			after, _ := Snapshot(w.Root)
			if d := Diff(before, after); len(d) != 0 {
				t.Fatalf("refused replacement still changed %v", d)
			}
		})
	}
	if b, _ := os.ReadFile(external); string(b) != "A" {
		t.Fatal("external sentinel was modified")
	}
	t.Run("non-canonical root", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(w.Root, alias); err != nil {
			t.Fatal(err)
		}
		if _, err := Replace(alias, "once", "A", "Z"); err == nil {
			t.Fatal("symlinked root accepted")
		}
	})
	t.Run("size limit", func(t *testing.T) {
		big := prepared(t, map[string]string{"f": "A" + strings.Repeat("x", MaxFileBytes-1)})
		if _, err := Replace(big.Root, "f", "A", strings.Repeat("y", 10)); err == nil {
			t.Fatal("growth past the limit accepted")
		}
	})
}

func TestSnapshotAndDiff(t *testing.T) {
	w := prepared(t, map[string]string{"a": "1", "d/b": "2"})
	s1, err := Snapshot(w.Root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Root, "new", "3", 0o644)
	if err := os.Remove(filepath.Join(w.Root, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(w.Root, "d", "b"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, _ := Snapshot(w.Root)
	if got := strings.Join(Diff(s1, s2), ","); got != "a,d/b,new" {
		t.Fatalf("diff = %s (added, removed and mode-changed files must all show)", got)
	}
}

func TestCloseFailureAndSafety(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root from removing files")
	}
	src := t.TempDir()
	write(t, src, "locked/f", "x", 0o644)
	w, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(w.Root, "locked")
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700); _ = w.Close() })
	if err := w.Close(); err == nil {
		t.Fatal("Close reported success while the workspace still exists")
	}
	if _, err := os.Lstat(w.Root); err != nil {
		t.Fatal("expected the undeletable workspace to remain")
	}

	if err := (&Workspace{Root: src, source: src}).Close(); err == nil {
		t.Fatal("Close removed (or tried to remove) the source checkout")
	}
	if _, err := os.Stat(filepath.Join(src, "locked", "f")); err != nil {
		t.Fatal("source damaged")
	}
	if err := (*Workspace)(nil).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceDetectsUndeclaredChange(t *testing.T) {
	// The manifest check exists to catch a mutation that touched more than the
	// declared file. Exercise Diff with the shapes Replace relies on.
	a := map[string]Entry{"x": {Kind: "file", SHA256: "1"}, "y": {Kind: "file", SHA256: "2"}}
	b := map[string]Entry{"x": {Kind: "file", SHA256: "9"}, "y": {Kind: "file", SHA256: "2"}}
	if got := Diff(a, b); len(got) != 1 || got[0] != "x" {
		t.Fatalf("got %v", got)
	}
	b["z"] = Entry{Kind: "file"}
	if got := Diff(a, b); len(got) != 2 {
		t.Fatalf("an added file must be reported: %v", got)
	}
	if !bytes.Equal([]byte(sum("")), []byte("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")) {
		t.Fatal("hash helper")
	}
}

func TestReplaceRejectsUndeclaredChangeDuringMutation(t *testing.T) {
	for name, extra := range map[string]func(root string){
		"added file":   func(root string) { _ = os.WriteFile(filepath.Join(root, "extra"), []byte("x"), 0o644) },
		"changed file": func(root string) { _ = os.WriteFile(filepath.Join(root, "other"), []byte("changed"), 0o644) },
		"removed file": func(root string) { _ = os.Remove(filepath.Join(root, "other")) },
		"new symlink":  func(root string) { _ = os.Symlink("target", filepath.Join(root, "ln")) },
	} {
		t.Run(name, func(t *testing.T) {
			w := prepared(t, map[string]string{"t": "SAFE", "other": "o"})
			afterWrite = extra
			defer func() { afterWrite = func(string) {} }()
			if _, err := Replace(w.Root, "t", "SAFE", "BAD"); err == nil || !strings.Contains(err.Error(), "instead of exactly") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestReplaceRejectsRewriteOfTargetDuringMutation(t *testing.T) {
	// Only the declared file differs, but its bytes are not the ones Replace
	// wrote: the recorded after-hash must not be trusted.
	w := prepared(t, map[string]string{"t": "SAFE"})
	afterWrite = func(root string) { _ = os.WriteFile(filepath.Join(root, "t"), []byte("SOMETHING ELSE"), 0o644) }
	defer func() { afterWrite = func(string) {} }()
	if _, err := Replace(w.Root, "t", "SAFE", "BAD"); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("got %v", err)
	}
}

func TestReplaceDoesNotWriteThroughHardLink(t *testing.T) {
	w := prepared(t, map[string]string{"file": "old"})
	source := filepath.Join(filepath.Dir(w.Root), filepath.Base(w.Root)+"-hardlink")
	if err := os.Link(filepath.Join(w.Root, "file"), source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(source) })
	if _, err := Replace(w.Root, "file", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(source); err != nil || string(content) != "old" {
		t.Fatalf("linked file changed: %q %v", content, err)
	}
}
