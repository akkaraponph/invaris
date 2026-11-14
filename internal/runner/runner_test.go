package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperArg = "invaris-runner-helper"

// TestMain lets the test binary act as a deterministic subprocess. The mode is
// passed in argv because the runner deliberately withholds the parent's
// environment.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

func helper(mode string, args []string) int {
	switch mode {
	case "write": // write <stdout bytes> <stderr bytes>
		n, _ := strconv.Atoi(args[0])
		e, _ := strconv.Atoi(args[1])
		os.Stdout.Write([]byte(strings.Repeat("o", n)))
		os.Stderr.Write([]byte(strings.Repeat("e", e)))
	case "argv":
		json.NewEncoder(os.Stdout).Encode(os.Args[3:])
	case "env":
		json.NewEncoder(os.Stdout).Encode(os.Environ())
	case "stdin":
		data, _ := readAll(os.Stdin)
		os.Stdout.Write(data)
	case "exit":
		code, _ := strconv.Atoi(args[0])
		return code
	case "sleep":
		time.Sleep(time.Minute)
	case "spawn": // spawn <pidfile> <wait|exit>: start a descendant that holds stdout
		self, _ := os.Executable()
		child := exec.Command(self, helperArg, "sleep")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return 3
		}
		if err := os.WriteFile(args[0], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			return 3
		}
		if args[1] == "wait" {
			child.Wait()
		}
	}
	return 0
}

func readAll(f *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, nil
		}
	}
}

func helperCommand(t *testing.T, args ...string) Command {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Program: self, Args: append([]string{helperArg}, args...), Timeout: 10 * time.Second}
}

