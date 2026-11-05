//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyResize(sigs chan<- os.Signal) {
	signal.Notify(sigs, syscall.SIGWINCH)
}
