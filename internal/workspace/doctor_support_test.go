package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCheckMarkerMatchesReplace(t *testing.T) {
	cases := []struct{ name, data, before string }{
		{"none", "hello", "x"},
		{"once", "hello x", "x"},
		{"twice", "x hello x", "x"},
		{"overlapping", "aaa", "aa"},
		{"empty", "hello", ""},
		{"marker longer than data", "a", "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := prepared(t, map[string]string{"f.txt": c.data})
			_, replaceErr := ReplaceExpecting(w.Root, "f.txt", c.before, c.before+"!", "")
			checkErr := CheckMarker([]byte(c.data), c.before)
			if (checkErr != nil) != (replaceErr != nil) {
				t.Fatalf("CheckMarker %v, ReplaceExpecting %v", checkErr, replaceErr)
			}
			if checkErr != nil && replaceErr != nil && checkErr.Error() != replaceErr.Error() {
				t.Fatalf("messages differ: %q vs %q", checkErr, replaceErr)
			}
		})
	}
	if CheckMarker([]byte("aaa"), "aa") == nil {
		t.Fatal("overlapping occurrences must be refused")
	}
}

func TestCheckCopyableMatchesPrepare(t *testing.T) {
	build := map[string]func(t *testing.T, dir string) string{
		"clean": func(t *testing.T, dir string) string {
			write(t, dir, "a/b.txt", "x", 0o644)
			return ""
		},
		"git and invaris skipped": func(t *testing.T, dir string) string {
			write(t, dir, "a.txt", "x", 0o644)
			write(t, dir, ".git/big", "x", 0o644)
			if err := os.Symlink("/nonexistent", filepath.Join(dir, ".git", "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/nonexistent", filepath.Join(dir, ".invaris")); err != nil {
				t.Fatal(err)
			}
			return ""
		},
		"symlink": func(t *testing.T, dir string) string {
			write(t, dir, "adapters/x/node_modules/.bin/real", "x", 0o755)
			if err := os.Symlink("real", filepath.Join(dir, "adapters/x/node_modules/.bin/y")); err != nil {
				t.Fatal(err)
			}
			return "symlink not permitted: adapters/x/node_modules/.bin/y" + symlinkHint
		},
		"too many entries": func(t *testing.T, dir string) string {
			for i := 0; i < MaxEntries+1; i++ {
				write(t, dir, "f"+strconv.Itoa(i), "", 0o644)
			}
			return limitError(MaxEntries).Error()
		},
		"exactly the entry limit": func(t *testing.T, dir string) string {
			for i := 0; i < MaxEntries; i++ {
				write(t, dir, "f"+strconv.Itoa(i), "", 0o644)
			}
			return ""
		},
		"oversized file": func(t *testing.T, dir string) string {
			write(t, dir, "sub/big.bin", "", 0o644)
			if err := os.Truncate(filepath.Join(dir, "sub/big.bin"), MaxFileBytes+1); err != nil {
				t.Fatal(err)
			}
			return "unsupported or oversized file sub/big.bin"
		},
		"file at the size limit": func(t *testing.T, dir string) string {
			write(t, dir, "big.bin", "", 0o644)
			if err := os.Truncate(filepath.Join(dir, "big.bin"), MaxFileBytes); err != nil {
				t.Fatal(err)
			}
			return ""
		},
	}
	for name, mk := range build {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			want := mk(t, dir)
			err := CheckCopyable(dir)
			w, prepErr := Prepare(dir)
			if w != nil {
				_ = w.Close()
			}
			if (err != nil) != (prepErr != nil) {
				t.Fatalf("CheckCopyable %v, Prepare %v", err, prepErr)
			}
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != want {
				t.Fatalf("got %v, want %q", err, want)
			}
			if strings.Contains(err.Error(), dir) {
				t.Fatalf("error must be relative: %v", err)
			}
		})
	}
}

func TestResolveTarget(t *testing.T) {
	w := prepared(t, map[string]string{"a/b.txt": "x"})
	got, err := ResolveTarget(w.Root, "a/b.txt")
	if err != nil || got != filepath.Join(w.Root, "a", "b.txt") {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"../x", "/abs", "missing.txt", "a"} {
		if _, err := ResolveTarget(w.Root, bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	link := filepath.Join(w.Root, "l")
	if err := os.Symlink("a/b.txt", link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTarget(w.Root, "l"); err == nil {
		t.Fatal("symlink target must be refused")
	}
}

func TestCheckCopyableMatchesPrepareForUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	dir := t.TempDir()
	write(t, dir, "a/secret.txt", "x", 0o000)
	err := CheckCopyable(dir)
	w, prepErr := Prepare(dir)
	if w != nil {
		_ = w.Close()
	}
	if prepErr == nil {
		t.Fatal("Prepare must refuse an unreadable file")
	}
	if err == nil {
		t.Fatal("CheckCopyable must refuse an unreadable file")
	}
	if strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "a/secret.txt") {
		t.Fatalf("error must name the relative path only: %v", err)
	}
}
