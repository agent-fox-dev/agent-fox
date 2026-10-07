//go:build !windows

package gitx

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in a process group of its own, so that
// killGroup reaches everything it starts.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the command's whole process group, falling back to the
// direct child when the group cannot be signalled.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
