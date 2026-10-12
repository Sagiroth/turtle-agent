//go:build linux

package main

import (
	"os/exec"
	"syscall"
	"testing"
)

// A supervisor that dies without forwarding anything must not leave children logged in:
// the kernel interrupts them, which is their clean logout.
func TestChildDiesWithParentOnLinux(t *testing.T) {
	cmd := exec.Command("true")
	detachProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pdeathsig != syscall.SIGINT {
		t.Fatalf("SysProcAttr %+v", cmd.SysProcAttr)
	}
}
