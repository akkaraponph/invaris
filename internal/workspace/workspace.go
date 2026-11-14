// Package workspace prepares the disposable copy in which a proof runs and
// performs the one engine-managed mutation inside it.
//
// The copier is deliberately restrictive: it refuses symbolic links and
// non-regular files and bounds entry count and file size, rather than trying
// to reproduce a whole repository. The source checkout is only read.
//
// Limits: this is not a sandbox. Commands that later run in the copy are
// trusted local processes that can still reach absolute paths or the network,
// and a concurrent local process could race the path checks in Replace. The
// manifest comparison in Replace verifies the mutation operation itself, not
// containment of those commands.
package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxEntries bounds the files and directories copied (and snapshotted).
	MaxEntries = 2000
	// MaxFileBytes bounds each copied or mutated file.
	MaxFileBytes = 10 << 20
)

// Workspace is a disposable copy of a source checkout. Root is canonical (no
// symbolic links in the path) and is never the source.
type Workspace struct {
	Root   string
	source string
}

// Mutation describes one replacement. FaultID is left empty by Replace: it is
// derived by the caller, which knows the policy.
type Mutation struct {
	Path, FaultID, BeforeSHA256, AfterSHA256 string
}

// Prepare copies source into a new temporary directory. On any failure the
// partial copy is removed (and a removal failure is reported with the error).
func Prepare(source string) (*Workspace, error) {
	src, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return nil, errors.New("source is not a directory")
	}
	tmp, err := os.MkdirTemp("", "invaris-proof-*")
	if err != nil {
		return nil, err
	}
	w := &Workspace{source: src}
	fail := func(err error) (*Workspace, error) {
		if rerr := os.RemoveAll(tmp); rerr != nil {
			err = errors.Join(err, fmt.Errorf("remove partial workspace: %w", rerr))
		}
		return nil, err
	}
	root, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return fail(err)
	}
	if root == src || strings.HasPrefix(root+string(filepath.Separator), src+string(filepath.Separator)) {
		return fail(errors.New("temporary directory is inside the source checkout"))
	}
	w.Root = root
	count := 0
	if err := copyTree(src, root, &count); err != nil {
		return fail(fmt.Errorf("copy workspace: %w", err))
	}
	return w, nil
}