func TestRunnerLimits(t *testing.T) {
	for _, tc := range []struct {
		name           string
		out, errs      int
		truncated      bool
		wantOut, wantE int
	}{
		{"stdout exactly at limit", MaxStreamBytes, 0, false, MaxStreamBytes, 0},
		{"stdout one over limit", MaxStreamBytes + 1, 0, true, MaxStreamBytes, 0},
		{"stderr exactly at limit", 0, MaxStreamBytes, false, 0, MaxStreamBytes},
		{"stderr one over limit", 0, MaxStreamBytes + 1, true, 0, MaxStreamBytes},
		{"both at limit are separate", MaxStreamBytes, MaxStreamBytes, false, MaxStreamBytes, MaxStreamBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Run(context.Background(), t.TempDir(), helperCommand(t, "write", strconv.Itoa(tc.out), strconv.Itoa(tc.errs)))
			if r.Truncated != tc.truncated || len(r.Stdout) != tc.wantOut || len(r.Stderr) != tc.wantE {
				t.Fatalf("truncated=%v stdout=%d stderr=%d err=%v", r.Truncated, len(r.Stdout), len(r.Stderr), r.Err)
			}
			if strings.Trim(r.Stdout, "o") != "" || strings.Trim(r.Stderr, "e") != "" {
				t.Fatal("streams were mixed")
			}
			if tc.truncated != errors.Is(r.Err, ErrOutputLimit) {
				t.Fatalf("err = %v, truncated %v", r.Err, tc.truncated)
			}
			if !tc.truncated && (r.Err != nil || r.ExitCode == nil || *r.ExitCode != 0) {
				t.Fatalf("complete output not accepted: %+v", r.Err)
			}
		})
	}

	t.Run("missing executable", func(t *testing.T) {
		for _, program := range []string{"invaris-no-such-program", "/nonexistent/invaris"} {
			r := Run(context.Background(), t.TempDir(), Command{Program: program})
			if r.Err == nil || r.ExitCode != nil {
				t.Fatalf("%s: %+v", program, r)
			}
		}
	})

	t.Run("canceled context never spawns", func(t *testing.T) {
		root := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := Run(ctx, root, Command{Program: "/bin/sh", Args: []string{"-c", "echo x > spawned"}})
		if !errors.Is(r.Err, context.Canceled) || r.ExitCode != nil {
			t.Fatalf("%+v", r)
		}
		if _, err := os.Stat(filepath.Join(root, "spawned")); !os.IsNotExist(err) {
			t.Fatal("process spawned after cancellation")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		cmd := helperCommand(t, "sleep")
		cmd.Timeout = 100 * time.Millisecond
		started := time.Now()
		r := Run(context.Background(), t.TempDir(), cmd)
		if !errors.Is(r.Err, context.DeadlineExceeded) || time.Since(started) > 5*time.Second {
			t.Fatalf("%+v after %s", r, time.Since(started))
		}
	})

	t.Run("nonzero exit", func(t *testing.T) {
		r := Run(context.Background(), t.TempDir(), helperCommand(t, "exit", "7"))
		var exit *exec.ExitError
		if !errors.As(r.Err, &exit) || r.ExitCode == nil || *r.ExitCode != 7 {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("literal shell metacharacters", func(t *testing.T) {
		root := t.TempDir()
		args := []string{"$(touch pwned)", "`touch pwned`", "; touch pwned", "| cat", "*", "$HOME", "a b", "", "'\"\\"}
		cmd := helperCommand(t, append([]string{"argv"}, args...)...)
		r := Run(context.Background(), root, cmd)
		var got []string
		if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &got) != nil {
			t.Fatalf("%+v", r)
		}
		if fmt.Sprint(got) != fmt.Sprint(args) || len(got) != len(args) {
			t.Fatalf("argv changed: %q", got)
		}
		if _, err := os.Stat(filepath.Join(root, "pwned")); !os.IsNotExist(err) {
			t.Fatal("shell interpreted arguments")
		}
	})

	t.Run("environment allowlist", func(t *testing.T) {
		t.Setenv("INVARIS_TEST_SECRET", "do-not-leak")
		t.Setenv("INVARIS_TEST_ALLOWED", "visible")
		cmd := helperCommand(t, "env")
		cmd.EnvAllowlist = []string{"INVARIS_TEST_ALLOWED", "INVARIS_TEST_UNSET"}
		r := Run(context.Background(), t.TempDir(), cmd)
		var env []string
		if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &env) != nil {
			t.Fatalf("%+v", r)
		}
		joined := strings.Join(env, "\n")
		if strings.Contains(joined, "do-not-leak") || strings.Contains(joined, "INVARIS_TEST_SECRET") || strings.Contains(joined, "INVARIS_TEST_UNSET") {
			t.Fatalf("non-allowlisted variable leaked: %q", env)
		}
		if !strings.Contains(joined, "INVARIS_TEST_ALLOWED=visible") || !strings.Contains(joined, "PATH="+DefaultPath) {
			t.Fatalf("expected allowlisted variable and default PATH: %q", env)
		}
	})

	t.Run("stdin", func(t *testing.T) {
		r := RunInput(context.Background(), t.TempDir(), helperCommand(t, "stdin"), []byte(`{"request":1}`))
		if r.Err != nil || r.Stdout != `{"request":1}` {
			t.Fatalf("%+v", r)
		}
	})
}

func TestResolveUsesEffectivePath(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "invaris-probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\necho probe-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if r := Run(context.Background(), t.TempDir(), Command{Program: "invaris-probe"}); r.Err == nil {
		t.Fatalf("resolved through ambient parent PATH: %+v", r)
	}
	r := Run(context.Background(), t.TempDir(), Command{Program: "invaris-probe", EnvAllowlist: []string{"PATH"}})
	if r.Err != nil || r.Stdout != "probe-ran\n" {
		t.Fatalf("allowlisted PATH not used: %+v", r)
	}
	if r := Run(context.Background(), t.TempDir(), Command{Program: "sh", Args: []string{"-c", "echo $PATH"}}); r.Err != nil || r.Stdout != DefaultPath+"\n" {
		t.Fatalf("default PATH: %+v", r)
	}
	// Relative PATH entries never resolve against the workspace.
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "bin"), 0o755)
	os.WriteFile(filepath.Join(root, "bin", "invaris-local"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", "bin")
	if r := Run(context.Background(), root, Command{Program: "invaris-local", EnvAllowlist: []string{"PATH"}}); r.Err == nil {
		t.Fatal("resolved a relative PATH entry")
	}
}
