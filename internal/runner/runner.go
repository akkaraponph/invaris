// Package runner executes bounded, trusted local subprocesses.
//
// Arguments are passed as a literal argv vector and are never interpreted by a
// shell. The runner is not a security sandbox: a trusted command can still read
// or write outside its working directory, use the network, or start detached
// descendants that escape process-group termination.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultPath is the PATH used when PATH is not in a command's allowlist.
	DefaultPath = "/usr/bin:/bin"
	// DefaultTimeout applies when Command.Timeout is zero.
	DefaultTimeout = 30 * time.Second
	// MaxStreamBytes bounds each captured stream separately.
	MaxStreamBytes = 64 * 1024
	// PipeWaitLimit bounds how long Wait waits for I/O after the process exits
	// or is killed, for example when a descendant still holds a pipe.
	PipeWaitLimit = 250 * time.Millisecond
)

// ErrOutputLimit reports that a stream exceeded MaxStreamBytes.
var ErrOutputLimit = errors.New("output limit exceeded")

// Command is a literal argv invocation. Timeout zero means DefaultTimeout.
type Command struct {
	Program      string
	Args         []string
	EnvAllowlist []string
	Timeout      time.Duration
}

// Result records a completed or refused invocation. ExitCode is nil when the
// process never started. Err is nil only for a zero exit with complete,
// untruncated output; it wraps the context error on cancellation or timeout,
// is an *exec.ExitError for a nonzero exit, and is ErrOutputLimit on truncation.
type Result struct {
	Stdout, Stderr string
	// Executable is the resolved program path; empty when it did not resolve.
	Executable            string
	ExitCode              *int
	StartedAt, FinishedAt time.Time
	Truncated             bool
	Err                   error
}

// Run executes command in root without stdin.
func Run(ctx context.Context, root string, command Command) Result {
	return RunInput(ctx, root, command, nil)
}

// RunInput executes command in root, supplying stdin when it is non-nil.
func RunInput(ctx context.Context, root string, command Command, stdin []byte) (result Result) {
	result.StartedAt = time.Now().UTC()
	// result is a named return so the deferred update reaches the caller.
	defer func() { result.FinishedAt = time.Now().UTC() }()
	if err := ctx.Err(); err != nil {
		result.Err = fmt.Errorf("not started: %w", err)
		return result
	}
	env, path := environment(command.EnvAllowlist)
	program, err := resolve(command.Program, path)
	if err != nil {
		result.Err = err
		return result
	}
	result.Executable = program
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runctx, program, command.Args...)
	cmd.Args[0] = command.Program // keep argv literal; only the executable path is resolved
	cmd.Dir = root
	cmd.Env = env
	cmd.WaitDelay = PipeWaitLimit
	configureProcess(cmd)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout, stderr := &limitedWriter{max: MaxStreamBytes}, &limitedWriter{max: MaxStreamBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	terminateGroup(cmd)
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		result.ExitCode = &code
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.Truncated = stdout.Exceeded() || stderr.Exceeded()
	switch {
	case runctx.Err() != nil:
		result.Err = runctx.Err()
	case result.Truncated:
		result.Err = ErrOutputLimit
	default:
		result.Err = err
	}
	return result
}

// environment returns the child environment and its effective PATH. Only
// allowlisted variables present in the parent are inherited.
func environment(allowlist []string) ([]string, string) {
	path := DefaultPath
	env := []string{}
	seen := map[string]bool{}
	for _, key := range allowlist {
		if seen[key] {
			continue
		}
		seen[key] = true
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		if key == "PATH" {
			path = value
			continue
		}
		env = append(env, key+"="+value)
	}
	return append([]string{"PATH=" + path}, env...), path
}

// resolve finds a bare program name in the effective PATH rather than the
// parent's ambient PATH. Names containing a separator are used as given
// (relative names are relative to the command's working directory). Relative
// and empty PATH entries are ignored so lookup never depends on the workspace.
func resolve(program, path string) (string, error) {
	if program == "" {
		return "", errors.New("empty program")
	}
	if strings.ContainsRune(program, '/') || strings.ContainsRune(program, filepath.Separator) {
		return program, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, program)
		if info, err := os.Stat(candidate); err == nil && isExecutable(info) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable %q not found in PATH %q: %w", program, path, exec.ErrNotFound)
}

// Lookup reports the executable Run would start for program, applying the
// same environment and PATH rules without starting anything. A bare name is
// searched only in the effective PATH; a name with a separator is used as
// given, relative to dir, and must be an executable regular file.
func Lookup(program string, envAllowlist []string, dir string) (string, error) {
	_, path := environment(envAllowlist)
	found, err := resolve(program, path)
	if err != nil {
		return "", err
	}
	if !strings.ContainsRune(program, '/') && !strings.ContainsRune(program, filepath.Separator) {
		return found, nil
	}
	if !filepath.IsAbs(found) {
		found = filepath.Join(dir, found)
	}
	info, err := os.Stat(found)
	if err != nil {
		return "", fmt.Errorf("executable %q: %w", program, err)
	}
	if !info.Mode().IsRegular() || !isExecutable(info) {
		return "", fmt.Errorf("executable %q is not an executable regular file", program)
	}
	return found, nil
}

type limitedWriter struct {
	mu       sync.Mutex
	b        bytes.Buffer
	max      int
	exceeded bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	left := w.max - w.b.Len()
	if left > len(p) {
		left = len(p)
	}
	if left > 0 {
		w.b.Write(p[:left])
	}
	if len(p) > left {
		w.exceeded = true
	}
	return len(p), nil
}

func (w *limitedWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }
func (w *limitedWriter) Exceeded() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.exceeded }
