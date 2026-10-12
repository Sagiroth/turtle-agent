//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// detachProcessGroup puts the child in its own process group: a terminal Ctrl-C reaches
// only the supervisor. Where the OS can, the child also gets an interrupt (its clean
// logout) if the supervisor dies without forwarding one, a SIGKILL for instance.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	dieWithParent(cmd.SysProcAttr)
}
