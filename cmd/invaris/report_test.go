package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliBinary is built once so one test can check that mainExit delegates to runCLI.
var cliBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "invaris-cli-*")
	if err != nil {
		panic(err)
	}
	cliBinary = filepath.Join(dir, "invaris")
	if out, err := exec.Command("go", "build", "-o", cliBinary, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// invoke runs the CLI in process from cwd. Tests using it must not run in parallel:
// it changes the working directory and tests may replace fsOps.
func invoke(t *testing.T, cwd string, args ...string) (int, []byte, string) {
	t.Helper()
	return invokeWith(t, cwd, nil, args...)
}

func invokeWith(t *testing.T, cwd string, stdout io.Writer, args ...string) (int, []byte, string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	}()
	var out, stderr bytes.Buffer
	if stdout == nil {
		stdout = &out
	}
	code := runCLI(context.Background(), args, stdout, &stderr)
	return code, out.Bytes(), stderr.String()
}

// withFS replaces the report filesystem operations for one test.
func withFS(t *testing.T, edit func(*reportFS)) {
	t.Helper()
	saved := fsOps
	edit(&fsOps)
	t.Cleanup(func() { fsOps = saved })
}

var errInjected = errors.New("injected failure")

type faultyFile struct {
	tempFile
	write, sync, close bool
}

func (f faultyFile) Write(p []byte) (int, error) {
	if f.write {
		return 0, errInjected
	}
	return f.tempFile.Write(p)
}

func (f faultyFile) Sync() error {
	if f.sync {
		return errInjected
	}
	return f.tempFile.Sync()
}

func (f faultyFile) Close() error {
	err := f.tempFile.Close()
	if f.close {
		return errInjected
	}
	return err
}

type failingWriter struct{ calls int }

func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	return 0, errInjected
}

func writeConfig(t *testing.T, dir, steps string) string {
	t.Helper()
	config := filepath.Join(dir, "invaris.json")
	data := `{"version":"invaris.dev/v1alpha1","name":"test","steps":[` + steps + `]}`
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return config
}

func reportTemps(t *testing.T, dir string) []string {
	t.Helper()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var temps []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".invaris-") {
			temps = append(temps, e.Name())
		}
	}
	return temps
}

func TestCLIReportPublishCollision(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	config := writeConfig(t, dir, `{"id":"s","program":"sh","args":["-c","printf created-by-step > report.json"]}`)
	code, stdout, stderr := invoke(t, dir, "--report", report, "run", config)
	existing, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || len(stdout) != 0 {
		t.Fatalf("published a success after collision: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if string(existing) != "created-by-step" {
		t.Fatalf("overwrote destination: %q", existing)
	}
	if temps := reportTemps(t, dir); len(temps) != 0 {
		t.Fatalf("leaked report temp files: %v", temps)
	}
}

func TestCLIReportSuccessMatchesStdout(t *testing.T) {
	for _, relative := range []bool{false, true} {
		name := map[bool]string{false: "absolute", true: "relative"}[relative]
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			config := writeConfig(t, dir, `{"id":"s","program":"sh","args":["-c","exit 0"]}`)
			if err := os.Mkdir(filepath.Join(dir, "out"), 0o700); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(dir, "out", "report.json")
			arg := dest
			if relative {
				arg = filepath.Join("out", "report.json")
			}
			code, stdout, stderr := invoke(t, dir, "--report", arg, "run", config)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			saved, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if len(stdout) == 0 || !bytes.Equal(saved, stdout) {
				t.Fatalf("saved report differs from stdout:\nfile=%q\nstdout=%q", saved, stdout)
			}
			if temps := reportTemps(t, filepath.Join(dir, "out")); len(temps) != 0 {
				t.Fatalf("leaked report temp files: %v", temps)
			}
		})
	}
}

func TestMainExitDelegatesToRunCLI(t *testing.T) {
	dir := t.TempDir()
	config := writeConfig(t, dir, `{"id":"s","program":"sh","args":["-c","exit 0"]}`)
	report := filepath.Join(dir, "report.json")
	cmd := exec.Command(cliBinary, "--report", report, "run", config)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary run failed: %v", err)
	}
	saved, err := os.ReadFile(report)
	if err != nil || !bytes.Equal(saved, out) {
		t.Fatalf("binary report mismatch: %v", err)
	}
}

