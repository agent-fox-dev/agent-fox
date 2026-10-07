//go:build windows

package gitx

import (
	"os/exec"
	"strconv"
)

// setProcessGroup has nothing to do on Windows: killGroup ends the tree by
// process id.
func setProcessGroup(*exec.Cmd) {}

// killGroup ends the command and everything it started, with taskkill /T,
// falling back to the direct child.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
