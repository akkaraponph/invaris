package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// tempFile is the subset of *os.File used to stage a report.
type tempFile interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
	Name() string
}

// reportFS holds the filesystem operations used for report publication so tests
// can inject failures. Production code always uses the os implementations.
type reportFS struct {
	CreateTemp func(dir, pattern string) (tempFile, error)
	Link       func(oldname, newname string) error
	Remove     func(name string) error
	Lstat      func(name string) (fs.FileInfo, error)
}

var fsOps = reportFS{
	CreateTemp: func(dir, pattern string) (tempFile, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err // avoid a non-nil interface holding a nil *os.File
		}
		return f, nil
	},
	Link:   os.Link,
	Remove: os.Remove,
	Lstat:  os.Lstat,
}

// prepareReport resolves the destination against its canonical parent directory
// and checks, before any command runs, that exclusive publication is possible:
// the destination must not exist (checked with Lstat, so dangling symlinks count)
// and the parent must accept a temporary file and a hard link to it.
//
// This is an early refusal, not a reservation. Something may still occupy the
// destination while commands run; saveReport's exclusive link stays authoritative.
func prepareReport(path string) (string, error) {
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return "", fmt.Errorf("report destination %q names a directory", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("report directory: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("report directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("report directory %s is not a directory", parent)
	}
	dest := filepath.Join(parent, filepath.Base(abs))
	if _, err := fsOps.Lstat(dest); err == nil {
		return "", fmt.Errorf("report destination %s already exists", dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := probeLink(parent); err != nil {
		return "", err
	}
	return dest, nil
}

// probeLink creates, hard-links and removes a temporary file in dir. Every
// cleanup failure is reported because it leaves files behind in the user's directory.
func probeLink(dir string) (err error) {
	f, err := fsOps.CreateTemp(dir, ".invaris-preflight-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() {
		if rmErr := fsOps.Remove(temp); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove preflight file: %w", rmErr))
		}
	}()
	if err := f.Close(); err != nil {
		return err
	}
	link := temp + ".link"
	if err := fsOps.Link(temp, link); err != nil {
		return err
	}
	if err := fsOps.Remove(link); err != nil {
		return fmt.Errorf("remove preflight link: %w", err)
	}
	return nil
}

// saveReport writes data to a temporary file in the destination directory and
// publishes it with a hard link, which fails if the destination exists. Any
// write, sync, close, link or cleanup error is returned.
func saveReport(path string, data []byte) (err error) {
	f, err := fsOps.CreateTemp(filepath.Dir(path), ".invaris-report-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() {
		if rmErr := fsOps.Remove(temp); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove temporary report: %w", rmErr))
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return fsOps.Link(temp, path)
}
