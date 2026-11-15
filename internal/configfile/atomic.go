// Package configfile publishes configuration files written by the authoring
// commands (init, migrate). It never overwrites a file it did not expect:
// Create publishes only to an absent name, and ReplaceWithBackup keeps an
// exact backup and replaces the original only while it still holds the
// expected bytes.
//
// Limits: these checks serialize cooperating Invaris processes and detect
// ordinary concurrent edits; they are not a defense against a hostile local
// process racing the same files. Power loss can leave the documented temporary,
// lock and backup files behind.
package configfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// file is the subset of *os.File used to stage a file.
type file interface {
	Write(p []byte) (int, error)
	Sync() error
	Chmod(mode fs.FileMode) error
	Close() error
	Name() string
}

// ops holds the filesystem operations so tests can inject failures.
// Production code always uses the os implementations.
type ops struct {
	CreateTemp func(dir, pattern string) (file, error)
	OpenFile   func(name string, flag int, perm fs.FileMode) (file, error)
	Link       func(oldname, newname string) error
	Rename     func(oldname, newname string) error
	Remove     func(name string) error
	SyncDir    func(dir string) error
}

var osOps = ops{
	CreateTemp: func(dir, pattern string) (file, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err // never a non-nil interface holding a nil *os.File
		}
		return f, nil
	},
	OpenFile: func(name string, flag int, perm fs.FileMode) (file, error) {
		f, err := os.OpenFile(name, flag, perm)
		if err != nil {
			return nil, err
		}
		return f, nil
	},
	Link:    os.Link,
	Rename:  os.Rename,
	Remove:  os.Remove,
	SyncDir: syncDir,
}

var fsys = osOps

// ErrExists reports that a destination is already occupied.
var ErrExists = errors.New("already exists")

// ErrNotSynced reports that Create published the file but could not sync its
// directory: the file exists and may not survive a crash.
var ErrNotSynced = errors.New("created but directory not synced")

// syncDir flushes a directory entry change on Linux and macOS.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// stage writes data to a new temporary file in dir with mode and returns its
// name. The temporary file is removed on failure.
func stage(dir, pattern string, data []byte, mode fs.FileMode) (string, error) {
	f, err := fsys.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if rerr := fsys.Remove(name); rerr != nil {
			err = errors.Join(err, fmt.Errorf("remove temporary file %s: %w", name, rerr))
		}
		return "", err
	}
	return name, nil
}

// Create publishes data at path with mode (subject to the filesystem) only if
// nothing exists at path, including a directory or a dangling symbolic link.
// The bytes are staged in a temporary file in the same directory and published
// with a hard link, which fails if the name is taken, so two concurrent
// creators cannot both succeed and no partial file is ever visible at path.
// A filesystem without hard links is refused rather than overwritten.
func Create(path string, data []byte, mode fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s %w", path, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temp, err := stage(dir, ".invaris-create-*", data, mode)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := fsys.Remove(temp); rerr != nil {
			err = errors.Join(err, fmt.Errorf("remove temporary file %s: %w", temp, rerr))
		}
	}()
	if err := fsys.Link(temp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s %w", path, ErrExists)
		}
		return fmt.Errorf("publish %s (hard links are required): %w", path, err)
	}
	if err := fsys.SyncDir(dir); err != nil {
		return fmt.Errorf("%s: %w: %w", path, ErrNotSynced, err)
	}
	return nil
}

// maxConfigBytes bounds the files ReplaceWithBackup reads.
const maxConfigBytes = 1 << 20

// ErrReplaced reports that the rename completed but a later step failed, so
// the new bytes may already be in place. The backup is kept for recovery.
var ErrReplaced = errors.New("replacement may have completed")

// readBounded reads a regular file of at most maxConfigBytes.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err == nil && len(data) > maxConfigBytes {
		err = errors.New("file exceeds 1 MiB")
	}
	return data, err
}

// ReplaceWithBackup replaces the regular file at path, which must currently
// hold exactly expected, with newBytes, keeping its permission bits:
//
//  1. refuse a symbolic link, special file, file with more than one hard link,
//     or a file without owner write permission;
//  2. take an exclusive lock file <path>.migrate.lock (removed on return; a
//     stale lock is never removed automatically);
//  3. create <path>.bak exclusively with the original bytes and mode (an
//     existing backup, even with matching bytes, is a collision);
//  4. stage newBytes in a temporary file in the same directory, fsync it;
//  5. check that path is still the same file with the expected bytes, then
//     rename the temporary file over it and fsync the directory.
//
// A failure before the rename leaves the original untouched and removes the
// backup and temporary file this call created. A failure after the rename
// wraps ErrReplaced: the replacement may have completed and the backup is
// kept. Restoring a backup is a manual, reviewed step.
func ReplaceWithBackup(path string, expected, newBytes []byte) (err error) {
	dir := filepath.Dir(path)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	switch {
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file (symbolic links and special files are refused)", path)
	case linkCount(info) > 1:
		return fmt.Errorf("%s has %d hard links; refusing to replace a shared file", path, linkCount(info))
	case info.Mode().Perm()&0o200 == 0:
		return fmt.Errorf("%s is not writable", path)
	}
	mode := info.Mode().Perm()
	current, err := readBounded(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("%s changed since it was read", path)
	}

	lock := path + ".migrate.lock"
	lf, err := fsys.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("lock %s exists: another migration is running, or a previous one stopped abruptly; remove it only after confirming no migration is active", lock)
		}
		return fmt.Errorf("create lock %s: %w", lock, err)
	}
	if err := lf.Close(); err != nil {
		return errors.Join(fmt.Errorf("create lock %s: %w", lock, err), removeNamed(lock, "lock"))
	}
	defer func() { err = errors.Join(err, removeNamed(lock, "lock")) }()

	backup := path + ".bak"
	if err := Create(backup, expected, mode); err != nil {
		if errors.Is(err, ErrNotSynced) {
			// This call created the backup; remove it so a retry is possible.
			return errors.Join(fmt.Errorf("create backup: %w", err), removeNamed(backup, "backup"))
		}
		if errors.Is(err, ErrExists) {
			return fmt.Errorf("backup %s already exists; move it away after reviewing it", backup)
		}
		return fmt.Errorf("create backup: %w", err)
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			err = errors.Join(err, removeNamed(backup, "backup"))
		}
	}()
	if saved, err := readBounded(backup); err != nil || !bytes.Equal(saved, expected) {
		return errors.Join(errors.New("backup does not hold the original bytes"), err)
	}

	temp, err := stage(dir, ".invaris-migrate-*", newBytes, mode)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			err = errors.Join(err, removeNamed(temp, "temporary file"))
		}
	}()
	again, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, again) || again.Mode() != info.Mode() {
		return errors.Join(fmt.Errorf("%s was replaced or changed during migration", path), err)
	}
	if current, err := readBounded(path); err != nil || !bytes.Equal(current, expected) {
		return errors.Join(fmt.Errorf("%s changed during migration", path), err)
	}
	if err := fsys.Rename(temp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	renamed, keepBackup = true, true
	if err := fsys.SyncDir(dir); err != nil {
		return fmt.Errorf("%w: %s was renamed but its directory could not be synced (%v); backup kept at %s", ErrReplaced, path, err, backup)
	}
	return nil
}

func removeNamed(name, role string) error {
	if err := fsys.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s %s: %w", role, name, err)
	}
	return nil
}
