//go:build linux || darwin

package runner

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

// configureProcess starts the command in its own process group so
// cancellation and timeout kill the whole group, not only the leader.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// terminateGroup kills descendants left in the group after the leader exits.
// The group ID cannot be reused while any member remains, so this cannot hit an
// unrelated process. Descendants that changed group or session are not reached.
func terminateGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func isExecutable(info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