func TestCLIPreflightFaultsPreventExecution(t *testing.T) {
	isPreflight := func(name string) bool { return strings.Contains(filepath.Base(name), ".invaris-preflight-") }
	cases := []struct {
		name string
		edit func(*reportFS)
	}{
		{"create temp", func(f *reportFS) {
			f.CreateTemp = func(string, string) (tempFile, error) { return nil, errInjected }
		}},
		{"close temp", func(f *reportFS) {
			create := f.CreateTemp
			f.CreateTemp = func(dir, pattern string) (tempFile, error) {
				file, err := create(dir, pattern)
				if err != nil {
					return nil, err
				}
				return faultyFile{tempFile: file, close: true}, nil
			}
		}},
		{"link", func(f *reportFS) {
			f.Link = func(string, string) error { return errInjected }
		}},
		{"remove link", func(f *reportFS) {
			remove := f.Remove
			f.Remove = func(name string) error {
				if strings.HasSuffix(name, ".link") {
					remove(name)
					return errInjected
				}
				return remove(name)
			}
		}},
		{"remove temp", func(f *reportFS) {
			remove := f.Remove
			f.Remove = func(name string) error {
				if isPreflight(name) && !strings.HasSuffix(name, ".link") {
					remove(name)
					return errInjected
				}
				return remove(name)
			}
		}},
		{"lstat", func(f *reportFS) {
			f.Lstat = func(string) (fs.FileInfo, error) { return nil, errInjected }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "sentinel")
			config := writeConfig(t, dir, `{"id":"s","program":"touch","args":["`+sentinel+`"]}`)
			report := filepath.Join(dir, "report.json")
			withFS(t, tc.edit)
			code, stdout, stderr := invoke(t, dir, "--report", report, "run", config)
			if code != 2 || len(stdout) != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "injected failure") {
				t.Fatalf("stderr does not report the failure: %q", stderr)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("executed despite failed preflight: %v", err)
			}
			if _, err := os.Lstat(report); !os.IsNotExist(err) {
				t.Fatalf("destination created: %v", err)
			}
			if temps := reportTemps(t, dir); len(temps) != 0 {
				t.Fatalf("leaked temp files: %v", temps)
			}
		})
	}
}

func TestCLIPublishFaultsSuppressStdout(t *testing.T) {
	isSave := func(name string) bool { return strings.Contains(filepath.Base(name), ".invaris-report-") }
	faultyCreate := func(f *reportFS, file faultyFile) {
		create := f.CreateTemp
		f.CreateTemp = func(dir, pattern string) (tempFile, error) {
			t, err := create(dir, pattern)
			if err != nil || !strings.HasPrefix(pattern, ".invaris-report-") {
				return t, err
			}
			file.tempFile = t
			return file, nil
		}
	}
	cases := []struct {
		name      string
		edit      func(*reportFS)
		published bool // the report reached the destination before the failure
	}{
		{"create temp", func(f *reportFS) {
			create := f.CreateTemp
			f.CreateTemp = func(dir, pattern string) (tempFile, error) {
				if strings.HasPrefix(pattern, ".invaris-report-") {
					return nil, errInjected
				}
				return create(dir, pattern)
			}
		}, false},
		{"write", func(f *reportFS) { faultyCreate(f, faultyFile{write: true}) }, false},
		{"sync", func(f *reportFS) { faultyCreate(f, faultyFile{sync: true}) }, false},
		{"close", func(f *reportFS) { faultyCreate(f, faultyFile{close: true}) }, false},
		{"link", func(f *reportFS) {
			link := f.Link
			f.Link = func(old, new string) error {
				if isSave(old) {
					return errInjected
				}
				return link(old, new)
			}
		}, false},
		{"cleanup after publish", func(f *reportFS) {
			remove := f.Remove
			f.Remove = func(name string) error {
				err := remove(name)
				if isSave(name) {
					return errors.Join(err, errInjected)
				}
				return err
			}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := writeConfig(t, dir, `{"id":"s","program":"sh","args":["-c","exit 0"]}`)
			report := filepath.Join(dir, "report.json")
			withFS(t, tc.edit)
			code, stdout, stderr := invoke(t, dir, "--report", report, "run", config)
			if code != 2 || len(stdout) != 0 {
				t.Fatalf("passing verdict after publish failure: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "injected failure") {
				t.Fatalf("stderr does not report the failure: %q", stderr)
			}
			_, err := os.Lstat(report)
			if published := err == nil; published != tc.published {
				t.Fatalf("published=%v, want %v (%v)", published, tc.published, err)
			}
			if temps := reportTemps(t, dir); len(temps) != 0 {
				t.Fatalf("leaked temp files: %v", temps)
			}
		})
	}
}

func TestCLIStdoutWriteFailure(t *testing.T) {
	for _, withReport := range []bool{false, true} {
		t.Run(map[bool]string{false: "no report", true: "with report"}[withReport], func(t *testing.T) {
			dir := t.TempDir()
			config := writeConfig(t, dir, `{"id":"s","program":"sh","args":["-c","exit 0"]}`)
			args := []string{"run", config}
			if withReport {
				args = append([]string{"--report", "report.json"}, args...)
			}
			w := &failingWriter{}
			code, _, stderr := invokeWith(t, dir, w, args...)
			if code != 2 || w.calls == 0 {
				t.Fatalf("code=%d calls=%d stderr=%q", code, w.calls, stderr)
			}
			if !strings.Contains(stderr, "injected failure") {
				t.Fatalf("stderr does not report the failure: %q", stderr)
			}
		})
	}
}
