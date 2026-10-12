//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
