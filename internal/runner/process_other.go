//go:build !linux && !darwin

package runner

import (
	"io/fs"
	"os/exec"
)

// Process-tree termination is not provided on this platform: cancellation
// kills only the direct child, and descendants may survive.
func configureProcess(_ *exec.Cmd) {}

func terminateGroup(_ *exec.Cmd) {}

func isExecutable(info fs.FileInfo) bool { return info.Mode().IsRegular() }
