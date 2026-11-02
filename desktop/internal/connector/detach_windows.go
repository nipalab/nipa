//go:build windows

package connector

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess | createNoWindow,
	}
}
