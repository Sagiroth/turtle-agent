package addonhost

import (
	"os/exec"
	"syscall"
)

// setDeathSignal has the kernel kill the sidecar when the agent dies, even by SIGKILL.
func setDeathSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
