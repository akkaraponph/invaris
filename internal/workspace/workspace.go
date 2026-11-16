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
	"context"
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
	return PrepareContext(context.Background(), source)
}

// PrepareContext is Prepare that stops when ctx ends. The context is checked
// before every entry and between bounded chunks of every file, so a canceled
// copy stops promptly and its partial workspace is removed; the returned
// error wraps ctx.Err(). Cancellation of an already-prepared workspace is the
// caller's concern (Close still removes it).
func PrepareContext(ctx context.Context, source string) (*Workspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("prepare workspace: %w", err)
	}
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
	if err := copyTree(ctx, src, root, &count); err != nil {
		return fail(fmt.Errorf("copy workspace: %w", err))
	}
	return w, nil
}

// copyTree does not follow symbolic links: a link is refused, which keeps a
// copy from escaping into (or exposing) files outside the checkout.
func copyTree(ctx context.Context, src, dst string, count *int) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".invaris" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
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
			if err := copyTree(ctx, from, to, count); err != nil {
				return err
			}
		case !info.Mode().IsRegular() || info.Size() > MaxFileBytes:
			return fmt.Errorf("unsupported or oversized file %s", from)
		default:
			if err := copyFile(ctx, from, to, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyChunk bounds how much is copied between context checks.
const copyChunk = 1 << 20

// ctxReader returns ctx.Err() before each read once ctx has ended.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > copyChunk {
		p = p[:copyChunk]
	}
	return c.r.Read(p)
}

func copyFile(ctx context.Context, from, to string, perm os.FileMode) error {
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
	n, err := io.Copy(out, ctxReader{ctx, io.LimitReader(in, MaxFileBytes+1)})
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
	if before == "" || before == after {
		return m, errors.New("mutation must replace a non-empty marker with different text")
	}
	target, info, err := resolveTarget(root, path)
	if err != nil {
		return m, err
	}
	data, err := readBounded(target)
	if err != nil {
		return m, err
	}
	if preimage != "" && hashBytes(data) != preimage {
		return m, errors.New("target content does not match the declared preimage digest")
	}
	if err := CheckMarker(data, before); err != nil {
		return m, err
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

// CheckMarker reports whether before is a usable marker in data: non-empty and
// occurring exactly once. It is the occurrence rule ReplaceExpecting applies.
func CheckMarker(data []byte, before string) error {
	if before == "" {
		return errors.New("mutation must replace a non-empty marker with different text")
	}
	marker := []byte(before)
	first := bytes.Index(data, marker)
	if first < 0 || bytes.Contains(data[first+1:], marker) {
		return errors.New("before marker must occur exactly once")
	}
	return nil
}

// ResolveTarget applies the call-time target validation to path under root
// and returns the resolved file path. It reads nothing and writes nothing.
func ResolveTarget(root, path string) (string, error) {
	target, _, err := resolveTarget(root, path)
	return target, err
}

// CheckCopyable walks source read-only and reports the first reason
// PrepareContext would refuse to copy it, applying the same rules as
// copyTree. Paths in errors are relative to source. Nothing is created.
func CheckCopyable(source string) error {
	src, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return errors.New("source is not a directory")
	}
	count := 0
	return checkTree(src, "", &count)
}

func checkTree(dir, rel string, count *int) error {
	entries, err := os.ReadDir(dir)
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
		name := entry.Name()
		if rel != "" {
			name = rel + "/" + name
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("symlink not permitted: %s", name)
		case info.IsDir():
			if err := checkTree(filepath.Join(dir, entry.Name()), name, count); err != nil {
				return err
			}
		case !info.Mode().IsRegular() || info.Size() > MaxFileBytes:
			return fmt.Errorf("unsupported or oversized file %s", name)
		default:
			f, err := os.Open(filepath.Join(dir, entry.Name()))
			if err != nil {
				var pe *fs.PathError
				if errors.As(err, &pe) {
					err = pe.Err
				}
				return fmt.Errorf("unreadable file %s: %w", name, err)
			}
			_ = f.Close()
		}
	}
	return nil
}

// resolveTarget validates a mutation target at call time: a canonical
// relative path under a canonical root that resolves to itself without
// symbolic links, naming a bounded, owner-writable regular file. Every
// engine read or write of a target goes through it.
func resolveTarget(root, path string) (string, os.FileInfo, error) {
	if !ValidPath(path) {
		return "", nil, fmt.Errorf("unsafe mutation path %q", path)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return "", nil, errors.New("workspace root is not canonical")
	}
	target := filepath.Join(root, filepath.FromSlash(path))
	if resolved, err := filepath.EvalSymlinks(target); err != nil || resolved != target {
		return "", nil, errors.New("mutation target unavailable or redirected")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return "", nil, errors.New("mutation target is not a bounded regular file")
	}
	if info.Mode().Perm()&0o200 == 0 {
		return "", nil, errors.New("mutation target is not writable")
	}
	return target, info, nil
}

// readBounded reads at most MaxFileBytes, refusing a file that grew past it.
func readBounded(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err == nil && len(data) > MaxFileBytes {
		err = errors.New("mutation target is not a bounded regular file")
	}
	return data, err
}

// ReadTarget returns the bytes of the mutation target at path under root,
// after the same call-time validation ReplaceExpecting applies. Built-in
// operators plan their edit from these bytes; ReplaceExpecting re-validates
// the target and pins the preimage when it writes.
func ReadTarget(root, path string) ([]byte, error) {
	target, _, err := resolveTarget(root, path)
	if err != nil {
		return nil, err
	}
	return readBounded(target)
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

// ManifestDigest returns a deterministic SHA-256 over a manifest: every path in
// sorted order with its kind, mode, content hash and link target. Equal digests
// mean equal manifests; the digest identifies content and is not attestation.
func ManifestDigest(m map[string]Entry) string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		e := m[p]
		fmt.Fprintf(h, "%d:%s\x00%s\x00%o\x00%s\x00%s\x00", len(p), p, e.Kind, e.Mode, e.SHA256, e.Link)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Subtree returns the entries of m below dir, with paths relative to dir, as
// Snapshot would record them for a copy of that directory. Dir "." returns m.
func Subtree(m map[string]Entry, dir string) map[string]Entry {
	if dir == "." {
		return m
	}
	prefix := dir + "/"
	out := map[string]Entry{}
	for p, e := range m {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			out[rest] = e
		}
	}
	return out
}
