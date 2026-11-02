//go:build !windows

package connector

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session so it outlives the app and
// ignores signals sent to the app's process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
