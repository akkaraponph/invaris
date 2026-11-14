//go:build !linux && !darwin

package engine

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