// copyTree does not follow symbolic links: a link is refused, which keeps a
// copy from escaping into (or exposing) files outside the checkout.
func copyTree(src, dst string, count *int) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".invaris" {
			continue
		}
		if *count++; *count > MaxEntries {
			return fmt.Errorf("workspace exceeds %d entries", MaxEntries)
		}
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("symlink not permitted: %s", from)
		case info.IsDir():
			if err := os.Mkdir(to, 0o700); err != nil {
				return err
			}
			if err := copyTree(from, to, count); err != nil {
				return err
			}
		case !info.Mode().IsRegular() || info.Size() > MaxFileBytes:
			return fmt.Errorf("unsupported or oversized file %s", from)
		default:
			if err := copyFile(from, to, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	// A mode without owner write is applied after the copy completes.
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// Read one byte past the limit so a file that grew since Stat is refused.
	n, err := io.Copy(out, io.LimitReader(in, MaxFileBytes+1))
	if err == nil && n > MaxFileBytes {
		err = fmt.Errorf("oversized file %s", from)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Chmod(to, perm)
}

// Close removes the workspace and verifies that it is gone. It is safe to call
// twice. It refuses to remove an empty path or the source checkout.
func (w *Workspace) Close() error {
	if w == nil || w.Root == "" {
		return nil
	}
	if w.Root == w.source || w.Root == string(filepath.Separator) {
		return errors.New("refusing to remove the source checkout")
	}
	if err := os.RemoveAll(w.Root); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	if _, err := os.Lstat(w.Root); err == nil {
		return errors.New("workspace still exists after removal")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("verify workspace removal: %w", err)
	}
	return nil
}

// ValidPath reports whether name is a canonical relative slash path.
func ValidPath(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Replace changes exactly one occurrence of before to after in the regular file
// at path under root, and returns the path with the SHA-256 of the file before
// and after. The target is re-validated at call time (after any baseline run
// may have altered the copy): it must still resolve to itself without symbolic
// links, be a bounded owner-writable regular file, and contain before exactly
// once. The file mode is preserved. The manifest of the whole workspace is
// compared before and after the write, and any difference other than the one
// declared file is an error.
func Replace(root, path, before, after string) (Mutation, error) {
	return ReplaceExpecting(root, path, before, after, "")
}

// ReplaceExpecting is Replace with an optional precondition: when preimage is
// non-empty it must equal the lowercase hex SHA-256 of the whole target file
// as found at call time, or nothing is written. This pins the experiment to
// the exact content it was authored against. The digest identifies content;
// it is not an attestation of who produced it.
func ReplaceExpecting(root, path, before, after, preimage string) (Mutation, error) {
	var m Mutation
	if !ValidPath(path) {
		return m, fmt.Errorf("unsafe mutation path %q", path)
	}
	if before == "" || before == after {
		return m, errors.New("mutation must replace a non-empty marker with different text")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return m, errors.New("workspace root is not canonical")
	}
	target := filepath.Join(root, filepath.FromSlash(path))
	if resolved, err := filepath.EvalSymlinks(target); err != nil || resolved != target {
		return m, errors.New("mutation target unavailable or redirected")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return m, errors.New("mutation target is not a bounded regular file")
	}
	if info.Mode().Perm()&0o200 == 0 {
		return m, errors.New("mutation target is not writable")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return m, err
	}
	if preimage != "" && hashBytes(data) != preimage {
		return m, errors.New("target content does not match the declared preimage digest")
	}
	marker := []byte(before)
	first := bytes.Index(data, marker)
	if first < 0 || bytes.Contains(data[first+1:], marker) {
		return m, errors.New("before marker must occur exactly once")
	}
	manifestBefore, err := Snapshot(root)
	if err != nil {
		return m, fmt.Errorf("snapshot before mutation: %w", err)
	}
	modified := bytes.Replace(data, []byte(before), []byte(after), 1)
	if len(modified) > MaxFileBytes {
		return m, errors.New("mutated file would exceed the size limit")
	}
	if err := writeReplacement(target, modified, info.Mode().Perm()); err != nil {
		return m, err
	}
	afterWrite(root)
	manifestAfter, err := Snapshot(root)
	if err != nil {
		return m, fmt.Errorf("snapshot after mutation: %w", err)
	}
	if changed := Diff(manifestBefore, manifestAfter); len(changed) != 1 || changed[0] != path {
		return m, fmt.Errorf("mutation changed %v instead of exactly %q", changed, path)
	}
	m = Mutation{Path: path, BeforeSHA256: hashBytes(data), AfterSHA256: hashBytes(modified)}
	if got := manifestAfter[path].SHA256; got != m.AfterSHA256 || manifestBefore[path].SHA256 != m.BeforeSHA256 {
		return Mutation{}, errors.New("recorded hashes disagree with the workspace manifest")
	}
	return m, nil
}

// Rename installs a new inode so a hard link made by a trusted command
// cannot turn the engine's replacement into a write to the source.
func writeReplacement(target string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".invaris-replace-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Chmod(mode), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// afterWrite is a test seam run between the write and the second snapshot, so
// tests can simulate an undeclared change made during the mutation.
var afterWrite = func(root string) {}

// Entry is one manifest record. SHA256 is set for regular files only, and Link
// for symbolic links.
type Entry struct {
	Kind   string // "file", "dir", "symlink" or "other"
	Mode   fs.FileMode
	SHA256 string
	Link   string
}

// Snapshot records every entry under root without following symbolic links.
func Snapshot(root string) (map[string]Entry, error) {
	out := map[string]Entry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if len(out) >= MaxEntries {
			return fmt.Errorf("workspace exceeds %d entries", MaxEntries)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			if e.Link, err = os.Readlink(p); err != nil {
				return err
			}
		case info.IsDir():
			e.Kind = "dir"
		case info.Mode().IsRegular():
			e.Kind = "file"
			if info.Size() > MaxFileBytes {
				return fmt.Errorf("oversized file %s", rel)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			e.SHA256 = hashBytes(data)
		default:
			e.Kind = "other"
		}
		out[filepath.ToSlash(rel)] = e
		return nil
	})
	return out, err
}

// Diff lists, sorted, the paths added, removed or changed between manifests.
func Diff(a, b map[string]Entry) []string {
	var changed []string
	for p, ea := range a {
		if eb, ok := b[p]; !ok || ea != eb {
			changed = append(changed, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
