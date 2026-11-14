//go:build linux || darwin

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitPID polls for the descendant PID the helper records.
func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant did not start")
	return 0
}

// assertGone allows bounded termination polling. A Linux zombie awaiting
// reaping by its new parent counts as terminated.
func assertGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if runtime.GOOS == "linux" {
			if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
				if end := strings.LastIndex(string(stat), ") "); end >= 0 && strings.HasPrefix(string(stat)[end+2:], "Z ") {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived", pid)
}

func TestProcessGroupCancellation(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(root, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Run(ctx, root, helperCommand(t, "spawn", pidfile, "wait")) }()
	pid := waitPID(t, pidfile)
	defer syscall.Kill(pid, syscall.SIGKILL)
	started := time.Now()
	cancel()
	select {
	case r := <-done:
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("canceled run reported %+v", r)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("return took %s", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	assertGone(t, pid)
}

// A leader that exits successfully while a descendant still holds its stdout
// must not produce a successful result, and the descendant is terminated.
func TestPipeHoldingDescendantIsNotSuccess(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(root, "child.pid")
	started := time.Now()
	r := Run(context.Background(), root, helperCommand(t, "spawn", pidfile, "exit"))
	pid := waitPID(t, pidfile)
	defer syscall.Kill(pid, syscall.SIGKILL)
	if r.Err == nil {
		t.Fatalf("pipe-holding descendant produced success: %+v", r)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("pipe wait was not bounded: %s", elapsed)
	}
	assertGone(t, pid)
}
