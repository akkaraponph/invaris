//go:build linux || darwin

package engine

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

func TestProcessGroupCancellationTerminatesChild(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		done <- execute(ctx, root, Command{ID: "process", Program: "/bin/sh", Args: []string{"-c", "sleep 60 & echo $! > child.pid; wait"}})
	}()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(root, "child.pid"))
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child did not start")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	select {
	case result := <-done:
		if result.Status != "REFUSE" {
			t.Fatalf("cancellation returned %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command did not return after cancellation")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		// Linux can retain an already-dead child as a zombie until its new parent reaps it.
		if runtime.GOOS == "linux" {
			if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
				if end := strings.LastIndex(string(stat), ") "); end >= 0 && strings.HasPrefix(string(stat)[end+2:], "Z ") {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child %d still exists after process-group cancellation", pid)
}
